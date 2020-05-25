package server

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestDuration = promauto.With(prometheus.DefaultRegisterer).NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "http_request_duration_seconds",
			Help: "Tracks the latencies for HTTP requests.",
		},
		[]string{"code", "handler", "method"},
	)

	requestSize = promauto.With(prometheus.DefaultRegisterer).NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "http_request_size_bytes",
			Help: "Tracks the size of HTTP requests.",
		},
		[]string{"code", "handler", "method"},
	)

	requestsTotal = promauto.With(prometheus.DefaultRegisterer).NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Tracks the number of HTTP requests.",
		}, []string{"code", "handler", "method"},
	)
)

// InstrumentedHandler is an HTTP middleware that monitors HTTP requests and responses.
func InstrumentedHandler(handlerName string, next http.Handler) http.Handler {
	return promhttp.InstrumentHandlerDuration(
		requestDuration.MustCurryWith(prometheus.Labels{"handler": handlerName}),
		promhttp.InstrumentHandlerRequestSize(
			requestSize.MustCurryWith(prometheus.Labels{"handler": handlerName}),
			promhttp.InstrumentHandlerCounter(
				requestsTotal.MustCurryWith(prometheus.Labels{"handler": handlerName}),
				next,
			),
		),
	)
}
