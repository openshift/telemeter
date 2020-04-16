package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"time"

	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/reader"
)

// ClusterID is a HTTP middleware that extracts the cluster's ID and passes it on via context.
func ClusterID(key string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client, ok := authorize.FromContext(r.Context())
		if !ok {
			http.Error(w, "unable to find user info", http.StatusInternalServerError)
			return
		}
		if len(client.Labels[key]) == 0 {
			http.Error(w, fmt.Sprintf("user data must contain a '%s' label", key), http.StatusInternalServerError)
			return
		}

		r = r.WithContext(WithClusterID(r.Context(), client.Labels[key]))

		next.ServeHTTP(w, r)
	}
}

// WithClusterID puts the clusterID into the given context.
func WithClusterID(ctx context.Context, clusterID string) context.Context {
	return context.WithValue(ctx, clusterIDCtx, clusterID)
}

// ClusterIDFromContext returns the clusterID from the context.
func ClusterIDFromContext(ctx context.Context) (string, bool) {
	p, ok := ctx.Value(clusterIDCtx).(string)
	return p, ok
}

type clusterIDCtxType int

const (
	clusterIDCtx clusterIDCtxType = iota
)

// Validate the payload of a request against given and required rules.
func Validate(baseTransforms metricfamily.Transformer, maxAge time.Duration, limitBytes int64, now func() time.Time, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client, ok := authorize.FromContext(r.Context())
		if !ok {
			http.Error(w, "unable to find user info", http.StatusInternalServerError)
			return
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

		var transforms metricfamily.MultiTransformer
		transforms.With(metricfamily.NewErrorOnUnsorted(true))
		transforms.With(metricfamily.NewRequiredLabels(client.Labels))
		transforms.With(metricfamily.TransformerFunc(metricfamily.DropEmptyFamilies))

		if limitBytes > 0 {
			r.Body = reader.NewLimitReadCloser(r.Body, limitBytes)
		}

		// these transformers need to be created for every request
		if maxAge > 0 {
			transforms.With(metricfamily.NewErrorInvalidFederateSamples(now().Add(-maxAge)))
		}

		transforms.With(metricfamily.OverwriteTimestamps(now))
		transforms.With(baseTransforms)

		decoder := expfmt.NewDecoder(bytes.NewBuffer(body), expfmt.ResponseFormat(r.Header))

		families := make([]*clientmodel.MetricFamily, 0, 100)
		for {
			family := &clientmodel.MetricFamily{}
			if err := decoder.Decode(family); err != nil {
				if err == io.EOF {
					break
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			families = append(families, family)
		}

		families = metricfamily.Pack(families)

		if err := metricfamily.Filter(families, transforms); err != nil {
			if errors.Is(err, metricfamily.ErrNoTimestamp) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if errors.Is(err, metricfamily.ErrUnsorted) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if errors.Is(err, metricfamily.ErrTimestampTooOld) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if errors.Is(err, metricfamily.ErrRequiredLabelMissing) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		buf := &bytes.Buffer{}
		encoder := expfmt.NewEncoder(buf, expfmt.ResponseFormat(r.Header))
		for _, f := range families {
			if err := encoder.Encode(f); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return

			}
		}

		r.Body = ioutil.NopCloser(buf)

		next.ServeHTTP(w, r)
	}
}
