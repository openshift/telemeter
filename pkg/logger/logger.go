package logger

import (
	"net/http"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"go.opentelemetry.io/otel/trace"

	"github.com/openshift/telemeter/pkg/server"
)

// LogLevelFromString determines log level to string, defaults to all,
func LogLevelFromString(l string) level.Option {
	switch l {
	case "debug":
		return level.AllowDebug()
	case "info":
		return level.AllowInfo()
	case "warn":
		return level.AllowWarn()
	case "error":
		return level.AllowError()
	default:
		return level.AllowAll()
	}
}

// RequestLoggerWithTraceInfo is a middleware that logs requests with additional tracing information.
// It is based on the RequestLogger middleware from github.com/go-chi/chi/middleware.
func RequestLoggerWithTraceInfo(logger log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap the responseWriter to capture the HTTP status code.
			aw := &server.AccessLogResponseWriter{ResponseWriter: w, StatusCode: http.StatusOK}
			spanContext := trace.SpanFromContext(r.Context()).SpanContext()

			next.ServeHTTP(aw, r)

			reqLogger := logger
			if spanContext.HasTraceID() {
				reqLogger = log.WithPrefix(reqLogger, "trace_id", spanContext.TraceID().String())
			}

			if spanContext.HasSpanID() {
				reqLogger = log.WithPrefx(reqLogger, "span_id", spanContext.SpanID().String())
			}

			level.Info(reqLogger).Log(
				"msg", "request log",
				"method", r.Method,
				"path", r.URL.Path,
				"status", aw.StatusCode,
				"duration", time.Since(start),
			)
		})
	}
}
