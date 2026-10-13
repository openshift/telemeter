package ssl

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
)

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
