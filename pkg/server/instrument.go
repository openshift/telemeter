package server

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:                            "http_request_duration_seconds",
			Help:                            "Tracks the latencies for HTTP requests.",
			NativeHistogramBucketFactor:     1.1,
			NativeHistogramMaxBucketNumber:  100,
			NativeHistogramMinResetDuration: 1 * time.Hour,
			//TODO(simonpasquier): remove legacy histogram support once we've
			//switched all consumers to use native histograms.
			Buckets: prometheus.DefBuckets,
		},
		[]string{"code", "handler", "method"},
	)

	requestSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:                            "http_request_size_bytes",
			Help:                            "Tracks the size of HTTP requests.",
			NativeHistogramBucketFactor:     1.1,
			NativeHistogramMaxBucketNumber:  100,
			NativeHistogramMinResetDuration: 1 * time.Hour,
			//TODO(simonpasquier): remove legacy histogram support once we've
			//switched all consumers to use native histograms.
			Buckets: []float64{1024, 8192, 65536, 262144, 524288, 1048576, 2097152},
		},
		[]string{"code", "handler", "method"},
	)

	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Tracks the number of HTTP requests.",
		}, []string{"code", "handler", "method"},
	)
)

func init() {
	prometheus.MustRegister(requestDuration, requestSize, requestsTotal)
}

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
