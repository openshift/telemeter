package server

import (
	"bytes"
	"io/ioutil"
	"net/http"

	"github.com/golang/snappy"
)

func Snappy(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reader := r.Body

		if r.Header.Get("Content-Encoding") == "snappy" {
			body, err := ioutil.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			defer r.Body.Close()

			payload, _ := snappy.Decode(nil, body)
			reader = ioutil.NopCloser(bytes.NewBuffer(payload))
		}

		r.Body = reader

		next.ServeHTTP(w, r)
	}
}
