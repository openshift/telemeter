package validator

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/reader"
)

type Validator struct {
	partitionKey string
	labels       map[string]string
	limitBytes   int64
	maxAge       time.Duration
}

// New handles prometheus metrics from end clients that must be assumed to be hostile.
// It implements metrics transforms that sanitize the incoming content.
func New(partitionKey string, addLabels map[string]string, limitBytes int64, maxAge time.Duration) *Validator {
	return &Validator{
		partitionKey: partitionKey,
		labels:       addLabels,
		limitBytes:   limitBytes,
		maxAge:       maxAge,
	}
}

func (v *Validator) ValidateUpload(ctx context.Context, req *http.Request) (string, metricfamily.Transformer, error) {
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

	if len(v.labels) > 0 {
		transforms.With(metricfamily.NewLabel(v.labels, nil))
	}

	transforms.With(metricfamily.NewRequiredLabels(client.Labels))
	transforms.With(metricfamily.TransformerFunc(metricfamily.DropEmptyFamilies))

	if v.limitBytes > 0 {
		req.Body = reader.NewLimitReadCloser(req.Body, v.limitBytes)
	}

	return client.Labels[v.partitionKey], transforms, nil
}
