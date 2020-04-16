package server

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	clientmodel "github.com/prometheus/client_model/go"

	"github.com/openshift/telemeter/pkg/metricsclient"
)

func TestSnappy(t *testing.T) {
	metrics := []*clientmodel.MetricFamily{}

	s := httptest.NewServer(Snappy(func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Helper()
			t.Fatal(err)
		}
		defer r.Body.Close()

		fmt.Println(string(body))

		//if string(body) != message {
		//	t.Errorf("expected body to be '%s'. got '%s'", message, body)
		//}
	}))

	{
		payload := bytes.Buffer{}
		if err := metricsclient.Write(&payload, metrics); err != nil {
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
	//{
	//	req, err := http.NewRequest(http.MethodPost, s.URL, bytes.NewBufferString(message))
	//	if err != nil {
	//		t.Fatal(err)
	//	}
	//
	//	resp, err := s.Client().Do(req)
	//	if err != nil {
	//		t.Fatal(err)
	//	}
	//
	//	if resp.StatusCode != http.StatusOK {
	//		t.Errorf("expected %d and got %d", resp.StatusCode, http.StatusOK)
	//	}
	//}
}
