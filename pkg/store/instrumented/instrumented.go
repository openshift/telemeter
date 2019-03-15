package instrumented

import (
	"context"

	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/store"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricSamplesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "telemeter_server_samples_total",
		Help: "Tracks the number of samples processed by this server.",
	}, []string{"phase"})
)

func init() {
	prometheus.MustRegister(metricSamplesTotal)
}

type instrumented struct {
	target  store.Store
	counter prometheus.Counter
}

func New(target store.Store, phase string) *instrumented {
	return &instrumented{
		target:  target,
		counter: metricSamplesTotal.WithLabelValues(phase),
	}
}

func (s *instrumented) ReadMetrics(ctx context.Context, minTimestampMs int64) ([]*store.PartitionedMetrics, error) {
	if s.target != nil {
		return s.target.ReadMetrics(ctx, minTimestampMs)
	}
	return nil, nil
}

func (s *instrumented) WriteMetrics(ctx context.Context, p *store.PartitionedMetrics) error {
	if s.target != nil {
		err := s.target.WriteMetrics(ctx, p)
		if err != nil {
			return err
		}
	}

	s.counter.Add(float64(metricfamily.MetricsCount(p.Families)))

	return nil
}
