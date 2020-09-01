package http

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/prometheus/client_golang/prometheus"
)

// InstrumentedRoundTripper holds necessary metrics to instrument an http.RoundTripper.
type InstrumentedRoundTripper interface {
	NewRoundTripper(name string, rt http.RoundTripper) http.RoundTripper
}

type idleConnectionCloser interface {
	CloseIdleConnections()
}

type transportWithIdleConnectionCloser struct {
	idleConnectionCloser
	http.RoundTripper
}

type defaultInstrumentedRoundTripperProvider struct {
	inFlightGauge *prometheus.GaugeVec
	counter       *prometheus.CounterVec
	dnsLatencyVec *prometheus.HistogramVec
	tlsLatencyVec *prometheus.HistogramVec
	histVec       *prometheus.HistogramVec
}

func NewInstrumentedRoundTripperProvider(reg prometheus.Registerer) InstrumentedRoundTripper {
	ins := defaultInstrumentedRoundTripperProvider{
			inFlightGauge: promauto.With(reg).NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "client_in_flight_requests",
					Help: "A gauge of in-flight requests for the wrapped client.",
				}, []string{"client"},
			),
			counter: promauto.With(reg).NewCounterVec(
				prometheus.CounterOpts{
					Name: "client_api_requests_total",
					Help: "A counter for requests from the wrapped client.",
				}, []string{"code", "method", "client"},
			),
			dnsLatencyVec: promauto.With(reg).NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "dns_duration_seconds",
					Help:    "Trace dns latency histogram.",
					Buckets: []float64{.005, .01, .025, .05},
				}, []string{"event", "client"},
			),
			tlsLatencyVec: promauto.With(reg).NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "tls_duration_seconds",
					Help:    "Trace tls latency histogram.",
					Buckets: []float64{.05, .1, .25, .5},
				}, []string{"event", "client"},
			),
			histVec: promauto.With(reg).NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "request_duration_seconds",
					Help:    "A histogram of request latencies.",
					Buckets: prometheus.DefBuckets,
				}, []string{"method", "client"},
			),
	}

	return &ins
}

func (p *defaultInstrumentedRoundTripperProvider) NewRoundTripper(clientName string, next http.RoundTripper) http.RoundTripper {
	trace := &promhttp.InstrumentTrace{
		DNSStart: func(t float64) {
			p.dnsLatencyVec.
				WithLabelValues("dns_stap", clientName).
				Observe(t)
		},
		DNSDone: func(t float64) {
			p.dnsLatencyVec.
				WithLabelValues("dns_done", clientName).
				Observe(t)
		},
		TLSHandshakeStart: func(t float64) {
			p.tlsLatencyVec.
				WithLabelValues("tls_handshake_stap", clientName).
				Observe(t)
		},
		TLSHandshakeDone: func(t float64) {
			p.tlsLatencyVec.
				WithLabelValues("tls_handshake_done", clientName).
				Observe(t)
		},
	}

	rt := promhttp.InstrumentRoundTripperInFlight(p.inFlightGauge.WithLabelValues(clientName),
		promhttp.InstrumentRoundTripperCounter(p.counter.MustCurryWith(prometheus.Labels{"client": clientName}),
			promhttp.InstrumentRoundTripperTrace(trace,
				promhttp.InstrumentRoundTripperDuration(p.histVec.MustCurryWith(prometheus.Labels{"client": clientName}),
					next),
			),
		),
	)

	// promhttp does not pass idle connection closer properly, so let's do it on our own.
	// TODO(bwplotka): Improve promhttp upstream
	if ic, ok := next.(idleConnectionCloser); ok {
		return &transportWithIdleConnectionCloser{
			idleConnectionCloser: ic,
			RoundTripper:         rt,
		}
	}
	return rt
}
