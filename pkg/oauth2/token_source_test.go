package oauth2

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"
)

func TestPasswordCredentialsTokenSource(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	conf := newConf(ts.URL)
	src := NewPasswordCredentialsTokenSource(context.Background(), conf, "user1", "password1")

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
		init         func(*passwordCredentialsTokenSource)
		name         string
		check        checkFunc
		refreshToken *oauth2.Token
	}{
		{
			name: "initial request",
			check: checks(
				hasError(nil),
				hasAccessToken("access_token_1"),
				hasRefreshToken("refresh_token_1"),
				hasTokenType("bearer"),
				isValid(true),
			),
		},
		{
			name: "reuse access token",
			check: checks(
				hasError(nil),
				hasAccessToken("access_token_1"),
				hasRefreshToken("refresh_token_1"),
				hasTokenType("bearer"),
				isValid(true),
			),
		},
		{
			name: "refresh the refresh token",

			// invalidate current refresh token
			init: func(src *passwordCredentialsTokenSource) { src.refreshToken = nil },

			check: checks(
				hasError(nil),
				hasAccessToken("access_token_2"),
				hasRefreshToken("refresh_token_2"),
				hasTokenType("bearer"),
				isValid(true),
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.init != nil {
				tc.init(src)
			}
			if err := tc.check(src.Token()); err != nil {
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
	var counter uint64

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		expected := "/token"
		if r.URL.String() != expected {
			t.Errorf("URL = %q; want %q", r.URL, expected)
		}
		headerAuth := r.Header.Get("Authorization")
		expected = "Basic Q0xJRU5UX0lEOkNMSUVOVF9TRUNSRVQ="
		if headerAuth != expected {
			t.Errorf("Authorization header = %q; want %q", headerAuth, expected)
		}
		headerContentType := r.Header.Get("Content-Type")
		expected = "application/x-www-form-urlencoded"
		if headerContentType != expected {
			t.Errorf("Content-Type header = %q; want %q", headerContentType, expected)
		}
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Errorf("Failed reading request body: %s.", err)
		}
		expected = "grant_type=password&password=password1&username=user1"
		if string(body) != expected {
			t.Errorf("res.Body = %q; want %q", string(body), expected)
		}
		w.Header().Set("Content-Type", "application/json")

		cnt := int(atomic.AddUint64(&counter, 1))

		w.Write([]byte(`{
  "access_token": "access_token_` + strconv.Itoa(cnt) + `",
  "expires_in": ` + strconv.Itoa(cnt) + `00,
  "refresh_expires_in": ` + strconv.Itoa(cnt) + `00,
  "refresh_token": "refresh_token_` + strconv.Itoa(cnt) + `",
  "token_type": "bearer"
}`))
	}))
}
