package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/openshift/telemeter/pkg/store"
	"github.com/openshift/telemeter/pkg/store/memstore"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

func family(name string, timestamps ...int64) *clientmodel.MetricFamily {
	families := &clientmodel.MetricFamily{Name: &name}
	for i := range timestamps {
		one := float64(1)
		ts := &timestamps[i]
		if *ts < 0 {
			ts = nil
		}
		families.Metric = append(families.Metric, &clientmodel.Metric{Counter: &clientmodel.Counter{Value: &one}, TimestampMs: ts})
	}
	return families
}

func storeWithData(data map[string][]*clientmodel.MetricFamily) store.Store {
	s := memstore.New()
	for k, v := range data {
		if err := s.WriteMetrics(context.TODO(), k, v); err != nil {
			panic(err)
		}
	}
	return s
}

func TestServer_Get(t *testing.T) {
	type fields struct {
		store     store.Store
		validator UploadValidator
		nowFn     func() time.Time
	}
	tests := []struct {
		name         string
		fields       fields
		req          *http.Request
		wantCode     int
		wantFamilies []*clientmodel.MetricFamily
	}{
		{
			name: "drop expired samples",
			fields: fields{
				store: storeWithData(map[string][]*clientmodel.MetricFamily{
					"cluster-1": {
						family("test_1", 1000000, 1002000, 1004000),
						family("test_2", 1000000, 1002000, 1004000),
					},
				}),
				nowFn: func() time.Time { return time.Unix(1001+10*60, 0) },
			},
			req: &http.Request{
				Method: "GET",
			},
			wantFamilies: []*clientmodel.MetricFamily{
				family("test_1", -1, -1),
				family("test_2", -1, -1),
			},
			wantCode: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{
				store:     tt.fields.store,
				validator: tt.fields.validator,
				nowFn:     tt.fields.nowFn,
			}
			w := httptest.NewRecorder()
			s.Get(w, tt.req)
			if w.Code != tt.wantCode {
				t.Fatalf("unexpected code %d", w.Code)
			}
			families, err := read(w.Body)
			if err != nil {
				t.Fatal(err)
			}
			sort.Slice(families, func(i, j int) bool { return families[i].GetName() < families[j].GetName() })
			got, expected := familiesToText(families), familiesToText(tt.wantFamilies)
			if got != expected {
				t.Fatalf("got\n%s\nwant\n%s", got, expected)
			}
		})
	}
}

func familiesToText(families []*clientmodel.MetricFamily) string {
	buf := &bytes.Buffer{}
	for _, f := range families {
		_, _ = expfmt.MetricFamilyToText(buf, f)
	}
	return buf.String()
}

func read(r io.Reader) ([]*clientmodel.MetricFamily, error) {
	decoder := expfmt.NewDecoder(r, expfmt.FmtText)
	families := make([]*clientmodel.MetricFamily, 0, 100)
	for {
		family := &clientmodel.MetricFamily{}
		if err := decoder.Decode(family); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		families = append(families, family)
	}
	return families, nil
}
