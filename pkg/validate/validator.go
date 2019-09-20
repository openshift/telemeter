package validate

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/reader"
)

// Validator validates an upload.
type Validator interface {
	Validate(ctx context.Context, req *http.Request) (string, metricfamily.Transformer, error)
}

type validator struct {
	partitionKey string
	limitBytes   int64
	maxAge       time.Duration
	nowFunc      func() time.Time
}

// New handles Prometheus metrics from end clients that must be assumed to be hostile.
// It implements metrics transforms that sanitize the incoming content.
func New(partitionKey string, limitBytes int64, maxAge time.Duration, nowFunc func() time.Time) Validator {
	return &validator{
		partitionKey: partitionKey,
		limitBytes:   limitBytes,
		maxAge:       maxAge,
		nowFunc:      nowFunc,
	}
}

// Validate implements the Validator interface. It validates an upload.
func (v *validator) Validate(ctx context.Context, req *http.Request) (string, metricfamily.Transformer, error) {
	client, ok := authorize.FromContext(ctx)
	if !ok {
		return "", nil, fmt.Errorf("unable to find user info")
	}
	if len(client.Labels[v.partitionKey]) == 0 {
		return "", nil, fmt.Errorf("user data must contain a '%s' label", v.partitionKey)
	}

	var transforms metricfamily.MultiTransformer

	if v.maxAge > 0 {
		transforms.With(metricfamily.NewErrorInvalidFederateSamples(time.Now().Add(-v.maxAge)))
	}

	transforms.With(metricfamily.NewErrorOnUnsorted(true))
	transforms.With(metricfamily.NewRequiredLabels(client.Labels))
	transforms.With(metricfamily.TransformerFunc(metricfamily.DropEmptyFamilies))
	transforms.With(metricfamily.OverwriteTimestamps(v.nowFunc))

	if v.limitBytes > 0 {
		req.Body = reader.NewLimitReadCloser(req.Body, v.limitBytes)
	}

	return client.Labels[v.partitionKey], transforms, nil
}
