package server

import (
	"io"
	"net/http"
	"strings"

	"github.com/golang/snappy"
)

// Snappy checks HTTP headers and if Content-Ecoding is snappy it decodes the request body.
func Snappy(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Content-Encoding"), "snappy") {
			r.Body = io.NopCloser(snappy.NewReader(r.Body))
		}

		next.ServeHTTP(w, r)
	}
}
