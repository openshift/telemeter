package metricfamily

import (
	prom "github.com/prometheus/client_model/go"
)

type elide struct {
	labelSet map[string]struct{}
}

// NewElide creates a new elide transformer for the given metrics.
func NewElide(labels ...string) *elide {
	labelSet := make(map[string]struct{})
	for i := range labels {
		labelSet[labels[i]] = struct{}{}
	}

	return &elide{labelSet}
}

// Transform filters label pairs in the given metrics family,
// eliding labels.
func (t *elide) Transform(family *prom.MetricFamily) (bool, error) {
	if family == nil || len(family.Metric) == 0 {
		return true, nil
	}
	var filteredMetrics []*prom.Metric
	for i := range family.Metric {
		if family.Metric[i] == nil {
			elideSkippedMetrics.Inc()
			continue
		}

		var filtered []*prom.LabelPair
		for j := range family.Metric[i].Label {
			if _, elide := t.labelSet[family.Metric[i].Label[j].GetName()]; elide {
				continue
			}
			filtered = append(filtered, family.Metric[i].Label[j])
		}
		family.Metric[i].Label = filtered
		filteredMetrics = append(filteredMetrics, family.Metric[i])
	}
	family.Metric = filteredMetrics
	return true, nil
}
