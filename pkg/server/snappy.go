package server

import (
	"bytes"
	"io/ioutil"
	"net/http"

	"github.com/golang/snappy"
)

// Snappy checks HTTP headers and if Content-Ecoding is snappy it decodes the request body.
func Snappy(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") == "snappy" {
			body, err := ioutil.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer r.Body.Close()

			payload, err := snappy.Decode(nil, body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			reader := ioutil.NopCloser(bytes.NewBuffer(payload))
			r.Body = reader
		}

		next.ServeHTTP(w, r)
	}
}
