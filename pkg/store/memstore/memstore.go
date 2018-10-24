package memstore

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/openshift/telemeter/pkg/store"
	clientmodel "github.com/prometheus/client_model/go"
)

const ttl = 20 * time.Minute

type clusterMetricSlice struct {
	newest   int64
	families []*clientmodel.MetricFamily
}

type memoryStore struct {
	mu    sync.RWMutex
	store map[string]*clusterMetricSlice
}

func New() *memoryStore {
	return &memoryStore{
		store: make(map[string]*clusterMetricSlice),
	}
}

// StartCleaner starts a goroutine, executing the cleanup of stored data
// at regular intervals specified by "interval".
// The goroutine will be stopped when the given context is done.
func (s *memoryStore) StartCleaner(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)

	go func() {
		for {
			select {
			case <-ticker.C:
				s.cleanup(time.Now)
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}

func (s *memoryStore) cleanup(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for partitionKey, slice := range s.store {
		ttlTimestampMs := now().Add(-ttl).UnixNano() / int64(time.Millisecond)

		if slice.newest < ttlTimestampMs {
			delete(s.store, partitionKey)
		}
	}
}

func (s *memoryStore) ReadMetrics(ctx context.Context, minTimestampMs int64) ([]*store.PartitionedMetrics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*store.PartitionedMetrics

	for partitionKey, slice := range s.store {
		if slice.newest < minTimestampMs {
			continue
		}

		result = append(result, &store.PartitionedMetrics{
			PartitionKey: partitionKey,
			Families:     slice.families,
		})
	}

	return result, nil
}

func (s *memoryStore) WriteMetrics(ctx context.Context, p *store.PartitionedMetrics) error {
	if p == nil || len(p.Families) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.store[p.PartitionKey]

	if !ok {
		m = &clusterMetricSlice{}
		s.store[p.PartitionKey] = m
	}

	m.newest = math.MinInt64
	for i := range p.Families {
		for j := range p.Families[i].Metric {
			cur := p.Families[i].Metric[j].GetTimestampMs()
			if cur > m.newest {
				m.newest = cur
			}
		}
	}

	m.families = p.Families

	return nil
}
