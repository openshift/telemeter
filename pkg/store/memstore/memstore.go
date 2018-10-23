package memstore

import (
	"context"
	"sync"

	"github.com/openshift/telemeter/pkg/store"
	clientmodel "github.com/prometheus/client_model/go"
)

type clusterMetricSlice struct {
	families []*clientmodel.MetricFamily
}

type memoryStore struct {
	lock  sync.Mutex
	store map[string]*clusterMetricSlice
}

func New() *memoryStore {
	return &memoryStore{
		store: make(map[string]*clusterMetricSlice),
	}
}

func (s *memoryStore) ReadMetrics(ctx context.Context, minTimestampMs int64) ([]*store.PartitionedMetrics, error) {
	s.lock.Lock()
	oldStore := s.store
	s.store = make(map[string]*clusterMetricSlice)
	s.lock.Unlock()

	var result []*store.PartitionedMetrics

	for partitionKey, slice := range oldStore {
		result = append(result, &store.PartitionedMetrics{
			PartitionKey: partitionKey,
			Families:     slice.families,
		})
	}

	return result, nil
}

func (s *memoryStore) WriteMetrics(ctx context.Context, p *store.PartitionedMetrics) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	m := s.store[p.PartitionKey]
	if m == nil {
		m = &clusterMetricSlice{}
		s.store[p.PartitionKey] = m
	}

	m.families = p.Families
	return nil
}
