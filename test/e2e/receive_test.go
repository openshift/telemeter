package e2e

import (
	"bytes"
	"context"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/kit/log"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/receive"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/prompb"
)

// TODO(bwplotka): Move this to main_e2e_test.go with properly mocked auth.

func TestReceiveValidateLabels(t *testing.T) {
	testcases := []struct {
		Name             string
		Timeseries       []prompb.TimeSeries
		ExpectStatusCode int
	}{
		{
			Name: "NoLabels",
			Timeseries: []prompb.TimeSeries{{
				Labels: []prompb.Label{},
			}},
			ExpectStatusCode: http.StatusBadRequest,
		},
		{
			Name: "MissingRequiredLabel",
			Timeseries: []prompb.TimeSeries{{
				Labels: []prompb.Label{{Name: "foo", Value: "bar"}},
			}},
			ExpectStatusCode: http.StatusBadRequest,
		},
		{
			Name: "Valid",
			Timeseries: []prompb.TimeSeries{{
				Labels: []prompb.Label{{Name: "__name__", Value: "foo"}},
			}},
			ExpectStatusCode: http.StatusOK,
		},
		{
			Name: "MultipleMissingRequiredLabel",
			Timeseries: []prompb.TimeSeries{{
				Labels: []prompb.Label{{Name: "foo", Value: "bar"}},
			}, {
				Labels: []prompb.Label{{Name: "bar", Value: "baz"}},
			}},
			ExpectStatusCode: http.StatusBadRequest,
		},
		{
			Name: "OneMultipleMissingRequiredLabel",
			Timeseries: []prompb.TimeSeries{{
				Labels: []prompb.Label{{Name: "foo", Value: "bar"}},
			}, {
				Labels: []prompb.Label{{Name: "__name__", Value: "foo"}},
			}},
			ExpectStatusCode: http.StatusBadRequest,
		},
		{
			Name: "MultipleValid",
			Timeseries: []prompb.TimeSeries{{
				Labels: []prompb.Label{{Name: "__name__", Value: "foo"}},
			}, {
				Labels: []prompb.Label{{Name: "__name__", Value: "bar"}},
			}},
			ExpectStatusCode: http.StatusOK,
		},
	}

	var receiveServer *httptest.Server
	{
		receiveServer = httptest.NewServer(func() http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {}
		}())
		defer receiveServer.Close()
	}

	var telemeterServer *httptest.Server
	{
		receiver, err := receive.NewHandler(log.NewNopLogger(), receiveServer.URL, &http.Client{}, prometheus.NewRegistry(), "default-tenant", nil, nil)
		if err != nil {
			t.Error("failed to initialize receive handler")
		}

		telemeterServer = httptest.NewServer(
			fakeAuthorizeHandler(
				receiver.TransformAndValidateWriteRequest(
					log.NewNopLogger(),
					http.HandlerFunc(receiver.Receive),
					"__name__",
				),
				&authorize.Client{ID: "test"},
			),
		)

		defer telemeterServer.Close()
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {
			wreq := &prompb.WriteRequest{Timeseries: tc.Timeseries}
			data, err := proto.Marshal(wreq)
			if err != nil {
				t.Error("failed to marshal proto message")
			}
			compressed := snappy.Encode(nil, data)

			resp, err := http.Post(telemeterServer.URL+"/metrics/v1/receive", "", bytes.NewBuffer(compressed))
			if err != nil {
				t.Error("failed to send the receive request: %w", err)
			}
			defer resp.Body.Close()

			body, _ := ioutil.ReadAll(resp.Body)
			if resp.StatusCode != tc.ExpectStatusCode {
				t.Errorf("request did not return %d, but %s: %s", tc.ExpectStatusCode, resp.Status, string(body))
			}
		})
	}
}

func TestLimitBodySize(t *testing.T) {
	var receiveServer *httptest.Server
	{
		receiveServer = httptest.NewServer(func() http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {}
		}())
		defer receiveServer.Close()
	}

	var telemeterServer *httptest.Server
	{
		logger := log.NewNopLogger()
		receiver, err := receive.NewHandler(logger, receiveServer.URL, &http.Client{}, prometheus.NewRegistry(), "default-tenant", nil, nil)
		if err != nil {
			t.Error("failed to initialize receive handler")
		}

		telemeterServer = httptest.NewServer(
			fakeAuthorizeHandler(
				receiver.LimitBodySize(logger, receive.DefaultRequestLimit,
					http.HandlerFunc(receiver.Receive),
				),
				&authorize.Client{ID: "test"},
			),
		)
		defer telemeterServer.Close()
	}
}

func fakeAuthorizeHandler(h http.Handler, client *authorize.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req = req.WithContext(context.WithValue(req.Context(), authorize.TenantKey, client.ID))
		req = req.WithContext(authorize.WithClient(req.Context(), client))
		h.ServeHTTP(w, req)
	})
}
