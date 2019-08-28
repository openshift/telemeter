package http

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewInstrumentedHandler(reg *prometheus.Registry, handlerName string, next http.Handler) http.Handler {
	labels := prometheus.Labels{"handler": handlerName}

	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "http_request_duration_seconds",
			Help: "Tracks the latencies for HTTP requests.",
		},
		[]string{"code", "handler", "method"},
	).MustCurryWith(labels)

	requestSize := prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "http_request_size_bytes",
			Help: "Tracks the size of HTTP requests.",
		},
		[]string{"code", "handler", "method"},
	).MustCurryWith(labels)

	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Tracks the number of HTTP requests.",
		}, []string{"code", "handler", "method"},
	).MustCurryWith(labels)

	return promhttp.InstrumentHandlerDuration(requestDuration,
		promhttp.InstrumentHandlerRequestSize(requestSize,
			promhttp.InstrumentHandlerCounter(requestsTotal,
				next,
			),
		),
	)
}
