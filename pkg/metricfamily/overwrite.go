package metricfamily

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	client "github.com/prometheus/client_model/go"
)

// driftRange is used to observe timestamps being older than 5min, newer than 5min,
// or within the present (+-5min)
const driftRange = 5 * time.Minute

// TimestampOverwriter describes a mechanism to manipulate timestamps of metrics.
type TimestampOverwriter struct {
	overwrittenMetrics *prometheus.CounterVec
}

func NewTimestampOverwriter(reg prometheus.Registerer) TimestampOverwriter {
	return TimestampOverwriter{
		overwrittenMetrics: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Name: "telemeter_overwritten_timestamps_total",
			Help: "Number of timestamps that were in the past, present or future",
		}, []string{"tense"}),
	}
}

// Overwrite sets all timestamps to the current time.
// We essentially already do this in Telemeter v1 by dropping all timestamps on Telemeter Servers
// and then when federating Telemeter Prometheus sets its own current timestamp.
// For v2 we want to be consistent when using remote-write and thus we overwrite the timestamps
// on Telemeter Server already to forward the same timestamps to both systems.
func (w TimestampOverwriter) Overwrite(now func() time.Time) TransformerFunc {
	return func(family *client.MetricFamily) (bool, error) {
		timestamp := now().Unix() * 1000
		for i, m := range family.Metric {
			observeDrift(w.overwrittenMetrics, now, m.GetTimestampMs())

			family.Metric[i].TimestampMs = &timestamp
		}
		return true, nil
	}
}

func observeDrift(overwrittenMetrics *prometheus.CounterVec, now func() time.Time, ms int64) {
	timestamp := time.Unix(ms/1000, 0)

	if timestamp.Before(now().Add(-driftRange)) {
		overwrittenMetrics.WithLabelValues("past").Inc()
	} else if timestamp.After(now().Add(driftRange)) {
		overwrittenMetrics.WithLabelValues("future").Inc()
	} else {
		overwrittenMetrics.WithLabelValues("present").Inc()
	}
}
