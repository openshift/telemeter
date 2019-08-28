package http

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/prometheus/client_golang/prometheus"
)

type InstrumentHandler struct {
	requestDuration *prometheus.HistogramVec
	requestSize     *prometheus.SummaryVec
	requestsTotal   *prometheus.CounterVec
}

func NewInstrumentedHandler(reg *prometheus.Registry) *InstrumentHandler {
	h := InstrumentHandler{
		requestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "http_request_duration_seconds",
				Help: "Tracks the latencies for HTTP requests.",
			},
			[]string{"code", "handler", "method"},
		),

		requestSize: prometheus.NewSummaryVec(
			prometheus.SummaryOpts{
				Name: "http_request_size_bytes",
				Help: "Tracks the size of HTTP requests.",
			},
			[]string{"code", "handler", "method"},
		),

		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Tracks the number of HTTP requests.",
			}, []string{"code", "handler", "method"},
		),
	}

	reg.MustRegister(
		h.requestDuration,
		h.requestSize,
		h.requestsTotal,
	)

	return &h
}

func (i InstrumentHandler) Handle(handlerName string, next http.Handler) http.Handler {
	labels := prometheus.Labels{"handler": handlerName}

	return promhttp.InstrumentHandlerDuration(i.requestDuration.MustCurryWith(labels),
		promhttp.InstrumentHandlerRequestSize(i.requestSize.MustCurryWith(labels),
			promhttp.InstrumentHandlerCounter(i.requestsTotal.MustCurryWith(labels),
				next,
			),
		),
	)
}
