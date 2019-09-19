package metricfamily

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	client "github.com/prometheus/client_model/go"
)

// driftRange is used to observe timestamps being older than 5min, newer than 5min,
// or within the present (+-5min)
const driftRange = 5 * time.Minute

var (
	overrideMetrics = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "telemeter_override_timestamps_total",
		Help: "Number of timestamps that were in the past, present or future",
	}, []string{"tense"})
)

func init() {
	prometheus.MustRegister(overrideMetrics)
}

func OverrideTimestamps(now func() time.Time) TransformerFunc {
	return func(family *client.MetricFamily) (bool, error) {
		timestamp := now().Unix() * 1000
		for i, m := range family.Metric {
			observeDrift(now, m.GetTimestampMs())

			family.Metric[i].TimestampMs = &timestamp
		}
		return true, nil
	}
}

func observeDrift(now func() time.Time, ms int64) {
	timestamp := time.Unix(ms/1000, 0)

	if timestamp.Before(now().Add(-driftRange)) {
		overrideMetrics.WithLabelValues("past").Inc()
	} else if timestamp.After(now().Add(driftRange)) {
		overrideMetrics.WithLabelValues("future").Inc()
	} else {
		overrideMetrics.WithLabelValues("present").Inc()
	}

}
