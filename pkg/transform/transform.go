package transform

import (
	"fmt"
	"sort"
	"time"

	clientmodel "github.com/prometheus/client_model/go"
)

// Metrics returns the number of unique metrics in the given families. It skips
// nil families but does not skip nil metrics.
func Metrics(families []*clientmodel.MetricFamily) int {
	count := 0
	for _, family := range families {
		if family == nil {
			continue
		}
		count += len(family.Metric)
	}
	return count
}

func Filter(families []*clientmodel.MetricFamily, filter Interface) error {
	for i, family := range families {
		ok, err := filter.Transform(family)
		if err != nil {
			return err
		}
		if !ok {
			families[i] = nil
		}
	}
	return nil
}

func Pack(families []*clientmodel.MetricFamily) []*clientmodel.MetricFamily {
	j := len(families)
	next := 0
Found:
	for i := 0; i < j; i++ {
		if families[i] != nil {
			continue
		}
		// scan for the next non-nil family
		if next <= i {
			next = i + 1
		}
		for k := next; k < j; k++ {
			if families[k] == nil {
				continue
			}
			// fill the current i with a non-nil family
			families[i], families[k] = families[k], nil
			next = k + 1
			continue Found
		}
		// no more valid families
		return families[:i]
	}
	return families
}

type Interface interface {
	Transform(*clientmodel.MetricFamily) (ok bool, err error)
}

type none struct{}

var None Interface = none{}

func (_ none) Transform(mf *clientmodel.MetricFamily) (bool, error) { return true, nil }

type All []Interface

