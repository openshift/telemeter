package store

import (
	"context"

	clientmodel "github.com/prometheus/client_model/go"
)

type PartitionedMetrics struct {
	PartitionKey string
	Families     []*clientmodel.MetricFamily
}

type Store interface {
	ReadMetrics(ctx context.Context, minTimestampMs int64) ([]*PartitionedMetrics, error)
	WriteMetrics(context.Context, *PartitionedMetrics) error
}
