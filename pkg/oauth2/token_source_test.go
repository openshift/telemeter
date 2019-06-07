package oauth2

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type headerModifier struct {
	delegate http.RoundTripper
	header   http.Header
}

func (hm *headerModifier) RoundTrip(req *http.Request) (*http.Response, error) {
	r := new(http.Request)
	*r = *req

	h := make(http.Header, len(req.Header))
	for k, vs := range req.Header {
		newvs := make([]string, len(vs))
		copy(newvs, vs)
		h[k] = newvs
	}

	for k, vs := range hm.header {
		h[k] = vs
	}

	r.Header = h
	res, err := hm.delegate.RoundTrip(r)
	hm.header = make(http.Header)
	return res, err
}

func (hm *headerModifier) SetHeader(key, value string) {
	hm.header.Set(key, value)
}

func TestPasswordCredentialsTokenSource(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	conf := newConf(ts.URL)

	type checkFunc func(*oauth2.Token, error) error

	checks := func(fs ...checkFunc) checkFunc {
		return checkFunc(func(token *oauth2.Token, err error) error {
			for _, f := range fs {
				if e := f(token, err); e != nil {
					return e
				}
			}
			return nil
		})
	}

	type givenFunc func() (oauth2.TokenSource, *headerModifier)

	passwordCredentials := func(username, password string) givenFunc {
		return func() (oauth2.TokenSource, *headerModifier) {
			h := make(http.Header)

			tr := &headerModifier{
				delegate: http.DefaultTransport,
				header:   h,
			}

			ctx := context.WithValue(context.Background(), oauth2.HTTPClient,
				&http.Client{
					Timeout:   20 * time.Second,
					Transport: tr,
				},
			)

			src := NewPasswordCredentialsTokenSource(ctx, conf, username, password)
			return src, tr
		}
	}

	type whenFunc func(oauth2.TokenSource, *headerModifier) (*oauth2.Token, error)

	steps := func(whens ...whenFunc) whenFunc {
		return func(src oauth2.TokenSource, m *headerModifier) (tok *oauth2.Token, err error) {
			for _, wf := range whens {
				tok, err = wf(src, m)
				if err != nil {
					return
				}
			}
			return
		}
	}

	expireNextAccessTokenIn := func(expiry int) whenFunc {
		return func(_ oauth2.TokenSource, m *headerModifier) (*oauth2.Token, error) {
			m.SetHeader("Expires-In", strconv.Itoa(expiry))
			return nil, nil
		}
	}

	expireNextRefreshTokenIn := func(expiry int) whenFunc {
		return func(_ oauth2.TokenSource, m *headerModifier) (*oauth2.Token, error) {
			m.SetHeader("Refresh-Expires-In", strconv.Itoa(expiry))
			return nil, nil
		}
	}

	nextRespondWith := func(code int) whenFunc {
		return func(_ oauth2.TokenSource, m *headerModifier) (*oauth2.Token, error) {
			m.SetHeader("Respond-With", strconv.Itoa(code))
			return nil, nil
		}
	}

	getToken := func(src oauth2.TokenSource, m *headerModifier) (*oauth2.Token, error) {
		return src.Token()
	}

	hasAccessToken := func(expected string) checkFunc {
		return checkFunc(func(token *oauth2.Token, _ error) error {
			if got := token.AccessToken; got != expected {
				return fmt.Errorf("want access token %v, got %v", expected, got)
			}
			return nil
		})
	}

	hasRefreshToken := func(expected string) checkFunc {
		return checkFunc(func(token *oauth2.Token, _ error) error {
			if got := token.RefreshToken; got != expected {
				return fmt.Errorf("want refresh token %v, got %v", expected, got)
			}
			return nil
		})
	}

	hasTokenType := func(expected string) checkFunc {
		return checkFunc(func(token *oauth2.Token, _ error) error {
			if got := token.TokenType; got != expected {
				return fmt.Errorf("want token type %v, got %v", expected, got)
			}
			return nil
		})
	}

	isValid := func(expected bool) checkFunc {
		return checkFunc(func(token *oauth2.Token, _ error) error {
			if got := token.Valid(); got != expected {
				return fmt.Errorf("want token valid %t, got %t", expected, got)
			}
			return nil
		})
	}

	hasError := func(expected error) checkFunc {
		return checkFunc(func(_ *oauth2.Token, got error) error {
			if got != expected {
				return fmt.Errorf("expected error %v, got %v", expected, got)
			}
			return nil
		})
	}

	for _, tc := range []struct {
		name  string
		given givenFunc
		when  whenFunc
		check checkFunc
	}{
		{
			name: "initial request",

			given: passwordCredentials("user1", "password1"),

			when: getToken,

			check: checks(
				hasError(nil),
				hasAccessToken("access_token"),
				hasRefreshToken("refresh_token"),
				hasTokenType("bearer"),
				isValid(true),
			),
		},
		{
			name: "reuse access token",

			given: passwordCredentials("user1", "password1"),

			when: steps(
				getToken,
				getToken,
			),

			check: checks(
				hasError(nil),
				hasAccessToken("access_token"),
				hasRefreshToken("refresh_token"),
				hasTokenType("bearer"),
				isValid(true),
			),
		},
		{
			name: "refresh the refresh token",

			given: passwordCredentials("user1", "password1"),

			when: steps(
				// let the first token to be expired immediately
				expireNextRefreshTokenIn(-1),
				getToken,
				// let the second refresh token be not expired
				expireNextRefreshTokenIn(100),
				getToken,
			),

			check: checks(
				hasError(nil),
				hasAccessToken("access_token"),
				hasRefreshToken("refresh_token"),
				hasTokenType("bearer"),
				isValid(true),
			),
		},
		{
			name: "invalidate refresh token session",

			given: passwordCredentials("user1", "password1"),

			when: steps(
				// let the first access token be expired immmediately
				expireNextAccessTokenIn(-1),
				expireNextRefreshTokenIn(100),
				getToken,
				nextRespondWith(http.StatusBadRequest),
				getToken,
			),

			check: checks(
				hasError(nil),
				hasAccessToken("access_token"),
				hasRefreshToken("refresh_token"),
				hasTokenType("bearer"),
				isValid(true),
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.check(tc.when(tc.given())); err != nil {
				t.Error(err)
			}
		})
	}
}

