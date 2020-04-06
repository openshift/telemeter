package jwt

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/go-kit/kit/log"

	"github.com/openshift/telemeter/pkg/authorize"
)

type statusCodeErr struct {
	code int
	err  string
}

func newStatusCodeErr(code int, err string) statusCodeErr {
	return statusCodeErr{
		code: code,
		err:  err,
	}
}

func (e statusCodeErr) Error() string {
	return e.err
}

func (e statusCodeErr) HTTPStatusCode() int {
	return e.code
}

func newTestClusterAuthorizer(subject string, err error) authorize.ClusterAuthorizer {
	return authorize.ClusterAuthorizerFunc(func(token, cluster string) (string, error) {
		return subject, err
	})
}

type requestBuilder struct{ *http.Request }

func (r requestBuilder) WithHeaders(kvs ...string) requestBuilder {
	r.Header = make(http.Header)
	for i := 0; i < len(kvs)/2; i++ {
		k := kvs[i*2]
		v := kvs[i*2+1]
		r.Header.Set(k, v)
	}
	return r
}

func (r requestBuilder) WithForm(kvs ...string) requestBuilder {
	r.Form = make(url.Values)
	for i := 0; i < len(kvs)/2; i++ {
		k := kvs[i*2]
		v := kvs[i*2+1]
		r.Form.Set(k, v)
	}
	return r
}

func TestAuthorizeClusterHandler(t *testing.T) {
	clusterIDKey := "_id"
	labels := map[string]string{
		"foo": "bar",
		"baz": "qux",
	}
	pk, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}

	type checkFunc func(*httptest.ResponseRecorder) error

	labelsEqual := func(labels map[string]string, id string) checkFunc {
		return func(rec *httptest.ResponseRecorder) error {
			var tr authorize.TokenResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
				return fmt.Errorf("failed to unmarshal TokenResponse: %v", err)
			}
			if tr.Labels[clusterIDKey] != id {
				return fmt.Errorf("expected response to have '%s=%s', got '%s=%s'", clusterIDKey, id, clusterIDKey, tr.Labels[clusterIDKey])
			}
			delete(tr.Labels, clusterIDKey)
			if len(labels) > len(tr.Labels) {
				for k, v := range labels {
					if v != tr.Labels[k] {
						return fmt.Errorf("expected response to have '%s=%s', got '%s=%s'", k, v, k, tr.Labels[k])
					}
				}
			}
			for k, v := range tr.Labels {
				if v != labels[k] {
					return fmt.Errorf("unexpected label in response: got '%s=%s', expected '%s=%s'", k, v, k, labels[k])
				}
			}

			return nil
		}
	}

	responseCodeIs := func(code int) checkFunc {
		return func(rec *httptest.ResponseRecorder) error {
			if got := rec.Code; got != code {
				return fmt.Errorf("want HTTP response code %d, got %d", code, got)
			}
			return nil
		}
	}

	for _, tc := range []struct {
		name        string
		signer      *Signer
		clusterAuth authorize.ClusterAuthorizer
		req         *http.Request
		check       checkFunc
	}{
		{
			name:  "invalid method",
			req:   httptest.NewRequest("GET", "https://telemeter", nil),
			check: responseCodeIs(405),
		},

		{
			name:  "no auth header",
			req:   httptest.NewRequest("POST", "https://telemeter", nil),
			check: responseCodeIs(400),
		},

		{
			name: "invalid auth header",
			req: requestBuilder{httptest.NewRequest("POST", "https://telemeter", nil)}.
				WithForm("id", "test").
				WithHeaders("Authorization", "invalid").
				Request,
			check: responseCodeIs(401),
		},

		{
			name: "cluster auth failed",
			req: requestBuilder{httptest.NewRequest("POST", "https://telemeter", nil)}.
				WithForm("id", "test").
				WithHeaders("Authorization", "bearer invalid").
				Request,
			clusterAuth: newTestClusterAuthorizer("", errors.New("invalid")),
			check:       responseCodeIs(500),
		},
		{
			name: "cluster auth returned error",
			req: requestBuilder{httptest.NewRequest("POST", "https://telemeter", nil)}.
				WithForm("id", "test").
				WithHeaders("Authorization", "bearer invalid").
				Request,
			clusterAuth: newTestClusterAuthorizer("", newStatusCodeErr(666, "some error")),
			check:       responseCodeIs(666),
		},
		{
			name: "signing failed",
			req: requestBuilder{httptest.NewRequest("POST", "https://telemeter", nil)}.
				WithForm("id", "test").
				WithHeaders("Authorization", "bearer valid").
				Request,
			clusterAuth: newTestClusterAuthorizer("sub123", nil),
			signer:      NewSigner("iss456", crypto.PrivateKey(nil)),
			check:       responseCodeIs(500),
		},
		{
			name: "cluster auth success",
			req: requestBuilder{httptest.NewRequest("POST", "https://telemeter", nil)}.
				WithForm("id", "test").
				WithHeaders("Authorization", "bearer valid").
				Request,
			clusterAuth: newTestClusterAuthorizer("sub123", nil),
			signer:      NewSigner("iss456", pk),
			check:       responseCodeIs(200),
		},
		{
			name: "labels equal success",
			req: requestBuilder{httptest.NewRequest("POST", "https://telemeter", nil)}.
				WithForm("id", "test").
				WithHeaders("Authorization", "bearer valid").
				Request,
			clusterAuth: newTestClusterAuthorizer("sub123", nil),
			signer:      NewSigner("iss456", pk),
			check:       labelsEqual(labels, "test"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewAuthorizeClusterHandler(log.NewNopLogger(), clusterIDKey, 2, tc.signer, labels, tc.clusterAuth)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tc.req)
			if err := tc.check(rec); err != nil {
				t.Error(err)
			}
		})
	}
}
