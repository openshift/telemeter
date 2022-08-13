package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/efficientgo/tools/core/pkg/testutil"
	"github.com/go-kit/log"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/prometheus/pkg/timestamp"
	"github.com/prometheus/prometheus/prompb"
	"go.uber.org/goleak"
)

const sampleMetrics = `
up{job="test",label="value0"} 1
up{job="test",label="value1"} 1
up{job="test",label="value2"} 0
`

var expectedTimeSeries = []prompb.TimeSeries{
	{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "up"},
			{Name: "cluster", Value: "dynamic"},
			{Name: "job", Value: "test"},
			{Name: "label", Value: "value0"},
		},
		Samples: []prompb.Sample{{Value: 1}},
	},
	{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "up"},
			{Name: "cluster", Value: "dynamic"},
			{Name: "job", Value: "test"},
			{Name: "label", Value: "value1"},
		},
		Samples: []prompb.Sample{{Value: 1}},
	},
	{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "up"},
			{Name: "cluster", Value: "dynamic"},
			{Name: "job", Value: "test"},
			{Name: "label", Value: "value2"},
		},
		Samples: []prompb.Sample{{Value: 0}},
	},
}

func TestServer(t *testing.T) {
	defer goleak.VerifyNone(t)

	var receiveServer *httptest.Server
	{
		// This is the receiveServer that the Telemeter Server is going to forward to
		// upon receiving metrics itself.
		receiveServer = httptest.NewServer(mockedReceiver(t))
		defer receiveServer.Close()
	}

	for _, tcase := range []struct {
		name      string
		extraOpts func(opts *Options)
	}{
		{
			name:      "without OIDC",
			extraOpts: func(opts *Options) {},
		},
		//{
		//	// TODO(bwplotka): Mock OIDC server and uncomment.
		//	name: "with OIDC",
		//	extraOpts: func(opts *Options) {
		//		opts.OIDCIssuer = "..."
		//		opts.OIDCClientID = "..."
		//		opts.OIDCClientSecret = "..."
		//		opts.OIDCAudienceEndpoint = "...api/v2/"
		//	},
		//},
	} {
		t.Run(tcase.name, func(t *testing.T) {
			prometheus.DefaultRegisterer = prometheus.NewRegistry()

			ext, err := net.Listen("tcp", "127.0.0.1:0")
			testutil.Ok(t, err)

			var wg sync.WaitGroup
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer func() {
				cancel()
				wg.Wait()
			}()

			{
				opts := defaultOpts()
				opts.ForwardURL = receiveServer.URL
				opts.TenantID = "default-tenant"
				opts.Labels = map[string]string{"cluster": "test"}
				opts.clusterIDKey = "cluster"
				opts.Logger = log.NewLogfmtLogger(os.Stderr)
				opts.Whitelist = []string{"up"}
				opts.Ratelimit = 0
				tcase.extraOpts(opts)

				local, err := net.Listen("tcp", "127.0.0.1:0")
				testutil.Ok(t, err)

				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := opts.Run(ctx, ext, local); err != context.Canceled {
						t.Fatal(err)
					}
				}()
			}

			// TODO(bwplotka): Test failure cases!

			for _, cluster := range []string{"cluster1", "cluster2", "cluster3"} {
				t.Run(cluster, func(t *testing.T) {
					tokenResp := authorize.TokenResponse{}
					t.Run("authorize", func(t *testing.T) {
						// Authorize first.
						req, err := http.NewRequest(http.MethodPost, "http://"+ext.Addr().String()+"/authorize", nil)
						testutil.Ok(t, err)

						q := req.URL.Query()
						q.Add("id", cluster)
						req.URL.RawQuery = q.Encode()
						req.Header.Set("Authorization", "bearer whatever")
						resp, err := http.DefaultClient.Do(req.WithContext(ctx))
						testutil.Ok(t, err)

						defer resp.Body.Close()
						body, err := ioutil.ReadAll(resp.Body)
						testutil.Ok(t, err)

						testutil.Equals(t, 2, resp.StatusCode/100, "request did not return 2xx, but %s: %s", resp.Status, string(body))

						testutil.Ok(t, json.Unmarshal(body, &tokenResp))
					})

					for i := 0; i < 5; i++ {
						t.Run("upload", func(t *testing.T) {
							metricFamilies := readMetrics(t, sampleMetrics, cluster)

							buf := &bytes.Buffer{}
							encoder := expfmt.NewEncoder(buf, expfmt.FmtProtoDelim)
							for _, f := range metricFamilies {
								testutil.Ok(t, encoder.Encode(f))
							}

							req, err := http.NewRequest(http.MethodPost, "http://"+ext.Addr().String()+"/upload", buf)
							testutil.Ok(t, err)

							req.Header.Set("Content-Type", string(expfmt.FmtProtoDelim))
							req.Header.Set("Authorization", "bearer "+tokenResp.Token)
							resp, err := http.DefaultClient.Do(req.WithContext(ctx))
							testutil.Ok(t, err)

							defer resp.Body.Close()

							body, err := ioutil.ReadAll(resp.Body)
							testutil.Ok(t, err)

							testutil.Equals(t, http.StatusOK, resp.StatusCode, string(body))
						})
					}
				})
			}
		})
	}
}

func readMetrics(t *testing.T, m string, cluster string) []*clientmodel.MetricFamily {
	var families []*clientmodel.MetricFamily

	now := timestamp.FromTime(time.Now())
	decoder := expfmt.NewDecoder(bytes.NewBufferString(m), expfmt.FmtText)
	for {
		family := clientmodel.MetricFamily{}
		if err := decoder.Decode(&family); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		for _, m := range family.Metric {
			m.TimestampMs = &now
			k := "cluster"
			v := cluster
			m.Label = append(m.Label, &clientmodel.LabelPair{Name: &k, Value: &v})
		}
		families = append(families, &family)
	}
	return families
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
				if l.Value == "dynamic" {
					continue
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
