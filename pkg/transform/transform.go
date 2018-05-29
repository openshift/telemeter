package transform

import (
	"fmt"
	"sort"
	"time"

	clientmodel "github.com/prometheus/client_model/go"
)

type Interface interface {
	Transform(*clientmodel.MetricFamily) (ok bool, err error)
}

type none struct{}

var None Interface = none{}

func (_ none) Transform(family *clientmodel.MetricFamily) (bool, error) { return true, nil }

type All []Interface

func (transformers All) Transform(family *clientmodel.MetricFamily) (bool, error) {
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

// PackedFamilyWithTimestampsByName sorts a packed slice of metrics
// (no nils, all families have at least one metric, and all metrics
// have timestamps) in order of metric name and then oldest sample
type PackedFamilyWithTimestampsByName []*clientmodel.MetricFamily

func (families PackedFamilyWithTimestampsByName) Len() int {
	return len(families)
}

func (families PackedFamilyWithTimestampsByName) Less(i int, j int) bool {
	a, b := families[i].GetName(), families[j].GetName()
	if a < b {
		return true
	}
	if a > b {
		return false
	}
	tA, tB := *families[i].Metric[0].TimestampMs, *families[j].Metric[0].TimestampMs
	if tA < tB {
		return true
	}
	return false
}

func (families PackedFamilyWithTimestampsByName) Swap(i int, j int) {
	families[i], families[j] = families[j], families[i]
}

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

// Pack returns only families with metrics in the returned array, preserving the
// order of the original slice. Nil entries are removed from the slice. The returned
// slice may be empty.
func Pack(families []*clientmodel.MetricFamily) []*clientmodel.MetricFamily {
	j := len(families)
	next := 0
Found:
	for i := 0; i < j; i++ {
		if families[i] != nil && len(families[i].Metric) > 0 {
			continue
		}
		// scan for the next non-nil family
		if next <= i {
			next = i + 1
		}
		for k := next; k < j; k++ {
			if families[k] == nil || len(families[k].Metric) == 0 {
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

// MergeSortedWithTimestamps collapses metrics families with the same name into a single family,
// preserving the order of the metrics. Families must be dense (no nils for families or metrics),
// all metrics must be sorted, and all metrics must have timestamps.
func MergeSortedWithTimestamps(families []*clientmodel.MetricFamily) []*clientmodel.MetricFamily {
	var dst *clientmodel.MetricFamily
	for pos, src := range families {
		if dst == nil {
			dst = src
			continue
		}
		if dst.GetName() != src.GetName() {
			dst = nil
			continue
		}

		lenI, lenJ := len(dst.Metric), len(src.Metric)

		// if the ranges don't overlap, we can block merge
		dstBegin, dstEnd := *dst.Metric[0].TimestampMs, *dst.Metric[lenI-1].TimestampMs
		srcBegin, srcEnd := *src.Metric[0].TimestampMs, *src.Metric[lenJ-1].TimestampMs
		if dstEnd < srcBegin {
			dst.Metric = append(dst.Metric, src.Metric...)
			families[pos] = nil
			continue
		}
		if srcEnd < dstBegin {
			dst.Metric = append(src.Metric, dst.Metric...)
			families[pos] = nil
			continue
		}

		// zip merge
		i, j := 0, 0
		result := make([]*clientmodel.Metric, 0, lenI+lenJ)
	Merge:
		for {
			switch {
			case j >= lenJ:
				for ; i < lenI; i++ {
					result = append(result, dst.Metric[i])
				}
				break Merge
			case i >= lenI:
				for ; j < lenJ; j++ {
					result = append(result, src.Metric[j])
				}
				break Merge
			default:
				a, b := *dst.Metric[i].TimestampMs, *src.Metric[j].TimestampMs
				if a <= b {
					result = append(result, dst.Metric[i])
					i++
				} else {
					result = append(result, src.Metric[j])
					j++
				}
			}
		}
		dst.Metric = result
		families[pos] = nil
	}
	return Pack(families)
}

const (
	maxLabelName  = 255
	maxLabelValue = 255
	maxMetricName = 255
)

type dropInvalidFederateSamples struct {
	min int64
}

func NewDropInvalidFederateSamples(min time.Time) Interface {
	return &dropInvalidFederateSamples{
		min: min.Unix() * 1000,
	}
}

func (t *dropInvalidFederateSamples) Transform(family *clientmodel.MetricFamily) (bool, error) {
	name := family.GetName()
	if len(name) == 0 {
		return false, nil
	}
	if len(name) > 255 {
		return false, nil
	}
	if family.Type == nil {
		return false, nil
	}
	switch t := *family.Type; t {
	case clientmodel.MetricType_COUNTER:
	case clientmodel.MetricType_GAUGE:
	case clientmodel.MetricType_HISTOGRAM:
	case clientmodel.MetricType_SUMMARY:
	case clientmodel.MetricType_UNTYPED:
	default:
		return false, nil
	}

	for i, m := range family.Metric {
		if m == nil {
			continue
		}
		for j, label := range m.Label {
			if label.Name == nil || len(*label.Name) == 0 || len(*label.Name) > 255 {
				m.Label[j] = nil
			}
			if label.Value == nil || len(*label.Value) == 0 || len(*label.Value) > 255 {
				m.Label[j] = nil
			}
		}
		if m.TimestampMs == nil || *m.TimestampMs < t.min {
			family.Metric[i] = nil
			continue
		}
		switch t := *family.Type; t {
		case clientmodel.MetricType_COUNTER:
			if m.Counter == nil || m.Gauge != nil || m.Histogram != nil || m.Summary != nil || m.Untyped != nil {
				family.Metric[i] = nil
			}
		case clientmodel.MetricType_GAUGE:
			if m.Counter != nil || m.Gauge == nil || m.Histogram != nil || m.Summary != nil || m.Untyped != nil {
				family.Metric[i] = nil
			}
		case clientmodel.MetricType_HISTOGRAM:
			if m.Counter != nil || m.Gauge != nil || m.Histogram == nil || m.Summary != nil || m.Untyped != nil {
				family.Metric[i] = nil
			}
		case clientmodel.MetricType_SUMMARY:
			if m.Counter != nil || m.Gauge != nil || m.Histogram != nil || m.Summary == nil || m.Untyped != nil {
				family.Metric[i] = nil
			}
		case clientmodel.MetricType_UNTYPED:
			if m.Counter != nil || m.Gauge != nil || m.Histogram != nil || m.Summary != nil || m.Untyped == nil {
				family.Metric[i] = nil
			}
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

func (t *errorInvalidFederateSamples) Transform(family *clientmodel.MetricFamily) (bool, error) {
	name := family.GetName()
	if len(name) == 0 {
		return false, nil
	}
	if len(name) > 255 {
		return false, fmt.Errorf("metrics_name cannot be longer than 255 characters")
	}
	if family.Type == nil {
		return false, nil
	}
	switch t := *family.Type; t {
	case clientmodel.MetricType_COUNTER:
	case clientmodel.MetricType_GAUGE:
	case clientmodel.MetricType_HISTOGRAM:
	case clientmodel.MetricType_SUMMARY:
	case clientmodel.MetricType_UNTYPED:
	default:
		return false, fmt.Errorf("unknown metric type %s", t)
	}

	for _, m := range family.Metric {
		if m == nil {
			continue
		}
		for _, label := range m.Label {
			if label.Name == nil || len(*label.Name) == 0 || len(*label.Name) > 255 {
				return false, fmt.Errorf("label_name cannot be longer than 255 characters")
			}
			if label.Value == nil || len(*label.Value) == 0 || len(*label.Value) > 255 {
				return false, fmt.Errorf("label_value cannot be longer than 255 characters")
			}
		}
		if m.TimestampMs == nil {
			return false, ErrNoTimestamp
		}
		if *m.TimestampMs < t.min {
			return false, ErrTimestampTooOld
		}
		switch t := *family.Type; t {
		case clientmodel.MetricType_COUNTER:
			if m.Counter == nil || m.Gauge != nil || m.Histogram != nil || m.Summary != nil || m.Untyped != nil {
				return false, fmt.Errorf("metric type %s must have counter field set", t)
			}
		case clientmodel.MetricType_GAUGE:
			if m.Counter != nil || m.Gauge == nil || m.Histogram != nil || m.Summary != nil || m.Untyped != nil {
				return false, fmt.Errorf("metric type %s must have gauge field set", t)
			}
		case clientmodel.MetricType_HISTOGRAM:
			if m.Counter != nil || m.Gauge != nil || m.Histogram == nil || m.Summary != nil || m.Untyped != nil {
				return false, fmt.Errorf("metric type %s must have histogram field set", t)
			}
		case clientmodel.MetricType_SUMMARY:
			if m.Counter != nil || m.Gauge != nil || m.Histogram != nil || m.Summary == nil || m.Untyped != nil {
				return false, fmt.Errorf("metric type %s must have summary field set", t)
			}
		case clientmodel.MetricType_UNTYPED:
			if m.Counter != nil || m.Gauge != nil || m.Histogram != nil || m.Summary != nil || m.Untyped == nil {
				return false, fmt.Errorf("metric type %s must have untyped field set", t)
			}
		}
	}
	return true, nil
}

var DropEmptyFamilies = dropEmptyFamilies{}

type dropEmptyFamilies struct{}

func (_ dropEmptyFamilies) Transform(family *clientmodel.MetricFamily) (bool, error) {
	for _, m := range family.Metric {
		if m != nil {
			return true, nil
		}
	}
	return false, nil
}

var PackMetrics = packMetrics{}

type packMetrics struct{}

func (_ packMetrics) Transform(family *clientmodel.MetricFamily) (bool, error) {
	metrics := family.Metric
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
		// no more valid family
		family.Metric = metrics[:i]
		break
	}
	return len(family.Metric) > 0, nil
}

var SortMetrics = sortMetrics{}

type sortMetrics struct{}

func (_ sortMetrics) Transform(family *clientmodel.MetricFamily) (bool, error) {
	sort.Sort(MetricsByTimestamp(family.Metric))
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

func (o *DropUnsorted) Transform(family *clientmodel.MetricFamily) (bool, error) {
	for i, m := range family.Metric {
		if m == nil {
			continue
		}
		var ts int64
		if m.TimestampMs != nil {
			ts = *m.TimestampMs
		}
		if ts < o.timestamp {
			family.Metric[i] = nil
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

func (t *errorOnUnsorted) Transform(family *clientmodel.MetricFamily) (bool, error) {
	t.timestamp = 0
	for _, m := range family.Metric {
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

func (t *Count) Transform(family *clientmodel.MetricFamily) (bool, error) {
	t.families++
	t.metrics += len(family.Metric)
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

func (t requireLabel) Transform(family *clientmodel.MetricFamily) (bool, error) {
	for k, v := range t.labels {
	Metrics:
		for _, m := range family.Metric {
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

func (t *label) Transform(family *clientmodel.MetricFamily) (bool, error) {
	// lazily resolve the label retriever as needed
	if t.retriever != nil && len(family.Metric) > 0 {
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
	for _, m := range family.Metric {
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
