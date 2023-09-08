package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/middleware"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

// AccessLogResponseWriter wrps the responseWriter to capture the HTTP status code.
type AccessLogResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (a *AccessLogResponseWriter) WriteHeader(code int) {
	a.statusCode = code
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

			level.Info(logger).Log(
				"msg", "request log",
				"method", r.Method,
				"path", r.URL.Path,
				"status", aw.statusCode,
				"duration", time.Since(start),
				"request", middleware.GetReqID(r.Context()),
			)
		})
	}
}
