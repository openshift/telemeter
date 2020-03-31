package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/metricfamily"
)

//
//// Validator validates an upload.
//type Validator interface {
//	Validate(ctx context.Context, req *http.Request) (string, metricfamily.Transformer, error)
//}
//
//type validator struct {
//	partitionKey string
//	limitBytes   int64
//	maxAge       time.Duration
//	nowFunc      func() time.Time
//}
//
//// New handles Prometheus metrics from end clients that must be assumed to be hostile.
//// It implements metrics transforms that sanitize the incoming content.
//func New(partitionKey string, limitBytes int64, maxAge time.Duration, nowFunc func() time.Time) Validator {
//	return &validator{
//		limitBytes: limitBytes,
//		maxAge:     maxAge,
//		nowFunc:    nowFunc,
//	}
//}

//// Validate implements the Validator interface. It validates an upload.
//func (v *validator) Validate(ctx context.Context, req *http.Request) (string, metricfamily.Transformer, error) {
//	client, ok := authorize.FromContext(ctx)
//	if !ok {
//		return "", nil, fmt.Errorf("unable to find user info")
//	}
//
//	var transforms metricfamily.MultiTransformer
//
//	if v.maxAge > 0 {
//		transforms.With(metricfamily.NewErrorInvalidFederateSamples(time.Now().Add(-v.maxAge)))
//	}
//
//	transforms.With(metricfamily.NewErrorOnUnsorted(true))
//	transforms.With(metricfamily.NewRequiredLabels(client.Labels))
//	transforms.With(metricfamily.TransformerFunc(metricfamily.DropEmptyFamilies))
//	transforms.With(metricfamily.OverwriteTimestamps(v.nowFunc))
//
//	if v.limitBytes > 0 {
//		req.Body = reader.NewLimitReadCloser(req.Body, v.limitBytes)
//	}
//
//	return client.Labels[v.partitionKey], transforms, nil
//}

func PartitionKey(key string, next http.HandlerFunc) http.HandlerFunc {
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

		r = r.WithContext(WithPartition(r.Context(), client.Labels[key]))

		next.ServeHTTP(w, r)
	}
}

func WithPartition(ctx context.Context, partition string) context.Context {
	return context.WithValue(ctx, partitionCtx, partition)
}

func PartitionFromContext(ctx context.Context) (string, bool) {
	p, ok := ctx.Value(partitionCtx).(string)
	return p, ok
}

type partitionCtxType int

const (
	partitionCtx partitionCtxType = iota
)

func ByteLimit(limit int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
	}
}

func MaxAge(maxAge time.Duration, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		metricfamily.NewErrorInvalidFederateSamples(time.Now().Add(-maxAge))
		next.ServeHTTP(w, r)
	}
}
