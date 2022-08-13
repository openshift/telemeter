package receive

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"time"
	"unsafe"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/promql"
)

const forwardTimeout = 5 * time.Second

// DefaultRequestLimit is the size limit of a request body coming in
const DefaultRequestLimit = 128 * 1024

// ClusterAuthorizer authorizes a cluster by its token and id, returning a subject or error
type ClusterAuthorizer interface {
	AuthorizeCluster(token, cluster string) (subject string, err error)
}

// Handler knows the forwardURL for all requests
type Handler struct {
	ForwardURL string
	tenantID   string
	client     *http.Client
	logger     log.Logger

	elideLabelSet map[string]struct{}
	matcherSets   [][]*labels.Matcher
	// Metrics.
	forwardRequestsTotal *prometheus.CounterVec
}

// NewHandler returns a new Handler with a http client
func NewHandler(logger log.Logger, forwardURL string, client *http.Client, reg prometheus.Registerer, tenantID string, whitelistRules []string, elideLabels []string) (*Handler, error) {
	h := &Handler{
		ForwardURL: forwardURL,
		tenantID:   tenantID,
		client:     client,
		logger:     log.With(logger, "component", "receive/handler"),
		forwardRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "telemeter_forward_requests_total",
				Help: "The number of forwarded remote-write requests.",
			}, []string{"result"},
		),
	}

	var ms [][]*labels.Matcher
	for _, rule := range whitelistRules {
		matchers, err := promql.ParseMetricSelector(rule)
		if err != nil {
			return nil, err
		}
		ms = append(ms, matchers)
	}
	h.matcherSets = ms

	h.elideLabelSet = make(map[string]struct{})
	for _, l := range elideLabels {
		h.elideLabelSet[l] = struct{}{}
	}

	if reg != nil {
		reg.MustRegister(h.forwardRequestsTotal)
	}

	return h, nil
}

