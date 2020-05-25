package server

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// InstrumentationMiddleware holds necessary metrics to instrument an http.Server
// and provides a middleware that monitors HTTP requests and responses.
type InstrumentationMiddleware interface {
	// NewHandler returns an HTTP middleware that monitors HTTP requests and responses.
	NewHandler(handlerName string, handler http.Handler) http.HandlerFunc
}

type nopInstrumentationMiddleware struct{}

func (ins nopInstrumentationMiddleware) NewHandler(handlerName string, handler http.Handler) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	})
}

// NewNopInstrumentationMiddleware provides a InstrumentationMiddleware which does nothing.
func NewNopInstrumentationMiddleware() InstrumentationMiddleware {
	return nopInstrumentationMiddleware{}
}

type defaultInstrumentationMiddleware struct {
	requestDuration *prometheus.HistogramVec
	requestSize     *prometheus.SummaryVec
	requestsTotal   *prometheus.CounterVec
}

// NewInstrumentationMiddleware provides default InstrumentationMiddleware.
func NewInstrumentationMiddleware(reg prometheus.Registerer) InstrumentationMiddleware {
	ins := defaultInstrumentationMiddleware{
		requestDuration: promauto.With(reg).NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "http_request_duration_seconds",
				Help: "Tracks the latencies for HTTP requests.",
			},
			[]string{"code", "handler", "method"},
		),

		requestSize: promauto.With(reg).NewSummaryVec(
			prometheus.SummaryOpts{
				Name: "http_request_size_bytes",
				Help: "Tracks the size of HTTP requests.",
			},
			[]string{"code", "handler", "method"},
		),

		requestsTotal: promauto.With(reg).NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Tracks the number of HTTP requests.",
			}, []string{"code", "handler", "method"},
		),
	}
	return &ins
}

// NewHandler returns an HTTP middleware that monitors HTTP requests and responses.
func (ins *defaultInstrumentationMiddleware) NewHandler(handlerName string, next http.Handler) http.HandlerFunc {
	return promhttp.InstrumentHandlerDuration(
		ins.requestDuration.MustCurryWith(prometheus.Labels{"handler": handlerName}),
		promhttp.InstrumentHandlerRequestSize(
			ins.requestSize.MustCurryWith(prometheus.Labels{"handler": handlerName}),
			promhttp.InstrumentHandlerCounter(
				ins.requestsTotal.MustCurryWith(prometheus.Labels{"handler": handlerName}),
				next,
			),
		),
	)
}
