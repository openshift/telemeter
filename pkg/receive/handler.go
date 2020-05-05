package receive

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/prompb"
)

const forwardTimeout = 5 * time.Second

// DefaultRequestLimit is the size limit of a request body coming in
const DefaultRequestLimit = 15 * 1024 // based on historic Prometheus data with 6KB at most

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

	// Metrics.
	forwardRequestsTotal *prometheus.CounterVec
}

// NewHandler returns a new Handler with a http client
func NewHandler(logger log.Logger, forwardURL string, reg prometheus.Registerer, tenantID string) *Handler {
	h := &Handler{
		ForwardURL: forwardURL,
		tenantID:   tenantID,
		client: &http.Client{
			Timeout: forwardTimeout,
		},
		logger: log.With(logger, "component", "receive/handler"),
		forwardRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "telemeter_forward_requests_total",
				Help: "The number of forwarded remote-write requests.",
			}, []string{"result"},
		),
	}

	if reg != nil {
		reg.MustRegister(h.forwardRequestsTotal)
	}

	return h
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
	req = req.WithContext(ctx)
	req.Header.Add("THANOS-TENANT", h.tenantID)

	resp, err := h.client.Do(req)
	if err != nil {
		h.forwardRequestsTotal.WithLabelValues("error").Inc()
		level.Error(h.logger).Log("msg", "failed to forward request", "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if resp.StatusCode/100 != 2 {
		msg := "upstream response status is not 200 OK"
		h.forwardRequestsTotal.WithLabelValues("error").Inc()
		level.Error(h.logger).Log("msg", msg, "statuscode", resp.Status)
		http.Error(w, msg, resp.StatusCode)
		return
	}
	h.forwardRequestsTotal.WithLabelValues("success").Inc()
	w.WriteHeader(resp.StatusCode)
}

// LimitBodySize is a middleware that check that the request body is not bigger than the limit
func LimitBodySize(limit int64, next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

		// Set body to this buffer for other handlers to read
		r.Body = ioutil.NopCloser(bytes.NewBuffer(body))

		if len(body) >= int(limit) {
			http.Error(w, "request too big", http.StatusRequestEntityTooLarge)
			return
		}

		next.ServeHTTP(w, r)
	}
}

// ErrRequiredLabelMissing is returned if a required label is missing from a metric
var ErrRequiredLabelMissing = fmt.Errorf("a required label is missing from the metric")

// ValidateLabels by checking each enforced label to be present in every time series
func ValidateLabels(logger log.Logger, next http.Handler, labels ...string) http.HandlerFunc {
	logger = log.With(logger, "component", "receive", "middleware", "validateLabels")

	labelmap := make(map[string]struct{})
	for _, label := range labels {
		labelmap[label] = struct{}{}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			level.Error(logger).Log("msg", "failed to read body", "err", err)
			http.Error(w, "failed to read body", http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

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
