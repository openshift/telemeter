package server

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-kit/kit/log"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/metricfamily"
)

func TestValidate(t *testing.T) {
	fooMetricName := "foo_metric"
	fooLabelName := "foo"
	fooLabelValue1 := "bar"
	clientIDLabelName := "_id"
	clientIDLabelValue := "test"
	value42 := 42.0
	timestamp := int64(15615582020000)
	timestampNewer := int64(15615582020000 + 10000)
	timestampTooOld := int64(1234)

	fakeAuthorizeHandler := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(authorize.WithClient(r.Context(), &authorize.Client{
				Labels: map[string]string{"_id": "test"},
			}))
			next.ServeHTTP(w, r)
		}
	}

	now := func() time.Time { return time.Date(2020, 04, 20, 20, 20, 20, 0, time.UTC) }

	s := httptest.NewServer(
		fakeAuthorizeHandler(
			Validate(log.NewNopLogger(), metricfamily.MultiTransformer{}, time.Hour, 512*1024, now,
				func(w http.ResponseWriter, r *http.Request) {
					// TODO: Make the check proper to changing timestamps?
					body, err := ioutil.ReadAll(r.Body)
					if err != nil {
						t.Fatalf("failed to read body: %v", err)
					}

					expectBody := "# TYPE foo_metric counter\nfoo_metric{_id=\"test\",foo=\"bar\"} 42 1587414020000\n"

					if strings.TrimSpace(string(body)) != strings.TrimSpace(expectBody) {
						t.Errorf("expected '%s', got: %s", expectBody, string(body))
					}
				},
			),
		),
	)
	defer s.Close()

	testcases := []struct {
		name         string
		families     []*clientmodel.MetricFamily
		expectStatus int
		expectBody   string
	}{{
		name: "valid",
		families: []*clientmodel.MetricFamily{{
			Name: &fooMetricName,
			Metric: []*clientmodel.Metric{{
				Label: []*clientmodel.LabelPair{
					{Name: &clientIDLabelName, Value: &clientIDLabelValue},
					{Name: &fooLabelName, Value: &fooLabelValue1},
				},
				Counter:     &clientmodel.Counter{Value: &value42},
				TimestampMs: &timestamp,
			}},
		}},
		expectStatus: http.StatusOK,
		expectBody:   "",
	}, {
		name: "noTimestamp",
		families: []*clientmodel.MetricFamily{{
			Name: &fooMetricName,
			Metric: []*clientmodel.Metric{{
				Label: []*clientmodel.LabelPair{
					{Name: &clientIDLabelName, Value: &clientIDLabelValue},
					{Name: &fooLabelName, Value: &fooLabelValue1},
				},
				Counter: &clientmodel.Counter{Value: &value42},
			}},
		}},
		expectStatus: http.StatusBadRequest,
		expectBody:   "metrics in provided family do not have a timestamp",
	}, {
		name: "unsorted",
		families: []*clientmodel.MetricFamily{{
			Name: &fooMetricName,
			Metric: []*clientmodel.Metric{{
				Label: []*clientmodel.LabelPair{
					{Name: &clientIDLabelName, Value: &clientIDLabelValue},
					{Name: &fooLabelName, Value: &fooLabelValue1},
				},
				Counter:     &clientmodel.Counter{Value: &value42},
				TimestampMs: &timestampNewer,
			}, {
				Label: []*clientmodel.LabelPair{
					{Name: &clientIDLabelName, Value: &clientIDLabelValue},
					{Name: &fooLabelName, Value: &fooLabelValue1},
				},
				Counter:     &clientmodel.Counter{Value: &value42},
				TimestampMs: &timestamp,
			}},
		}},
		expectStatus: http.StatusBadRequest,
		expectBody:   "metrics in provided family are not in increasing timestamp order",
	}, {
		name: "tooOld",
		families: []*clientmodel.MetricFamily{{
			Name: &fooMetricName,
			Metric: []*clientmodel.Metric{{
				Label: []*clientmodel.LabelPair{
					{Name: &clientIDLabelName, Value: &clientIDLabelValue},
					{Name: &fooLabelName, Value: &fooLabelValue1},
				},
				Counter:     &clientmodel.Counter{Value: &value42},
				TimestampMs: &timestampTooOld,
			}},
		}},
		expectStatus: http.StatusBadRequest,
		expectBody:   "metrics in provided family have a timestamp that is too old, check clock skew",
	}, {
		name: "missingRequiredLabel",
		families: []*clientmodel.MetricFamily{{
			Name: &fooMetricName,
			Metric: []*clientmodel.Metric{{
				Label: []*clientmodel.LabelPair{
					{Name: &fooLabelName, Value: &fooLabelValue1},
				},
				Counter:     &clientmodel.Counter{Value: &value42},
				TimestampMs: &timestampTooOld,
			}},
		}},
		expectStatus: http.StatusBadRequest,
		expectBody:   "a required label is missing from the metric",
	}}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			encoder := expfmt.NewEncoder(buf, expfmt.FmtText)
			for _, f := range tc.families {
				if err := encoder.Encode(f); err != nil {
					t.Fatal(err)
				}
			}

			req, err := http.NewRequest(http.MethodPost, s.URL, buf)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", string(expfmt.FmtText))

			resp, err := s.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}

			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}

			if resp.StatusCode != tc.expectStatus {
				t.Errorf("expected status code %d but got %d: %s", tc.expectStatus, resp.StatusCode, string(body))
			}
			if strings.TrimSpace(string(body)) != tc.expectBody {
				t.Errorf("expected body to be '%s' but got '%s'", tc.expectBody, strings.TrimSpace(string(body)))
			}
		})
	}
}
