package ssl

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
)

func TestClientCertConfigValidateSecrets(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "legacy", input: `{"secret":"old-key"}`},
		{name: "single key list", input: `{"secrets":["new-key"]}`},
		{name: "multiple keys", input: `{"secrets":["old-key","new-key"]}`},
		{name: "legacy and list", input: `{"secret":"old-key","secrets":["new-key"]}`},
		{name: "legacy with empty list", input: `{"secret":"old-key","secrets":[]}`},
		{name: "empty legacy with list", input: `{"secret":"","secrets":["new-key"]}`},
		{name: "missing keys", input: `{}`, wantErr: "secret or secrets must be set"},
		{name: "empty legacy", input: `{"secret":""}`, wantErr: "secret or secrets must be set"},
		{name: "empty list", input: `{"secrets":[]}`, wantErr: "secret or secrets must be set"},
		{name: "null list", input: `{"secrets":null}`, wantErr: "secret or secrets must be set"},
		{name: "empty entry", input: `{"secrets":[""]}`, wantErr: "secrets must not contain empty values"},
		{name: "null entry", input: `{"secrets":[null]}`, wantErr: "secrets must not contain empty values"},
		{name: "mixed empty entry", input: `{"secrets":["new-key",""]}`, wantErr: "secrets must not contain empty values"},
		{name: "legacy with empty entry", input: `{"secret":"old-key","secrets":[""]}`, wantErr: "secrets must not contain empty values"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var conf ClientCertConfig
			if err := json.Unmarshal([]byte(tc.input), &conf); err != nil {
				t.Fatal(err)
			}
			conf.Config = ClientCertInfo{SecretHeader: "x-secret", CommonNameHeader: "x-cn"}
			err := conf.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("expected error %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestClientCertInfoSecretRotation(t *testing.T) {
	stages := []struct {
		name     string
		input    string
		accepted []string
	}{
		{name: "before rotation", input: `{"secret":"old-key"}`, accepted: []string{"old-key"}},
		{name: "overlap with legacy", input: `{"secret":"old-key","secrets":["new-key"]}`, accepted: []string{"old-key", "new-key"}},
		{name: "overlap with list", input: `{"secrets":["old-key","new-key"]}`, accepted: []string{"old-key", "new-key"}},
		{name: "after rotation", input: `{"secrets":["new-key"]}`, accepted: []string{"new-key"}},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			var conf ClientCertConfig
			if err := json.Unmarshal([]byte(stage.input), &conf); err != nil {
				t.Fatal(err)
			}
			conf.Config = ClientCertInfo{SecretHeader: "x-secret", CommonNameHeader: "x-cn"}
			if err := conf.Validate(); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"", "old-key", "new-key", "unknown-key"} {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("x-secret", key)
				req.Header.Set("x-cn", "/O=test-O/CN=test-CN")
				res := httptest.NewRecorder()
				called := false
				handler := ClientCertInfoAsHeaders(conf, log.NewNopLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					if r.Context().Value(CommonNameContextKey{}) != "test-CN" || r.Context().Value(OrganizationContextKey{}) != "test-O" {
						t.Error("missing client identity in request context")
					}
				}))
				handler.ServeHTTP(res, req)
				wantStatus := http.StatusForbidden
				for _, accepted := range stage.accepted {
					if key == accepted {
						wantStatus = http.StatusOK
					}
				}
				if res.Code != wantStatus || called != (wantStatus == http.StatusOK) {
					t.Errorf("key %q: expected status %d, got %d (handler called: %t)", key, wantStatus, res.Code, called)
				}
			}
		})
	}
}

func TestClientCertInfoAsHeaders(t *testing.T) {
	const (
		secret  = "abc"
		xSecret = "x-secret"
		xCn     = "x-cn"

		expectO  = "test-O"
		expectCn = "test-CN"
	)

	// buildRequest builds a request with the expected headers that should
	// get a 200 response
	buildRequest := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "http://test.com", nil)
		req.Header.Set(xCn, fmt.Sprintf("/O=%s/CN=%s", expectO, expectCn))
		req.Header.Set(xSecret, secret)
		return req
	}

	// test function to validate the context has been set in previously in middleware
	// chain when ClientCertInfoAsHeaders is called
	testMiddleware := func(t *testing.T, name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			fn := func(w http.ResponseWriter, r *http.Request) {
				expectCN, ok := r.Context().Value(CommonNameContextKey{}).(string)
				if !ok || r.Context().Value(CommonNameContextKey{}) != expectCn {
					t.Errorf("%s: expected %s, got %s", name, expectCn, expectCN)
				}
				expectOrg, ok := r.Context().Value(OrganizationContextKey{}).(string)
				if !ok || r.Context().Value(OrganizationContextKey{}) != expectO {
					t.Errorf("%s: expected %s, got %s", name, expectO, expectOrg)
				}

				next.ServeHTTP(w, r)
			}
			return http.HandlerFunc(fn)
		}
	}

	conf := ClientCertConfig{
		Secret: secret,
		Config: ClientCertInfo{
			SecretHeader:     xSecret,
			CommonNameHeader: xCn,
			IssuerHeader:     "x-issuer",
		},
	}

	tests := []struct {
		name    string
		request func() *http.Request
		expect  int
	}{
		{
			name: "Test missing secret header returns 403",
			request: func() *http.Request {
				req := buildRequest()
				req.Header.Del(xSecret)
				return req
			},
			expect: http.StatusForbidden,
		},
		{
			name: "Test empty secret header returns 403",
			request: func() *http.Request {
				req := buildRequest()
				req.Header.Set(xSecret, "")
				return req
			},
			expect: http.StatusForbidden,
		},
		{
			name: "Test mismatched PSK returns 403",
			request: func() *http.Request {
				req := buildRequest()
				req.Header.Set(xSecret, "123")
				return req
			},
			expect: http.StatusForbidden,
		},
		{
			name: "Test missing CN returns 403",
			request: func() *http.Request {
				req := buildRequest()
				req.Header.Set(xCn, "")
				return req
			},
			expect: http.StatusForbidden,
		},
		{
			name: "Test invalid CN returns 403",
			request: func() *http.Request {
				req := buildRequest()
				req.Header.Set(xCn, "invalid")
				return req
			},
			expect: http.StatusForbidden,
		},
		{
			name:    "Test happy path",
			request: buildRequest,
			expect:  http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {}
			req := tc.request()
			res := httptest.NewRecorder()
			handler(res, req)

			mw := testMiddleware(t, tc.name)
			middlewareUnderTest := ClientCertInfoAsHeaders(conf, log.NewNopLogger())
			srv := middlewareUnderTest(mw(http.HandlerFunc(handler)))
			srv.ServeHTTP(res, req)

			if res.Code != tc.expect {
				t.Errorf("%s: expected %d, got %d", tc.name, tc.expect, res.Code)
			}
		})

	}
}
