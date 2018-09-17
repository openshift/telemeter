package metricfamily

import clientmodel "github.com/prometheus/client_model/go"

type AllTransformer []Transformer

func (transformers AllTransformer) Transform(family *clientmodel.MetricFamily) (bool, error) {
	for _, t := range transformers {
		ok, err := t.Transform(family)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}
