package transform

import (
	"reflect"
	"testing"

	clientmodel "github.com/prometheus/client_model/go"
)

func TestPack(t *testing.T) {
	A := "A"
	B := "B"
	C := "C"
	a := &clientmodel.MetricFamily{Name: &A}
	b := &clientmodel.MetricFamily{Name: &B}
	c := &clientmodel.MetricFamily{Name: &C}

	tests := []struct {
		name string
		args []*clientmodel.MetricFamily
		want []*clientmodel.MetricFamily
	}{
		{name: "empty", args: []*clientmodel.MetricFamily{nil, nil, nil}, want: []*clientmodel.MetricFamily{}},
		{name: "begin", args: []*clientmodel.MetricFamily{nil, a, b}, want: []*clientmodel.MetricFamily{a, b}},
		{name: "middle", args: []*clientmodel.MetricFamily{a, nil, b}, want: []*clientmodel.MetricFamily{a, b}},
		{name: "end", args: []*clientmodel.MetricFamily{a, b, nil}, want: []*clientmodel.MetricFamily{a, b}},
		{name: "skip", args: []*clientmodel.MetricFamily{a, nil, b, nil, c}, want: []*clientmodel.MetricFamily{a, b, c}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Pack(tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Pack() = %v, want %v", got, tt.want)
			}
		})
	}
}
