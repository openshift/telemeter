package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/snappy"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

func TestSnappy(t *testing.T) {
	fooMetricName := "foo_metric"
	fooLabelName := "foo"
	fooLabelValue1 := "bar"
	value42 := 42.0
	timestamp := int64(15615582020000)

	families := []*clientmodel.MetricFamily{{
		Name: &fooMetricName,
		Metric: []*clientmodel.Metric{{
			Label: []*clientmodel.LabelPair{
				{Name: &fooLabelName, Value: &fooLabelValue1},
			},
			Counter:     &clientmodel.Counter{Value: &value42},
			TimestampMs: &timestamp,
		}},
	}}

	s := httptest.NewServer(Snappy(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		decoder := expfmt.NewDecoder(r.Body, expfmt.FmtProtoDelim)
		families := make([]*clientmodel.MetricFamily, 0, 1)
		for {
			family := &clientmodel.MetricFamily{}
			if err := decoder.Decode(family); err != nil {
				if err == io.EOF {
					break
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			families = append(families, family)
		}

		if len(families) != 1 {
			t.Errorf("expected 1 metric family got: %d", len(families))
		}
		if *families[0].Name != fooMetricName {
			t.Errorf("metric name is not as expected, is: %s", *families[0].Name)
		}
	}))

	{
		payload := bytes.Buffer{}

		compress := snappy.NewBufferedWriter(&payload)
		encoder := expfmt.NewEncoder(compress, expfmt.FmtProtoDelim)
		for _, family := range families {
			if family == nil {
				continue
			}
			if err := encoder.Encode(family); err != nil {
				t.Fatal(err)
			}
		}
		if err := compress.Flush(); err != nil {
			t.Fatal(err)
		}

		req, err := http.NewRequest(http.MethodPost, s.URL, &payload)
		if err != nil {
			t.Fatal(err)
		}

		req.Header.Set("Content-Encoding", "snappy")

		resp, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected %d and got %d", http.StatusOK, resp.StatusCode)
		}
	}
	{
		payload := bytes.Buffer{}

		encoder := expfmt.NewEncoder(&payload, expfmt.FmtProtoDelim)
		for _, family := range families {
			if family == nil {
				continue
			}
			if err := encoder.Encode(family); err != nil {
				t.Fatal(err)
			}
		}

		req, err := http.NewRequest(http.MethodPost, s.URL, &payload)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected %d and got %d", resp.StatusCode, http.StatusOK)
		}
	}
}
