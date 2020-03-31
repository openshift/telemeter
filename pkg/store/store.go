package store

import (
	clientmodel "github.com/prometheus/client_model/go"
)

type PartitionedMetrics struct {
	PartitionKey string
	Families     []*clientmodel.MetricFamily
}
