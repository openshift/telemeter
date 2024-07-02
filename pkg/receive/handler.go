package receive

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"sort"
	"time"
	"unsafe"

	"github.com/go-chi/chi/middleware"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
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
	forwardRequestsTotal   *prometheus.CounterVec
	requestBodySizeLimited prometheus.Counter
	requestMissingLabels   prometheus.Counter
	seriesProcessedTotal   prometheus.Counter
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
		requestBodySizeLimited: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "telemeter_receive_size_limited_total",
				Help: "The number of remote write requests dropped due to body size.",
			},
		),
		requestMissingLabels: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "telemeter_receive_request_missing_labels_total",
				Help: "The number of remote write requests dropped due to missing labels.",
			},
		),
		seriesProcessedTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "telemeter_receive_series_processed_total",
				Help: "The total number of series processed by telemeter receive.",
			},
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
		reg.MustRegister(h.requestBodySizeLimited)
		reg.MustRegister(h.requestMissingLabels)
		reg.MustRegister(h.seriesProcessedTotal)
	}

	return h, nil
}

// Receive a remote-write request after it has been authenticated and forward it to Thanos
func (h *Handler) Receive(w http.ResponseWriter, r *http.Request) {
	logger := log.With(h.logger, "request", middleware.GetReqID(r.Context()))

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	ctx, cancel := context.WithTimeout(r.Context(), forwardTimeout)
	defer cancel()

	req, err := http.NewRequest(http.MethodPost, h.ForwardURL, r.Body)
	if err != nil {
		h.forwardRequestsTotal.WithLabelValues("error").Inc()
		level.Error(logger).Log("msg", "failed to create forward request", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// No need for adding a tenant header, as this is done by downstream Observatorium API.
	req = req.WithContext(ctx)

	resp, err := h.client.Do(req)
	if err != nil {
		h.forwardRequestsTotal.WithLabelValues("error").Inc()
		level.Error(logger).Log("msg", "failed to forward request", "err", err)
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
		level.Error(logger).Log("msg", msg, "statuscode", resp.Status)
		http.Error(w, msg, resp.StatusCode)
		return
	}
	h.forwardRequestsTotal.WithLabelValues("success").Inc()
	w.WriteHeader(resp.StatusCode)
}

// LimitBodySize is a middleware that check that the request body is not bigger than the limit
func (h *Handler) LimitBodySize(limit int64, next http.Handler) http.HandlerFunc {
	logger := log.With(h.logger, "middleware", "LimitBodySize")

	return func(w http.ResponseWriter, r *http.Request) {
		logger := log.With(logger, "request", middleware.GetReqID(r.Context()))

		defer r.Body.Close()
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			h.forwardRequestsTotal.WithLabelValues("error").Inc()
			level.Error(logger).Log("msg", "failed to read body", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Set body to this buffer for other handlers to read
		r.Body = ioutil.NopCloser(bytes.NewBuffer(body))

		if len(body) >= int(limit) {
			level.Warn(logger).Log("msg", "request is too big", "req_size", len(body))
			h.requestBodySizeLimited.Inc()
			http.Error(w, "request too big", http.StatusRequestEntityTooLarge)
			return
		}

		next.ServeHTTP(w, r)
	}
}

var ErrRequiredLabelMissing = fmt.Errorf("a required label is missing from the metric")

func (h *Handler) TransformAndValidateWriteRequest(logger log.Logger, next http.Handler, labels ...string) http.HandlerFunc {
	logger = log.With(h.logger, "middleware", "transformAndValidateWriteRequest")
	return func(w http.ResponseWriter, r *http.Request) {
		logger := log.With(logger, "request", middleware.GetReqID(r.Context()))

		body, err := io.ReadAll(r.Body)
		defer r.Body.Close()
		if err != nil {
			h.forwardRequestsTotal.WithLabelValues("error").Inc()
			level.Error(logger).Log("msg", "failed to read body", "err", err)
			http.Error(w, "failed to read body", http.StatusInternalServerError)
			return
		}

		content, err := snappy.Decode(nil, body)
		if err != nil {
			h.forwardRequestsTotal.WithLabelValues("error").Inc()
			level.Warn(logger).Log("msg", "failed to decode request body", "err", err)
			http.Error(w, "failed to decode request body", http.StatusBadRequest)
			return
		}

		var wreq prompb.WriteRequest
		if err := proto.Unmarshal(content, &wreq); err != nil {
			h.forwardRequestsTotal.WithLabelValues("error").Inc()
			level.Warn(logger).Log("msg", "failed to decode protobuf from body", "err", err)
			http.Error(w, "failed to decode protobuf from body", http.StatusBadRequest)
			return
		}

		labelmap := make(map[string]struct{})
		for _, label := range labels {
			labelmap[label] = struct{}{}
		}

		level.Debug(logger).Log("msg", "timeseries received: "+fmt.Sprint(len(wreq.Timeseries)))

		// Only allow whitelisted & sanitized metrics.
		n := 0
		for _, ts := range wreq.GetTimeseries() {
			level.Debug(logger).Log("msg", "labels received", "timeseries", ts.String())

			// Check required labels.
			// exit early if not enough labels anyway.
			if len(ts.GetLabels()) < len(labels) {
				level.Warn(logger).Log("msg", "request is missing required labels", "err", ErrRequiredLabelMissing)
				h.requestMissingLabels.Inc()
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
				h.requestMissingLabels.Inc()
				http.Error(w, ErrRequiredLabelMissing.Error(), http.StatusBadRequest)
				return
			}

			whitelisted := h.isWhitelisted(PrompbLabelsToPromLabels(ts.GetLabels()))
			level.Debug(logger).Log("msg", "labels comply with matchers", "match", whitelisted)
			if whitelisted {
				lbls := ts.Labels[:0]
				dedup := make(map[string]struct{})
				for _, l := range ts.Labels {
					// Skip empty labels.
					if l.Name == "" || l.Value == "" {
						continue
					}
					// Check for duplicates.
					if _, ok := dedup[l.Name]; ok {
						continue
					}
					// Remove elided labels.
					if _, elide := h.elideLabelSet[l.Name]; elide {
						continue
					}

					lbls = append(lbls, l)
					dedup[l.Name] = struct{}{}
				}

				// Sort labels.
				sortLabels(lbls)
				ts.Labels = lbls

				level.Debug(logger).Log("msg", "sanitized labels", "result timeseries", ts.String())

				wreq.Timeseries[n] = ts
				h.seriesProcessedTotal.Inc()
				n++
			}
		}
		wreq.Timeseries = wreq.Timeseries[:n]

		if len(wreq.Timeseries) == 0 {
			level.Warn(logger).Log("msg", "empty remote write request after telemeter processing")
			http.Error(w, "empty remote write request after telemeter processing", http.StatusBadRequest)
			return
		}

		data, err := proto.Marshal(&wreq)
		if err != nil {
			h.forwardRequestsTotal.WithLabelValues("error").Inc()
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

func (h *Handler) isWhitelisted(l labels.Labels) bool {
	if len(h.matcherSets) == 0 {
		return true
	}

	var ok bool
	for _, matchers := range h.matcherSets {
		if matches(l, matchers...) {
			ok = true
		}
	}

	return ok
}

func matches(l labels.Labels, matchers ...*labels.Matcher) bool {
Matcher:
	for _, m := range matchers {
		for _, lbl := range l {
			if lbl.Name != m.Name || !m.Matches(lbl.Value) {
				continue
			}
			continue Matcher
		}
		return false
	}
	return true
}

func sortLabels(labels []prompb.Label) {
	lset := sortableLabels(labels)
	sort.Sort(&lset)
}

// Extension on top of prompb.Label to allow for easier sorting.
// Based on https://github.com/prometheus/prometheus/blob/main/model/labels/labels.go#L44
type sortableLabels []prompb.Label

func (sl *sortableLabels) Len() int           { return len(*sl) }
func (sl *sortableLabels) Swap(i, j int)      { (*sl)[i], (*sl)[j] = (*sl)[j], (*sl)[i] }
func (sl *sortableLabels) Less(i, j int) bool { return (*sl)[i].Name < (*sl)[j].Name }

func PrompbLabelsToPromLabels(lset []prompb.Label) labels.Labels {
	return *(*labels.Labels)(unsafe.Pointer(&lset))
}
