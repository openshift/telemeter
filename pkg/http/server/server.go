package server

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/golang/snappy"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/store"
	"github.com/openshift/telemeter/pkg/store/ratelimited"
	"github.com/openshift/telemeter/pkg/validate"
)

func Post(logger log.Logger, store store.Store, validator validate.Validator, transformer metricfamily.Transformer) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		defer req.Body.Close()

		// TODO: Make middleware
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		defer cancel()

		partitionKey, transforms, err := validator.Validate(ctx, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var t metricfamily.MultiTransformer
		t.With(transforms)

		// read the response into memory
		format := expfmt.ResponseFormat(req.Header)
		var r io.Reader = req.Body
		if req.Header.Get("Content-Encoding") == "snappy" {
			r = snappy.NewReader(r)
		}
		decoder := expfmt.NewDecoder(r, format)

		errCh := make(chan error)
		go func() { errCh <- decodeAndStoreMetrics(ctx, store, partitionKey, decoder, t) }()

		select {
		case <-ctx.Done():
			http.Error(w, "Timeout while storing metrics", http.StatusInternalServerError)
			level.Error(logger).Log("msg", "timeout processing incoming request")
			return
		case err := <-errCh:
			switch err {
			case nil:
				break
			case ratelimited.ErrWriteLimitReached(partitionKey):
				http.Error(w, err.Error(), http.StatusTooManyRequests)
			default:
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
	}
}

func decodeAndStoreMetrics(ctx context.Context, s store.Store, partitionKey string, decoder expfmt.Decoder, transformer metricfamily.Transformer) error {
	families := make([]*clientmodel.MetricFamily, 0, 100)
	for {
		family := &clientmodel.MetricFamily{}
		families = append(families, family)
		if err := decoder.Decode(family); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}

	if err := metricfamily.Filter(families, transformer); err != nil {
		return err
	}
	families = metricfamily.Pack(families)

	return s.WriteMetrics(ctx, &store.PartitionedMetrics{
		PartitionKey: partitionKey,
		Families:     families,
	})
}

func PostMethod(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	}
}

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