// Receive a remote-write request after it has been authenticated and forward it to Thanos
func (h *Handler) Receive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	ctx, cancel := context.WithTimeout(r.Context(), forwardTimeout)
	defer cancel()

	req, err := http.NewRequest(http.MethodPost, h.ForwardURL, r.Body)
	if err != nil {
		level.Error(h.logger).Log("msg", "failed to create forward request", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// No need for adding a tenant header, as this is done by downstream Observatorium API.
	req = req.WithContext(ctx)

	resp, err := h.client.Do(req)
	if err != nil {
		h.forwardRequestsTotal.WithLabelValues("error").Inc()
		level.Error(h.logger).Log("msg", "failed to forward request", "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if resp.StatusCode/100 != 2 {
		// Return upstream error as well.
		body, err := ioutil.ReadAll(resp.Body)
		msg := fmt.Sprintf("upstream response status is not 200 OK: %s", body)
		if err != nil {
			msg = fmt.Sprintf("upstream response status is not 200 OK: couldn't read body %v", err)
		}
		h.forwardRequestsTotal.WithLabelValues("error").Inc()
		level.Error(h.logger).Log("msg", msg, "statuscode", resp.Status)
		http.Error(w, msg, resp.StatusCode)
		return
	}
	h.forwardRequestsTotal.WithLabelValues("success").Inc()
	w.WriteHeader(resp.StatusCode)
}

// LimitBodySize is a middleware that check that the request body is not bigger than the limit
func LimitBodySize(logger log.Logger, limit int64, next http.Handler) http.HandlerFunc {
	logger = log.With(logger, "component", "receive", "middleware", "LimitBodySize")
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			level.Error(logger).Log("msg", "failed to read body", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Set body to this buffer for other handlers to read
		r.Body = ioutil.NopCloser(bytes.NewBuffer(body))

		if len(body) >= int(limit) {
			level.Debug(logger).Log("msg", "request is too big", "req_size", len(body))
			http.Error(w, "request too big", http.StatusRequestEntityTooLarge)
			return
		}

		next.ServeHTTP(w, r)
	}
}

var ErrRequiredLabelMissing = fmt.Errorf("a required label is missing from the metric")

// ValidateLabels by checking each enforced label to be present in every time series
func ValidateLabels(logger log.Logger, next http.Handler, labels ...string) http.HandlerFunc {
	logger = log.With(logger, "component", "receive", "middleware", "validateLabels")

	labelmap := make(map[string]struct{})
	for _, label := range labels {
		labelmap[label] = struct{}{}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			level.Error(logger).Log("msg", "failed to read body", "err", err)
			http.Error(w, "failed to read body", http.StatusInternalServerError)
			return
		}

		// Set body to this buffer for other handlers to read
		r.Body = ioutil.NopCloser(bytes.NewBuffer(body))

		content, err := snappy.Decode(nil, body)
		if err != nil {
			level.Warn(logger).Log("msg", "failed to decode request body", "err", err)
			http.Error(w, "failed to decode request body", http.StatusBadRequest)
			return
		}

		var wreq prompb.WriteRequest
		if err := proto.Unmarshal(content, &wreq); err != nil {
			level.Warn(logger).Log("msg", "failed to decode protobuf from body", "err", err)
			http.Error(w, "failed to decode protobuf from body", http.StatusBadRequest)
			return
		}

		for _, ts := range wreq.GetTimeseries() {
			// exit early if not enough labels anyway
			if len(ts.GetLabels()) < len(labels) {
				level.Warn(logger).Log("msg", "request is missing required labels", "err", ErrRequiredLabelMissing)
				http.Error(w, ErrRequiredLabelMissing.Error(), http.StatusBadRequest)
				return
			}

			found := 0

			for _, l := range ts.GetLabels() {
				if _, ok := labelmap[l.GetName()]; ok {
					found++
				}
			}

			if len(labels) != found {
				level.Warn(logger).Log("msg", "request is missing required labels", "err", ErrRequiredLabelMissing)
				http.Error(w, ErrRequiredLabelMissing.Error(), http.StatusBadRequest)
				return
			}
		}

		next.ServeHTTP(w, r)
	}
}

func (h *Handler) TransformWriteRequest(logger log.Logger, next http.Handler) http.HandlerFunc {
	logger = log.With(h.logger, "middleware", "transformWriteRequest")
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			level.Error(logger).Log("msg", "failed to read body", "err", err)
			http.Error(w, "failed to read body", http.StatusInternalServerError)
			return
		}

		content, err := snappy.Decode(nil, body)
		if err != nil {
			level.Warn(logger).Log("msg", "failed to decode request body", "err", err)
			http.Error(w, "failed to decode request body", http.StatusBadRequest)
			return
		}

		var wreq prompb.WriteRequest
		if err := proto.Unmarshal(content, &wreq); err != nil {
			level.Warn(logger).Log("msg", "failed to decode protobuf from body", "err", err)
			http.Error(w, "failed to decode protobuf from body", http.StatusBadRequest)
			return
		}

		// Only allow whitelisted metrics.
		n := 0
		for _, ts := range wreq.GetTimeseries() {
			if h.matches(PrompbLabelsToPromLabels(ts.GetLabels())) {
				// Remove elided labels.
				for i, l := range ts.Labels {
					if _, elide := h.elideLabelSet[l.Name]; elide {
						ts.Labels = append(ts.Labels[:i], ts.Labels[i+1:]...)
					}
				}
				wreq.Timeseries[n] = ts
				n++
			}
		}
		wreq.Timeseries = wreq.Timeseries[:n]

		data, err := proto.Marshal(&wreq)
		if err != nil {
			msg := "failed to marshal proto"
			level.Warn(logger).Log("msg", msg, "err", err)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}

		compressed := snappy.Encode(nil, data)

		// Set body to this buffer for other handlers to read.
		r.Body = ioutil.NopCloser(bytes.NewBuffer(compressed))

		next.ServeHTTP(w, r)
	}
}

func (h *Handler) matches(l labels.Labels) bool {
	if len(h.matcherSets) == 0 {
		return true
	}

	for _, matchers := range h.matcherSets {
		for _, m := range matchers {
			if v := l.Get(m.Name); !m.Matches(v) {
				return false
			}
		}
	}
	return true
}

func PrompbLabelsToPromLabels(lset []prompb.Label) labels.Labels {
	return *(*labels.Labels)(unsafe.Pointer(&lset))
}
