package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/efficientgo/core/testutil"
	"github.com/go-kit/log"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/prometheus/prompb"
	"go.uber.org/goleak"
)

var expectedTimeSeries = []prompb.TimeSeries{
	{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "up"},
			{Name: "_id", Value: "fixed"},
			{Name: "cluster", Value: "dynamic"},
			{Name: "job", Value: "test"},
			{Name: "label", Value: "value0"},
		},
		Samples: []prompb.Sample{{Value: 1}},
	},
	{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "up"},
			{Name: "_id", Value: "fixed"},
			{Name: "cluster", Value: "dynamic"},
			{Name: "job", Value: "test"},
			{Name: "label", Value: "value1"},
		},
		Samples: []prompb.Sample{{Value: 1}},
	},
	{
		Labels: []prompb.Label{
			{Name: "__name__", Value: "up"},
			{Name: "_id", Value: "fixed"},
			{Name: "cluster", Value: "dynamic"},
			{Name: "job", Value: "test"},
			{Name: "label", Value: "value2"},
		},
		Samples: []prompb.Sample{{Value: 0}},
	},
}

func TestServerRhelMtls(t *testing.T) {
	defer goleak.VerifyNone(t)

	receiveServer := httptest.NewServer(mockedReceiver(t))
	defer receiveServer.Close()

	telemeterClient, err := makeMTLSClient()
	testutil.Ok(t, err)

	testCases := []struct {
		name      string
		extraOpts func(opts *Options)
	}{
		{
			name: "mTLS",
			extraOpts: func(opts *Options) {
				opts.TLSKeyPath = "testdata/server-private-key.pem"
				opts.TLSCertificatePath = "testdata/server-cert.pem"
				opts.TLSCACertificatePath = "testdata/ca-cert.pem"
			},
		},
	}

	for _, tcase := range testCases {
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

			opts := setTestDefaultOpts()
			opts.ForwardURL = receiveServer.URL
			tcase.extraOpts(opts)

			local, err := net.Listen("tcp", "127.0.0.1:0")
			testutil.Ok(t, err)

			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := opts.Run(ctx, ext, local); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}()

			// Wait for server to start by pinging it.
			for i := 0; i < 30; i++ {
				time.Sleep(100 * time.Millisecond)

				res, err := telemeterClient.Get("https://" + ext.Addr().String() + "/")
				if err != nil {
					fmt.Println("Waiting for server to start...", err)
					continue
				}

				res.Body.Close()

				if res.StatusCode == http.StatusOK {
					break
				}
				fmt.Println("Waiting for server to start...", res.StatusCode)
			}

			for _, cluster := range []string{"cluster1"} {
				t.Run(cluster, func(t *testing.T) {

					for i := 0; i < 1; i++ {
						t.Run("upload", func(t *testing.T) {
							var wr prompb.WriteRequest
							wr.Timeseries = expectedTimeSeries
							data, err := proto.Marshal(&wr)
							testutil.Ok(t, err)

							compressedData := snappy.Encode(nil, data)

							req, err := http.NewRequest(http.MethodPost, "https://"+ext.Addr().String()+"/metrics/v1/receive", bytes.NewReader(compressedData))
							testutil.Ok(t, err)

							req.Header.Set("Content-Type", string(expfmt.FmtProtoDelim))
							resp, err := telemeterClient.Do(req.WithContext(ctx))
							testutil.Ok(t, err)

							defer resp.Body.Close()

							body, err := io.ReadAll(resp.Body)
							testutil.Ok(t, err)

							testutil.Equals(t, http.StatusOK, resp.StatusCode, string(body))
						})
					}
				})
			}
		})
	}
}

