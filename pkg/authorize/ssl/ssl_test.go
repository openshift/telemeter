package ssl

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
)

func TestClientCertInfoAsHeaders(t *testing.T) {
	const (
		secret  = "abc"
		xSecret = "x-secret"
	)
	conf := ClientCertConfig{
		Secret: secret,
		Config: ClientCertInfo{
			SecretHeader:     xSecret,
			CommonNameHeader: "x-cn",
			IssuerHeader:     "x-issuer",
		},
	}

	tests := []struct {
		name    string
		request func() *http.Request
		expect  int
	}{
		{
			name: "Test missing header returns 403",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://test.com", nil)
				return req
			},
			expect: http.StatusForbidden,
		},
		{
			name: "Test empty header returns 403",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://test.com", nil)
				req.Header.Set(xSecret, "")
				return req
			},
			expect: http.StatusForbidden,
		},
		{
			name: "Test mismatched PSK returns 403",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://test.com", nil)
				req.Header.Set(xSecret, "123")
				return req
			},
			expect: http.StatusForbidden,
		},
		{
			name: "Test happy path",
			request: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://test.com", nil)
				req.Header.Set(xSecret, secret)
				return req
			},
			expect: http.StatusOK,
		},
	}

	for _, tc := range tests {
		handler := func(w http.ResponseWriter, r *http.Request) {}
		req := tc.request()
		res := httptest.NewRecorder()
		handler(res, req)

		middleware := ClientCertInfoAsHeaders(conf, log.NewNopLogger())
		srv := middleware(http.HandlerFunc(handler))
		srv.ServeHTTP(res, req)

		if res.Code != tc.expect {
			t.Errorf("expected %d, got %d", tc.expect, res.Code)
		}
	}
}
