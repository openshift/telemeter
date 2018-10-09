package store

import (
	"context"

	clientmodel "github.com/prometheus/client_model/go"
)

type Store interface {
	ReadMetrics(ctx context.Context, minTimestampMs int64, fn func(partitionKey string, families []*clientmodel.MetricFamily) error) error
	WriteMetrics(ctx context.Context, partitionKey string, families []*clientmodel.MetricFamily) error
}
