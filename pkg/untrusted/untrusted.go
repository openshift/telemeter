package untrusted

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openshift/telemeter/pkg/authorizer"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/reader"
)

type Validator struct {
	partitionKey string
	labels       map[string]string
	limitBytes   int64
	maxAge       time.Duration
}

// NewValidator handles prometheus metrics from end clients that must be assumed to be hostile.
// It implements metrics transforms that sanitize the incoming content.
func NewValidator(partitionKey string, addLabels map[string]string, limitBytes int64, maxAge time.Duration) *Validator {
	return &Validator{
		partitionKey: partitionKey,
		labels:       addLabels,
		limitBytes:   limitBytes,
		maxAge:       maxAge,
	}
}

func (v *Validator) ValidateUpload(ctx context.Context, req *http.Request) (string, []metricfamily.Transformer, error) {
	user, ok := authorizer.FromContext(ctx)
	if !ok {
		return "", nil, fmt.Errorf("unable to find user info")
	}
	if len(user.Labels[v.partitionKey]) == 0 {
		return "", nil, fmt.Errorf("user data must contain a '%s' label", v.partitionKey)
	}

	var transforms metricfamily.AllTransformer

	if v.maxAge > 0 {
		metricfamily.NewErrorInvalidFederateSamples(time.Now().Add(-v.maxAge))
	}

	transforms = append(transforms, metricfamily.NewErrorOnUnsorted(true))

	if len(v.labels) > 0 {
		transforms = append(transforms, metricfamily.NewLabel(v.labels, nil))
	}

	transforms = append(transforms, metricfamily.NewRequiredLabels(user.Labels))

	transforms = append(transforms, metricfamily.TransformerFunc(metricfamily.DropEmptyFamilies))

	if v.limitBytes > 0 {
		req.Body = reader.NewLimitReadCloser(req.Body, v.limitBytes)
	}

	return user.Labels[v.partitionKey], []metricfamily.Transformer{transforms}, nil
}
