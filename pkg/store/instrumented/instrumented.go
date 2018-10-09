package instrumented

import (
	"context"

	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/store"
	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"
)

var (
	metricSamples = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "telemeter_server_samples",
		Help: "Tracks the number of samples processed by this server.",
	}, []string{"phase"})
)

func init() {
	prometheus.MustRegister(metricSamples)
}

type instrumented struct {
	target store.Store
	gauge  prometheus.Gauge
}

func New(target store.Store, labelValues ...string) *instrumented {
	return &instrumented{
		target: target,
		gauge:  metricSamples.WithLabelValues(labelValues...),
	}
}

func (s *instrumented) ReadMetrics(ctx context.Context, minTimestampMs int64, fn func(partitionKey string, families []*clientmodel.MetricFamily) error) error {
	if s.target != nil {
		return s.target.ReadMetrics(ctx, minTimestampMs, fn)
	}
	return nil
}

func (s *instrumented) WriteMetrics(ctx context.Context, partitionKey string, families []*clientmodel.MetricFamily) error {
	if s.target != nil {
		err := s.target.WriteMetrics(ctx, partitionKey, families)
		if err != nil {
			return err
		}
	}

	s.gauge.Add(float64(metricfamily.MetricsCount(families)))

	return nil
}
