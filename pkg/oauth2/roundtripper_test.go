package oauth2

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	jose "gopkg.in/square/go-jose.v2"
)

// existingJWSSigner takes the given JWS and passes it as a signed JWS.
//
// It also passes the given error.
type existingJWSSigner struct {
	jws *jose.JSONWebSignature
	err error
}

func (s *existingJWSSigner) Sign(payload []byte) (*jose.JSONWebSignature, error) {
	return s.jws, s.err
}

// localRoundTripper takes the request in flight
// and passes it directly to the response.Request field.
//
// It also passes the given error.
type localRoundTripper struct{ err error }

func (rt localRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{Request: req}, rt.err
}

func (s *existingJWSSigner) Options() jose.SignerOptions {
	return jose.SignerOptions{}
}

func TestJWTClientAuthenticator(t *testing.T) {
	type checkFunc func(*http.Response, error) error

	checks := func(fs ...checkFunc) checkFunc {
		return checkFunc(func(res *http.Response, err error) error {
			for _, f := range fs {
				if e := f(res, err); e != nil {
					return e
				}
			}
			return nil
		})
	}

	requestHasFormValue := func(key, value string) checkFunc {
		return func(res *http.Response, _ error) error {
			if got := res.Request.Form.Get(key); got != value {
				return fmt.Errorf("expected header key %q value %q, got value %q", key, value, got)
			}
			return nil
		}
	}

	hasError := func(want string) checkFunc {
		return func(_ *http.Response, err error) error {
			if got := err.Error(); got != want {
				return fmt.Errorf("want error %q, got %q", want, got)
			}
			return nil
		}
	}

	someJWS := `eyJhbGciOiJSUzUxMiIsImtpZCI6IkZZSUZUQklQIn0.eyJhdWQiOlsiaHR0cDovL2xvY2FsaG9zdDo4MDgwL2F1dGgvcmVhbG1zL21hc3RlciJdLCJleHAiOjE1Mzg5OTEwMjYsImlhdCI6MTUzODk5MTAxNiwibmJmIjoxNTM4OTkxMDA2LCJzdWIiOiJ0ZWxlbWV0ZXIifQ.kMWp4_PhGF3-IBoPfm4Qr1FCr-Y7uuWpVzpGbLQjgAnn-VsJR9IC1yAmsynvw5HLZGNuRevAkh8NFSi0E8qGqfNW35VkKlwRgYRGDkPjrExqILYSG1Q_yhTKTEgicyi-kxhJ297DCNIX17KWv1Bx6FzL53lYYZ_ohwv2eLQCY89JTEIInAhSZqz60nbKUttFo4vPxZnBimE0w-jac02OHRdYOkkQaQZ_QIuRaLirp3nawDhElWll6aVjcoSlEu0GuXHbyknCnJ4uFe4DsVJNQpNYC5U6VM0OHMK9PlHWwsZ9FvLwMHUtVOSmse-bYoc7ucYoMurO8EwBUfarSNWztw`

	for _, tc := range []struct {
		name         string
		roundtripper http.RoundTripper
		req          *http.Request
		check        checkFunc
	}{
		{
			name: "sign error",

			roundtripper: NewJWTClientAuthenticator(
				ClientClaims{},
				&existingJWSSigner{
					jws: nil,
					err: errors.New("sign error"),
				},
				nil,
			),

			req: httptest.NewRequest("POST", "https://some.oauth.server", nil),

			check: checks(
				hasError("sign error"),
			),
		},
		{
			name: "roundtrip error",

			roundtripper: NewJWTClientAuthenticator(
				ClientClaims{},
				&existingJWSSigner{
					jws: mustParseSigned(someJWS),
					err: nil,
				},
				localRoundTripper{err: errors.New("roundtrip error")},
			),

			req: httptest.NewRequest("POST", "https://some.oauth.server", nil),

			check: checks(
				hasError("roundtrip error"),
			),
		},
		{
			name: "success",

			roundtripper: NewJWTClientAuthenticator(
				ClientClaims{},
				&existingJWSSigner{
					jws: mustParseSigned(someJWS),
					err: nil,
				},
				localRoundTripper{err: nil},
			),

			req: httptest.NewRequest("POST", "https://some.oauth.server", nil),

			check: checks(
				requestHasFormValue(
					"client_assertion_type",
					"urn:ietf:params:oauth:client-assertion-type:jwt-bearer",
				),
				requestHasFormValue(
					"client_assertion",
					someJWS,
				),
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.check(tc.roundtripper.RoundTrip(tc.req)); err != nil {
				t.Error(err)
			}
		})
	}
}

func mustParseSigned(payload string) *jose.JSONWebSignature {
	jws, err := jose.ParseSigned(payload)
	if err != nil {
		panic(err)
	}
	return jws
}
