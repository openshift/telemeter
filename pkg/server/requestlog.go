package server

import (
	"net/http"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"go.opentelemetry.io/otel/trace"
)

// AccessLogResponseWriter wrps the responseWriter to capture the HTTP status code.
type AccessLogResponseWriter struct {
	http.ResponseWriter
	StatusCode int
}

func (a *AccessLogResponseWriter) WriteHeader(code int) {
	a.StatusCode = code
	a.ResponseWriter.WriteHeader(code)
}

// RequestLogger is a middleware that logs requests.
func RequestLogger(logger log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap the responseWriter to capture the HTTP status code.
			aw := &AccessLogResponseWriter{w, http.StatusOK}

			next.ServeHTTP(aw, r)

			spanContext := trace.SpanFromContext(r.Context()).SpanContext()
			level.Info(logger).Log(
				"trace_id", spanContext.TraceID().String(),
				"span_id", spanContext.SpanID().String(),
				"msg", "request log",
				"method", r.Method,
				"path", r.URL.Path,
				"status", aw.StatusCode,
				"duration", time.Since(start),
			)
		})
	}
}
