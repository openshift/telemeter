package metricfamily

import clientmodel "github.com/prometheus/client_model/go"

type RenameMetrics struct {
	Names map[string]string
}

func (m RenameMetrics) Transform(family *clientmodel.MetricFamily) (bool, error) {
	if family == nil || len(family.Name) == 0 {
		return true, nil
	}
	if replace, ok := m.Names[family.Name]; ok {
		family.Name = replace
	}
	return true, nil
}
