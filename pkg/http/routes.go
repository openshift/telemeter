package http

import (
	"fmt"
	"net/http"
	"net/http/pprof"

	"github.com/prometheus/client_golang/prometheus"
)

// DebugRoutes adds the debug handlers to a mux.
func DebugRoutes(mux *http.ServeMux) *http.ServeMux {
	mux.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	mux.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	mux.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	return mux
}

// HealthRoutes adds the health checks to a mux.
func HealthRoutes(mux *http.ServeMux) *http.ServeMux {
	mux.Handle("/healthz", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { fmt.Fprintln(w, "ok") }))
	mux.Handle("/healthz/ready", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { fmt.Fprintln(w, "ok") }))
	return mux
}

// MetricRoutes adds the metrics endpoint to a mux.
func MetricRoutes(mux *http.ServeMux) *http.ServeMux {
	mux.Handle("/metrics", prometheus.UninstrumentedHandler())
	return mux
}
