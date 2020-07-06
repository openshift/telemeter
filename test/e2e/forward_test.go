package e2e

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/prometheus/prompb"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/server"
)

const sampleMetrics = `
up{cluster="test",job="test",label="value0"} 1 1562500000000
up{cluster="test",job="test",label="value1"} 1 1562600000000
up{cluster="test",job="test",label="value2"} 0 1562700000000
`

var expectedTimeSeries = []prompb.TimeSeries{
	{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "up"},
			{Name: "cluster", Value: "test"},
			{Name: "job", Value: "test"},
			{Name: "label", Value: "value0"},
		},
		Samples: []prompb.Sample{{Value: 1}},
	},
	{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "up"},
			{Name: "cluster", Value: "test"},
			{Name: "job", Value: "test"},
			{Name: "label", Value: "value1"},
		},
		Samples: []prompb.Sample{{Value: 1}},
	},
	{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "up"},
			{Name: "cluster", Value: "test"},
			{Name: "job", Value: "test"},
			{Name: "label", Value: "value2"},
		},
		Samples: []prompb.Sample{{Value: 0}},
	},
}

func TestForward(t *testing.T) {
	var receiveServer *httptest.Server
	{
		// This is the receiveServer that the Telemeter Server is going to forward to
		// upon receiving metrics itself.
		receiveServer = httptest.NewServer(mockedReceiver(t))
		defer receiveServer.Close()
	}
	var telemeterServer *httptest.Server
	{
		logger := log.NewNopLogger()

		labels := map[string]string{"cluster": "test"}

		receiveURL, _ := url.Parse(receiveServer.URL)

		telemeterServer = httptest.NewServer(
			fakeAuthorizeHandler(
				server.ClusterID(log.NewNopLogger(), "cluster",
					server.Ratelimit(log.NewNopLogger(), 4*time.Minute+30*time.Second, time.Now,
						server.Snappy(
							server.Validate(log.NewNopLogger(), metricfamily.MultiTransformer{}, 10*365*24*time.Hour, 500*1024, time.Now,
								server.ForwardHandler(logger, receiveURL, "default-tenant", &http.Client{}),
							),
						),
					),
				),
				&authorize.Client{ID: "test", Labels: labels},
			),
		)
		defer telemeterServer.Close()
	}

	metricFamilies := readMetrics(sampleMetrics)

	buf := &bytes.Buffer{}
	encoder := expfmt.NewEncoder(buf, expfmt.FmtProtoDelim)
	for _, f := range metricFamilies {
		if err := encoder.Encode(f); err != nil {
			t.Fatalf("failed to encode metric family: %v", err)
		}
	}

	// The following code send the initial request to the Telemeter Server
	// which then forwards the converted metrics as time series to the mocked receive server.
	// In the end we check for a 200 OK status code.

	resp, err := http.Post(telemeterServer.URL, string(expfmt.FmtProtoDelim), buf)
	if err != nil {
		t.Errorf("failed sending the upload request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		t.Errorf("request did not return 2xx, but %s: %s", resp.Status, string(body))
	}
}

func readMetrics(m string) []*clientmodel.MetricFamily {
	var families []*clientmodel.MetricFamily

	decoder := expfmt.NewDecoder(bytes.NewBufferString(m), expfmt.FmtText)
	for {
		family := clientmodel.MetricFamily{}
		if err := decoder.Decode(&family); err != nil {
			if err == io.EOF {
				break
			}
			panic(err)
		}
		families = append(families, &family)
	}

	return families
}

func fakeAuthorizeHandler(h http.Handler, client *authorize.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req = req.WithContext(context.WithValue(req.Context(), authorize.TenantKey, client.ID))
		req = req.WithContext(authorize.WithClient(req.Context(), client))
		h.ServeHTTP(w, req)
	})
}

// mockedReceiver unmarshalls the request body into prompb.WriteRequests
// and asserts the seeing contents against the pre-defined expectedTimeSeries from the top.
func mockedReceiver(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed reading body from forward request: %v", err)
		}

		reqBuf, err := snappy.Decode(nil, body)
		if err != nil {
			t.Errorf("failed to decode the snappy request: %v", err)
		}

		var wreq prompb.WriteRequest
		if err := proto.Unmarshal(reqBuf, &wreq); err != nil {
			t.Errorf("failed to unmarshal WriteRequest: %v", err)
		}

		tsc := len(wreq.Timeseries)
		if tsc != 3 {
			t.Errorf("expected 3 timeseries to be forwarded, got %d", tsc)
		}

		for i, ts := range expectedTimeSeries {
			for j, l := range ts.Labels {
				wl := wreq.Timeseries[i].Labels[j]
				if l.Name != wl.Name {
					t.Errorf("expected label name %s, got %s", l.Name, wl.Name)
				}
				if l.Value != wl.Value {
					t.Errorf("expected label value %s, got %s", l.Value, wl.Value)
				}
			}
			for j, s := range ts.Samples {
				ws := wreq.Timeseries[i].Samples[j]
				if s.Value != ws.Value {
					t.Errorf("expected value for sample %2.f, got %2.f", s.Value, ws.Value)
				}
			}
		}
	}
}
