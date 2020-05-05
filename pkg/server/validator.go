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

	"github.com/go-chi/chi/middleware"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/reader"
)

type clusterIDCtxType int

const (
	clusterIDCtx clusterIDCtxType = iota
)

// WithClusterID puts the clusterID into the given context.
func WithClusterID(ctx context.Context, clusterID string) context.Context {
	return context.WithValue(ctx, clusterIDCtx, clusterID)
}

// ClusterIDFromContext returns the clusterID from the context.
func ClusterIDFromContext(ctx context.Context) (string, bool) {
	p, ok := ctx.Value(clusterIDCtx).(string)
	return p, ok
}

// ClusterID is a HTTP middleware that extracts the cluster's ID and passes it on via context.
func ClusterID(logger log.Logger, key string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rlogger := log.With(logger, "request", middleware.GetReqID(r.Context()))

		client, ok := authorize.FromContext(r.Context())
		if !ok {
			msg := "unable to find user info"
			level.Warn(rlogger).Log("msg", msg)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}
		if len(client.Labels[key]) == 0 {
			msg := fmt.Sprintf("user data must contain a '%s' label", key)
			level.Warn(rlogger).Log("msg", msg)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}

		r = r.WithContext(WithClusterID(r.Context(), client.Labels[key]))

		next.ServeHTTP(w, r)
	}
}

// Validate the payload of a request against given and required rules.
func Validate(logger log.Logger, baseTransforms metricfamily.Transformer, maxAge time.Duration, limitBytes int64, now func() time.Time, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rlogger := log.With(logger, "request", middleware.GetReqID(r.Context()))

		client, ok := authorize.FromContext(r.Context())
		if !ok {
			msg := "unable to find user info"
			level.Warn(rlogger).Log("msg", msg)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}

		if limitBytes > 0 {
			r.Body = reader.NewLimitReadCloser(r.Body, limitBytes)
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			msg := "failed to read request body"
			if errors.Is(err, reader.ErrTooLong) {
				level.Warn(rlogger).Log("msg", msg, "err", err)
				http.Error(w, msg, http.StatusRequestEntityTooLarge)
				return
			}
			level.Warn(rlogger).Log("msg", msg, "err", err)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

		var transforms metricfamily.MultiTransformer
		transforms.With(metricfamily.NewErrorOnUnsorted(true))
		transforms.With(metricfamily.NewRequiredLabels(client.Labels))
		transforms.With(metricfamily.TransformerFunc(metricfamily.DropEmptyFamilies))

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
				msg := "failed to decode metrics"
				level.Warn(rlogger).Log("msg", msg, "err", err)
				http.Error(w, msg, http.StatusInternalServerError)
				return
			}
			families = append(families, family)
		}

		families = metricfamily.Pack(families)

		if err := metricfamily.Filter(families, transforms); err != nil {
			if errors.Is(err, metricfamily.ErrNoTimestamp) {
				level.Debug(rlogger).Log("msg", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if errors.Is(err, metricfamily.ErrUnsorted) {
				level.Debug(rlogger).Log("msg", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if errors.Is(err, metricfamily.ErrTimestampTooOld) {
				level.Debug(rlogger).Log("msg", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if errors.Is(err, metricfamily.ErrRequiredLabelMissing) {
				level.Debug(rlogger).Log("msg", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			msg := "unexpected error during metrics transforming"
			level.Warn(rlogger).Log("msg", msg, "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		buf := &bytes.Buffer{}
		encoder := expfmt.NewEncoder(buf, expfmt.ResponseFormat(r.Header))
		for _, f := range families {
			if f == nil {
				continue
			}
			if err := encoder.Encode(f); err != nil {
				msg := "failed to encode transformed metrics again"
				level.Warn(rlogger).Log("msg", msg, "err", err)
				http.Error(w, msg, http.StatusInternalServerError)
				return
			}
		}

		r.Body = ioutil.NopCloser(buf)

		next.ServeHTTP(w, r)
	}
}
