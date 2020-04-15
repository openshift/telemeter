package server

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/snappy"
)

func TestSnappy(t *testing.T) {
	message := "some test payload"

	s := httptest.NewServer(Snappy(func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()

		if string(body) != message {
			t.Errorf("expected body to be '%s'. got '%s'", message, body)
		}
	}))

	{
		payload := snappy.Encode(nil, []byte(message))

		req, err := http.NewRequest(http.MethodPost, s.URL, bytes.NewBuffer(payload))
		if err != nil {
			t.Fatal(err)
		}

		req.Header.Set("Content-Encoding", "snappy")

		resp, err := s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected %d and got %d", resp.StatusCode, http.StatusOK)
		}
	}
	{
		req, err := http.NewRequest(http.MethodPost, s.URL, bytes.NewBufferString(message))
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