func newConf(url string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "CLIENT_ID",
		ClientSecret: "CLIENT_SECRET",
		RedirectURL:  "REDIRECT_URL",
		Endpoint: oauth2.Endpoint{
			AuthURL:  url + "/auth",
			TokenURL: url + "/token",
		},
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		expected := "/token"
		if r.URL.String() != expected {
			t.Fatalf("URL = %q; want %q", r.URL, expected)
		}
		headerAuth := r.Header.Get("Authorization")
		// CLIENT_ID:CLIENT_SECRET
		expected = "Basic Q0xJRU5UX0lEOkNMSUVOVF9TRUNSRVQ="
		if headerAuth != expected {
			t.Fatalf("Authorization header = %q; want %q", headerAuth, expected)
		}

		if codestr, ok := r.Header["Respond-With"]; ok {
			code, err := strconv.Atoi(codestr[0])
			if err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(code)
			return
		}

		headerContentType := r.Header.Get("Content-Type")
		expected = "application/x-www-form-urlencoded"
		if headerContentType != expected {
			t.Fatalf("Content-Type header = %q; want %q", headerContentType, expected)
		}

		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Failed reading request body: %s.", err)
		}

		expected = "grant_type=password&password=password1&username=user1"
		if string(body) != expected {
			t.Errorf("res.Body = %q; want %q", string(body), expected)
		}
		w.Header().Set("Content-Type", "application/json")

		refreshExpiresIn := "100"
		if _, ok := r.Header["Refresh-Expires-In"]; ok {
			refreshExpiresIn = r.Header.Get("Refresh-Expires-In")
		}

		expiresIn := "100"
		if _, ok := r.Header["Expires-In"]; ok {
			expiresIn = r.Header.Get("Expires-In")
		}

		if _, err := w.Write([]byte(`{
  "access_token": "access_token",
  "expires_in": ` + expiresIn + `,
  "refresh_expires_in": ` + refreshExpiresIn + `,
  "refresh_token": "refresh_token",
  "token_type": "bearer"
}`)); err != nil {
			t.Errorf("error writing token: %v", err)
		}
	}))
}
