package prom

import "github.com/prometheus/client_golang/prometheus"

// WrapRegistererWith is like prometheus.WrapRegistererWith but it passes nil straight through
// which allows nil check.
func WrapRegistererWith(labels prometheus.Labels, reg prometheus.Registerer) prometheus.Registerer {
	if reg == nil {
		return nil
	}
	return prometheus.WrapRegistererWith(labels, reg)
}
