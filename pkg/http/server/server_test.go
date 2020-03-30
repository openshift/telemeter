package server

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/snappy"
)

func TestPostMethod(t *testing.T) {
	s := httptest.NewServer(PostMethod(func(w http.ResponseWriter, r *http.Request) {}))

	{
		req, err := http.NewRequest(http.MethodGet, s.URL, nil)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("expected %d and got %d", resp.StatusCode, http.StatusMethodNotAllowed)
		}
	}
	{
		req, err := http.NewRequest(http.MethodPost, s.URL, nil)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected %d and got %d", resp.StatusCode, http.StatusOK)
		}
	}
}

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

		resp, err := http.DefaultClient.Do(req)
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

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected %d and got %d", resp.StatusCode, http.StatusOK)
		}
	}
}