func (transformers All) Transform(mf *clientmodel.MetricFamily) (bool, error) {
	for _, t := range transformers {
		ok, err := t.Transform(mf)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

type dropInvalidFederateSamples struct {
	min int64
}

func NewDropInvalidFederateSamples(min time.Time) Interface {
	return &dropInvalidFederateSamples{
		min: min.Unix() * 1000,
	}
}

func (t *dropInvalidFederateSamples) Transform(mf *clientmodel.MetricFamily) (bool, error) {
	if len(mf.GetName()) == 0 {
		return false, nil
	}
	if mf.Type == nil {
		return false, nil
	}
	for i, m := range mf.Metric {
		if m == nil {
			continue
		}
		if m.TimestampMs == nil || *m.TimestampMs < t.min {
			mf.Metric[i] = nil
			continue
		}
	}
	return true, nil
}

type errorInvalidFederateSamples struct {
	min int64
}

func NewErrorInvalidFederateSamples(min time.Time) Interface {
	return &errorInvalidFederateSamples{
		min: min.Unix() * 1000,
	}
}

func (t *errorInvalidFederateSamples) Transform(mf *clientmodel.MetricFamily) (bool, error) {
	if len(mf.GetName()) == 0 {
		return false, nil
	}
	if mf.Type == nil {
		return false, nil
	}
	for _, m := range mf.Metric {
		if m == nil {
			continue
		}
		if m.TimestampMs == nil {
			return false, ErrNoTimestamp
		}
		if *m.TimestampMs < t.min {
			return false, ErrTimestampTooOld
		}
	}
	return true, nil
}

var DropEmptyFamilies = dropEmptyFamilies{}

type dropEmptyFamilies struct{}

func (_ dropEmptyFamilies) Transform(mf *clientmodel.MetricFamily) (bool, error) {
	for _, m := range mf.Metric {
		if m != nil {
			return true, nil
		}
	}
	return false, nil
}

var PackMetrics = packMetrics{}

type packMetrics struct{}

func (_ packMetrics) Transform(mf *clientmodel.MetricFamily) (bool, error) {
	metrics := mf.Metric
	j := len(metrics)
	next := 0
Found:
	for i := 0; i < j; i++ {
		if metrics[i] != nil {
			continue
		}
		// scan for the next non-nil family
		if next <= i {
			next = i + 1
		}
		for k := next; k < j; k++ {
			if metrics[k] == nil {
				continue
			}
			// fill the current i with a non-nil family
			metrics[i], metrics[k] = metrics[k], nil
			next = k + 1
			continue Found
		}
		// no more valid families
		mf.Metric = metrics[:i]
		break
	}
	return len(mf.Metric) > 0, nil
}

var SortMetrics = sortMetrics{}

type sortMetrics struct{}

func (_ sortMetrics) Transform(mf *clientmodel.MetricFamily) (bool, error) {
	sort.Sort(MetricsByTimestamp(mf.Metric))
	return true, nil
}

type MetricsByTimestamp []*clientmodel.Metric

func (m MetricsByTimestamp) Len() int {
	return len(m)
}

func (m MetricsByTimestamp) Less(i int, j int) bool {
	a, b := m[i], m[j]
	if a == nil {
		return b != nil
	}
	if b == nil {
		return false
	}
	if a.TimestampMs == nil {
		return b.TimestampMs != nil
	}
	if b.TimestampMs == nil {
		return false
	}
	return *a.TimestampMs < *b.TimestampMs
}

func (m MetricsByTimestamp) Swap(i int, j int) {
	m[i], m[j] = m[j], m[i]
}

type DropUnsorted struct {
	timestamp int64
}

func (o *DropUnsorted) Transform(mf *clientmodel.MetricFamily) (bool, error) {
	for i, m := range mf.Metric {
		if m == nil {
			continue
		}
		var ts int64
		if m.TimestampMs != nil {
			ts = *m.TimestampMs
		}
		if ts < o.timestamp {
			mf.Metric[i] = nil
			continue
		}
		o.timestamp = ts
	}
	o.timestamp = 0
	return true, nil
}

var (
	ErrUnsorted        = fmt.Errorf("metrics in provided family are not in increasing timestamp order")
	ErrNoTimestamp     = fmt.Errorf("metrics in provided family do not have a timestamp")
	ErrTimestampTooOld = fmt.Errorf("metrics in provided family have a timestamp that is too old, check clock skew")
)

type errorOnUnsorted struct {
	timestamp int64
	require   bool
}

func NewErrorOnUnsorted(requireTimestamp bool) Interface {
	return &errorOnUnsorted{
		require: requireTimestamp,
	}
}

func (t *errorOnUnsorted) Transform(mf *clientmodel.MetricFamily) (bool, error) {
	t.timestamp = 0
	for _, m := range mf.Metric {
		if m == nil {
			continue
		}
		var ts int64
		if m.TimestampMs != nil {
			ts = *m.TimestampMs
		} else if t.require {
			return false, ErrNoTimestamp
		}
		if ts < t.timestamp {
			return false, ErrUnsorted
		}
		t.timestamp = ts
	}
	return true, nil
}

type Count struct {
	families int
	metrics  int
}

func (t *Count) Metrics() int { return t.metrics }

func (t *Count) Transform(mf *clientmodel.MetricFamily) (bool, error) {
	t.families++
	t.metrics += len(mf.Metric)
	return true, nil
}

type requireLabel struct {
	labels map[string]string
}

func NewRequiredLabels(labels map[string]string) Interface {
	return requireLabel{labels: labels}
}

var (
	ErrRequiredLabelValueMismatch = fmt.Errorf("a label value does not match the required value")
	ErrRequiredLabelMissing       = fmt.Errorf("a required label is missing from the metric")
)

func (t requireLabel) Transform(mf *clientmodel.MetricFamily) (bool, error) {
	for k, v := range t.labels {
	Metrics:
		for _, m := range mf.Metric {
			if m == nil {
				continue
			}
			for _, label := range m.Label {
				if label == nil {
					continue
				}
				if label.GetName() == k {
					if label.GetValue() != v {
						return false, fmt.Errorf("expected label %s to have value %s instead of %s", label.GetName(), v, label.GetValue())
					}
					continue Metrics
				}
			}
			return false, ErrRequiredLabelMissing
		}
	}
	return true, nil
}

type LabelRetriever interface {
	Labels() (map[string]string, error)
}

type label struct {
	labels    map[string]*clientmodel.LabelPair
	retriever LabelRetriever
}

func NewLabel(labels map[string]string, retriever LabelRetriever) Interface {
	pairs := make(map[string]*clientmodel.LabelPair)
	for k, v := range labels {
		name, value := k, v
		pairs[k] = &clientmodel.LabelPair{Name: &name, Value: &value}
	}
	return &label{
		labels:    pairs,
		retriever: retriever,
	}
}

func (t *label) Transform(mf *clientmodel.MetricFamily) (bool, error) {
	// lazily resolve the label retriever as needed
	if t.retriever != nil && len(mf.Metric) > 0 {
		added, err := t.retriever.Labels()
		if err != nil {
			return false, err
		}
		t.retriever = nil
		for k, v := range added {
			name, value := k, v
			t.labels[k] = &clientmodel.LabelPair{Name: &name, Value: &value}
		}
	}
	for _, m := range mf.Metric {
		m.Label = appendLabels(m.Label, t.labels)
	}
	return true, nil
}

func appendLabels(existing []*clientmodel.LabelPair, overrides map[string]*clientmodel.LabelPair) []*clientmodel.LabelPair {
	var found []string
	for i, pair := range existing {
		name := pair.GetName()
		if value, ok := overrides[name]; ok {
			existing[i] = value
			found = append(found, name)
		}
	}
	for k, v := range overrides {
		if !contains(found, k) {
			existing = append(existing, v)
		}
	}
	return existing
}

func contains(values []string, s string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
