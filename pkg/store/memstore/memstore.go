package memstore

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"

	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/store"
)

type clusterMetricSlice struct {
	newest   int64
	families []*clientmodel.MetricFamily
}

type memoryStore struct {
	ttl   time.Duration
	mu    sync.RWMutex
	store map[string]*clusterMetricSlice

	families      *prometheus.GaugeVec
	partitions    prometheus.Gauge
	cleanupsTotal prometheus.Counter
	samplesTotal  prometheus.Counter
}

func New(reg *prometheus.Registry, ttl time.Duration) *memoryStore {
	ms := &memoryStore{
		ttl:   ttl,
		store: make(map[string]*clusterMetricSlice),

		families: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "telemeter_families",
			Help: "Tracks the current amount of families for a given partition.",
		}, []string{"partition"}),
		partitions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "telemeter_partitions",
			Help: "Tracks the current amount of stored partitions.",
		}),
		cleanupsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "telemeter_cleanups_total",
			Help: "Tracks the total amount of cleanups.",
		}),
		samplesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "telemeter_samples_total",
			Help: "Tracks the number of samples processed by this server.",
		}),
	}

	reg.MustRegister(
		ms.families,
		ms.partitions,
		ms.cleanupsTotal,
		ms.samplesTotal,
	)

	ms.cleanupsTotal.Add(0)
	ms.samplesTotal.Add(0)

	return ms
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
				s.cleanup(time.Now())
			case <-ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
}

func (s *memoryStore) cleanup(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for partitionKey, slice := range s.store {
		ttlTimestampMs := now.Add(-s.ttl).UnixNano() / int64(time.Millisecond)

		if slice.newest < ttlTimestampMs {
			s.families.WithLabelValues(partitionKey).Set(0)
			delete(s.store, partitionKey)
		}
	}

	s.cleanupsTotal.Inc()
	s.partitions.Set(float64(len(s.store)))
}

func (s *memoryStore) ReadMetrics(ctx context.Context, minTimestampMs int64) ([]*store.PartitionedMetrics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*store.PartitionedMetrics, 0, len(s.store))

	for partitionKey, slice := range s.store {
		if slice.newest < minTimestampMs {
			continue
		}

		families := make([]*clientmodel.MetricFamily, 0, len(slice.families))

		for i := range slice.families {
			families = append(families, proto.Clone(slice.families[i]).(*clientmodel.MetricFamily))
		}

		result = append(result, &store.PartitionedMetrics{
			PartitionKey: partitionKey,
			Families:     families,
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

	s.partitions.Set(float64(len(s.store)))
	s.families.WithLabelValues(p.PartitionKey).Set(float64(len(p.Families)))
	s.samplesTotal.Add(float64(metricfamily.MetricsCount(p.Families)))

	return nil
}
