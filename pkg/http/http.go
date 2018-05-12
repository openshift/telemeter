package http

import (
	"fmt"
	"net/http"
	"net/http/pprof"

	"github.com/prometheus/client_golang/prometheus"
)

// AddDebug adds the debug handlers to a mux.
func AddDebug(mux *http.ServeMux) *http.ServeMux {
	mux.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	mux.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	mux.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	return mux
}

// AddHealth adds the health checks to a mux.
func AddHealth(mux *http.ServeMux) *http.ServeMux {
	mux.Handle("/healthz", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { fmt.Fprintln(w, "ok") }))
	mux.Handle("/healthz/ready", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { fmt.Fprintln(w, "ok") }))
	return mux
}

// AddMetrics adds the metrics endpoint to a mux.
func AddMetrics(mux *http.ServeMux) *http.ServeMux {
	mux.Handle("/metrics", prometheus.UninstrumentedHandler())
	return mux
}

type bearerRoundTripper struct {
	token   string
	wrapper http.RoundTripper
}

func NewBearerRoundTripper(token string, rt http.RoundTripper) http.RoundTripper {
	return &bearerRoundTripper{token: token, wrapper: rt}
}

func (rt *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", rt.token))
	return rt.wrapper.RoundTrip(req)
}
