package server

import (
	"context"
	"sync"

	clientmodel "github.com/prometheus/client_model/go"
)

type clusterMetricSlice struct {
	newest   int64
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

func (s *memoryStore) ReadMetrics(ctx context.Context, fn func(partitionKey string, mf []*clientmodel.MetricFamily) error) error {
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

func (s *memoryStore) WriteMetrics(ctx context.Context, partitionKey string, mf []*clientmodel.MetricFamily) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	m := s.store[partitionKey]
	if m == nil {
		m = &clusterMetricSlice{}
		s.store[partitionKey] = m
	}

	// TODO: this should probably be a transformer
	previous := m.newest - 5000
	next := previous
	for _, family := range mf {
		for j, metric := range family.Metric {
			if metric.TimestampMs == nil {
				continue
			}
			t := *metric.TimestampMs
			if t < previous {
				family.Metric[j] = nil
				continue
			}
			if t > next {
				next = t
			}
		}
	}

	m.families = append(m.families, mf...)
	return nil
}
