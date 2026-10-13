package server

import (
	clientmodel "github.com/prometheus/client_model/go"
)

type PartitionedMetrics struct {
	ClusterID string
	Families  []*clientmodel.MetricFamily
}
