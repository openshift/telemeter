package server

import (
	"context"
	"sync"

	clientmodel "github.com/prometheus/client_model/go"

	"github.com/openshift/telemeter/pkg/metricfamily"
)

type clusterMetricSlice struct {
	families []*clientmodel.MetricFamily
}

type memoryStore struct {
	lock  sync.Mutex
	store map[string]*clusterMetricSlice
}

func NewMemoryStore() Store {
	return &memoryStore{
		store: make(map[string]*clusterMetricSlice),
	}
}

func (s *memoryStore) ReadMetrics(ctx context.Context, minTimestampMs int64, fn func(partitionKey string, families []*clientmodel.MetricFamily) error) error {
	s.lock.Lock()
	store := s.store
	s.store = make(map[string]*clusterMetricSlice)
	s.lock.Unlock()

	for partitionKey, slice := range store {
		if err := fn(partitionKey, slice.families); err != nil {
			return err
		}
	}
	return nil
}

func (s *memoryStore) WriteMetrics(ctx context.Context, partitionKey string, families []*clientmodel.MetricFamily) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	m := s.store[partitionKey]
	if m == nil {
		m = &clusterMetricSlice{}
		s.store[partitionKey] = m
	}

	metricSamples.WithLabelValues("memory").Add(float64(metricfamily.MetricsCount(families)))

	m.families = families
	return nil
}
