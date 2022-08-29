package metricfamily

import (
	"fmt"
	"reflect"
	"testing"

	clientmodel "github.com/prometheus/client_model/go"
)

func familyWithLabels(name string, labels ...[]*clientmodel.LabelPair) *clientmodel.MetricFamily {
	family := &clientmodel.MetricFamily{Name: &name}
	time := int64(0)
	for i := range labels {
		family.Metric = append(family.Metric, &clientmodel.Metric{TimestampMs: &time, Label: labels[i]})
	}
	return family
}

func copyMetric(family *clientmodel.MetricFamily) *clientmodel.MetricFamily {
	metric := make([]*clientmodel.Metric, len(family.Metric))
	copy(metric, family.Metric)
	f := *family
	f.Metric = metric
	return &f
}

func setNilMetric(family *clientmodel.MetricFamily, positions ...int) *clientmodel.MetricFamily {
	f := copyMetric(family)
	for _, position := range positions {
		f.Metric[position] = nil
	}
	return f
}

func TestWhitelist(t *testing.T) {
	type checkFunc func(family *clientmodel.MetricFamily, ok bool, err error) error

	isOK := func(want bool) checkFunc {
		return func(_ *clientmodel.MetricFamily, got bool, _ error) error {
			if want != got {
				return fmt.Errorf("want ok %t, got %t", want, got)
			}
			return nil
		}
	}

	hasErr := func(want error) checkFunc {
		return func(_ *clientmodel.MetricFamily, _ bool, got error) error {
			if want != got {
				return fmt.Errorf("want err %v, got %v", want, got)
			}
			return nil
		}
	}

	deepEqual := func(want *clientmodel.MetricFamily) checkFunc {
		return func(got *clientmodel.MetricFamily, _ bool, _ error) error {
			if !reflect.DeepEqual(want, got) {
				return fmt.Errorf("want metricfamily %v, got %v", want, got)
			}
			return nil
		}
	}

	strPnt := func(str string) *string {
		return &str
	}

	a := familyWithLabels("A", []*clientmodel.LabelPair{
		{
			Name:  strPnt("method"),
			Value: strPnt("POST"),
		},
	})

	b := familyWithLabels("B", []*clientmodel.LabelPair{
		{
			Name:  strPnt("method"),
			Value: strPnt("GET"),
		},
	})

	c := familyWithLabels("C",
		[]*clientmodel.LabelPair{
			{
				Name:  strPnt("method"),
				Value: strPnt("POST"),
			},
			{
				Name:  strPnt("status"),
				Value: strPnt("200"),
			},
		},
		[]*clientmodel.LabelPair{
			{
				Name:  strPnt("method"),
				Value: strPnt("GET"),
			},
			{
				Name:  strPnt("status"),
				Value: strPnt("200"),
			},
		},
		[]*clientmodel.LabelPair{
			{
				Name:  strPnt("method"),
				Value: strPnt("POST"),
			},
			{
				Name:  strPnt("status"),
				Value: strPnt("500"),
			},
		},
		[]*clientmodel.LabelPair{
			{
				Name:  strPnt("method"),
				Value: strPnt("DELETE"),
			},
			{
				Name:  strPnt("status"),
				Value: strPnt("200"),
			},
		},
	)

	for _, tc := range []struct {
		name        string
		checks      []checkFunc
		family      *clientmodel.MetricFamily
		whitelister Transformer
	}{
		{
			name:        "accept A",
			family:      a,
			checks:      []checkFunc{isOK(true), hasErr(nil), deepEqual(a)},
			whitelister: mustMakeWhitelist(t, []string{"{__name__=\"A\"}"}),
		},
		{
			name:        "reject B",
			family:      b,
			checks:      []checkFunc{isOK(false), hasErr(nil), deepEqual(setNilMetric(b, 0))},
			whitelister: mustMakeWhitelist(t, []string{"{__name__=\"A\"}"}),
		},
		{
			name:        "accept C",
			family:      c,
			checks:      []checkFunc{isOK(true), hasErr(nil), deepEqual(c)},
			whitelister: mustMakeWhitelist(t, []string{"{__name__=\"C\"}"}),
		},
		{
			name:        "reject C",
			family:      c,
			checks:      []checkFunc{isOK(false), hasErr(nil), deepEqual(setNilMetric(c, 0, 1, 2, 3))},
			whitelister: mustMakeWhitelist(t, []string{"{method=\"PUT\"}"}),
		},
		{
			name:        "reject parts of C",
			family:      c,
			checks:      []checkFunc{isOK(true), hasErr(nil), deepEqual(setNilMetric(c, 0, 2, 3))},
			whitelister: mustMakeWhitelist(t, []string{"{__name__=\"C\",method=\"GET\"}"}),
		},
		{
			name:        "reject different parts of C",
			family:      c,
			checks:      []checkFunc{isOK(true), hasErr(nil), deepEqual(setNilMetric(c, 2))},
			whitelister: mustMakeWhitelist(t, []string{"{status=\"200\"}"}),
		},
		{
			name:        "multiple rules",
			family:      c,
			checks:      []checkFunc{isOK(true), hasErr(nil), deepEqual(setNilMetric(c, 0, 3))},
			whitelister: mustMakeWhitelist(t, []string{"{method=\"GET\"}", "{status=\"500\"}"}),
		},
		{
			name:        "multiple rules complex",
			family:      c,
			checks:      []checkFunc{isOK(true), hasErr(nil), deepEqual(setNilMetric(c, 0, 1, 3))},
			whitelister: mustMakeWhitelist(t, []string{"{method=\"GET\",status=\"400\"}", "{status=\"500\"}"}),
		},
		{
			name:        "multiple rules complex with rejection",
			family:      c,
			checks:      []checkFunc{isOK(true), hasErr(nil), deepEqual(setNilMetric(c, 1, 2))},
			whitelister: mustMakeWhitelist(t, []string{"{method=\"POST\",status=\"200\"}", "{method=\"DELETE\"}"}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := copyMetric(tc.family)
			ok, err := tc.whitelister.Transform(f)
			for _, check := range tc.checks {
				if err := check(f, ok, err); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func mustMakeWhitelist(t *testing.T, rules []string) Transformer {
	w, err := NewWhitelist(rules)
	if err != nil {
		t.Fatalf("failed to create new whitelist transformer: %v", err)
	}
	return w
}