func TestServerRhelWithClientInfoFromHeaders(t *testing.T) {
	defer goleak.VerifyNone(t)

	receiveServer := httptest.NewServer(mockedReceiver(t))
	defer receiveServer.Close()

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	telemeterClient := &http.Client{
		Transport: tr,
	}

	testCases := []struct {
		name         string
		extraOpts    func(opts *Options)
		makeRequest  func(url string, withBody io.Reader) *http.Request
		expectStatus int
	}{
		{
			name: "Test client info from headers with no header set",
			extraOpts: func(opts *Options) {
				opts.ClientInfoFromRequestConfigFile = "testdata/client-info.json"
				opts.TLSKeyPath = "testdata/server-private-key.pem"
				opts.TLSCertificatePath = "testdata/server-cert.pem"
				opts.TLSCACertificatePath = "testdata/ca-cert.pem"
			},
			makeRequest: func(url string, withBody io.Reader) *http.Request {
				req, err := http.NewRequest(http.MethodPost, url, withBody)
				testutil.Ok(t, err)
				req.Header.Set("Content-Type", string(expfmt.FmtProtoDelim))
				return req
			},
			expectStatus: http.StatusForbidden,
		},
		{
			name: "Test client info from headers with empty header set",
			extraOpts: func(opts *Options) {
				opts.ClientInfoFromRequestConfigFile = "testdata/client-info.json"
				opts.TLSKeyPath = "testdata/server-private-key.pem"
				opts.TLSCertificatePath = "testdata/server-cert.pem"
				opts.TLSCACertificatePath = "testdata/ca-cert.pem"
			},
			makeRequest: func(url string, withBody io.Reader) *http.Request {
				req, err := http.NewRequest(http.MethodPost, url, withBody)
				testutil.Ok(t, err)
				req.Header.Set("Content-Type", string(expfmt.FmtProtoDelim))
				req.Header.Set("x-secret", "")
				return req
			},
			expectStatus: http.StatusForbidden,
		},
		{
			name: "Test client info from headers with bad header set",
			extraOpts: func(opts *Options) {
				opts.ClientInfoFromRequestConfigFile = "testdata/client-info.json"
				opts.TLSKeyPath = "testdata/server-private-key.pem"
				opts.TLSCertificatePath = "testdata/server-cert.pem"
				opts.TLSCACertificatePath = "testdata/ca-cert.pem"
			},
			makeRequest: func(url string, withBody io.Reader) *http.Request {
				req, err := http.NewRequest(http.MethodPost, url, withBody)
				testutil.Ok(t, err)
				req.Header.Set("Content-Type", string(expfmt.FmtProtoDelim))
				req.Header.Set("x-secret", "wrong")
				return req
			},
			expectStatus: http.StatusForbidden,
		},
		{
			name: "Test client info from headers with correct PSK but missing CN header",
			extraOpts: func(opts *Options) {
				opts.ClientInfoFromRequestConfigFile = "testdata/client-info.json"
				opts.TLSKeyPath = "testdata/server-private-key.pem"
				opts.TLSCertificatePath = "testdata/server-cert.pem"
				opts.TLSCACertificatePath = "testdata/ca-cert.pem"
			},
			makeRequest: func(url string, withBody io.Reader) *http.Request {
				req, err := http.NewRequest(http.MethodPost, url, withBody)
				testutil.Ok(t, err)
				req.Header.Set("Content-Type", string(expfmt.FmtProtoDelim))
				req.Header.Set("x-secret", "super-secret")
				return req
			},
			expectStatus: http.StatusForbidden,
		},
		{
			name: "Test client info from headers with correct PSK",
			extraOpts: func(opts *Options) {
				opts.ClientInfoFromRequestConfigFile = "testdata/client-info.json"
				opts.TLSKeyPath = "testdata/server-private-key.pem"
				opts.TLSCertificatePath = "testdata/server-cert.pem"
				opts.TLSCACertificatePath = "testdata/ca-cert.pem"
			},
			makeRequest: func(url string, withBody io.Reader) *http.Request {
				req, err := http.NewRequest(http.MethodPost, url, withBody)
				testutil.Ok(t, err)
				req.Header.Set("Content-Type", string(expfmt.FmtProtoDelim))
				req.Header.Set("x-secret", "super-secret")
				req.Header.Set("x-common-name", fmt.Sprintf("/O = %s, /CN = %s", "test", "test"))
				return req
			},
			expectStatus: http.StatusOK,
		},
		{
			name: "Test client info from headers with correct PSK and incorrect label validation",
			extraOpts: func(opts *Options) {
				opts.ClientInfoFromRequestConfigFile = "testdata/client-info.json"
				opts.TLSKeyPath = "testdata/server-private-key.pem"
				opts.TLSCertificatePath = "testdata/server-cert.pem"
				opts.TLSCACertificatePath = "testdata/ca-cert.pem"
				opts.ClientInfoSubjectLabel = "_id"
			},
			makeRequest: func(url string, withBody io.Reader) *http.Request {
				req, err := http.NewRequest(http.MethodPost, url, withBody)
				testutil.Ok(t, err)
				req.Header.Set("Content-Type", string(expfmt.FmtProtoDelim))
				req.Header.Set("x-secret", "super-secret")
				req.Header.Set("x-common-name", fmt.Sprintf("/O = %s, /CN = %s", "test", "test"))
				return req
			},
			expectStatus: http.StatusBadRequest,
		},
		{
			name: "Test client info from headers with correct PSK and valid label value",
			extraOpts: func(opts *Options) {
				opts.ClientInfoFromRequestConfigFile = "testdata/client-info.json"
				opts.TLSKeyPath = "testdata/server-private-key.pem"
				opts.TLSCertificatePath = "testdata/server-cert.pem"
				opts.TLSCACertificatePath = "testdata/ca-cert.pem"
				opts.ClientInfoSubjectLabel = "_id"
			},
			makeRequest: func(url string, withBody io.Reader) *http.Request {
				req, err := http.NewRequest(http.MethodPost, url, withBody)
				testutil.Ok(t, err)
				req.Header.Set("Content-Type", string(expfmt.FmtProtoDelim))
				req.Header.Set("x-secret", "super-secret")
				req.Header.Set("x-common-name", fmt.Sprintf("/O = %s, /CN = %s", "test", "fixed"))
				return req
			},
			expectStatus: http.StatusOK,
		},
	}

	for _, tcase := range testCases {
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

			opts := setTestDefaultOpts()
			opts.ForwardURL = receiveServer.URL
			tcase.extraOpts(opts)

			local, err := net.Listen("tcp", "127.0.0.1:0")
			testutil.Ok(t, err)

			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := opts.Run(ctx, ext, local); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}()

			// Wait for server to start by pinging it.
			for i := 0; i < 30; i++ {
				time.Sleep(100 * time.Millisecond)

				res, err := telemeterClient.Get("https://" + ext.Addr().String() + "/")
				if err != nil {
					fmt.Println("Waiting for server to start...", err)
					continue
				}

				res.Body.Close()
			}

			for _, cluster := range []string{"cluster1"} {
				t.Run(cluster, func(t *testing.T) {

					for i := 0; i < 1; i++ {
						t.Run("receive", func(t *testing.T) {
							var wr prompb.WriteRequest
							wr.Timeseries = expectedTimeSeries
							data, err := proto.Marshal(&wr)
							testutil.Ok(t, err)

							compressedData := snappy.Encode(nil, data)
							url := "https://" + ext.Addr().String() + "/metrics/v1/receive"
							req := tcase.makeRequest(url, bytes.NewReader(compressedData))

							resp, err := telemeterClient.Do(req.WithContext(ctx))
							testutil.Ok(t, err)

							defer resp.Body.Close()

							body, err := io.ReadAll(resp.Body)
							testutil.Ok(t, err)
							testutil.Equals(t, tcase.expectStatus, resp.StatusCode, string(body))
						})
					}
				})
			}
		})
	}
}

func setTestDefaultOpts() *Options {
	opts := defaultOpts()

	opts.Labels = map[string]string{"cluster": "test"}
	opts.Logger = log.NewLogfmtLogger(os.Stderr)
	opts.Whitelist = []string{"up"}
	opts.Ratelimit = 0
	return opts
}

func makeMTLSClient() (*http.Client, error) {
	cert, err := tls.LoadX509KeyPair("testdata/client-cert.pem", "testdata/client-private-key.pem")
	if err != nil {
		return nil, err
	}

	caCert, err := os.ReadFile("testdata/ca-cert.pem")
	if err != nil {
		return nil, err
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, errors.New("failed to add ca cert")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
	}

	transport := &http.Transport{TLSClientConfig: tlsConfig}
	return &http.Client{Transport: transport}, nil
}

// mockedReceiver unmarshalls the request body into prompb.WriteRequests
// and asserts the seeing contents against the pre-defined expectedTimeSeries from the top.
func mockedReceiver(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
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

		testutil.Equals(t, len(expectedTimeSeries), len(wreq.Timeseries))

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
