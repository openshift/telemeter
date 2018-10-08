package oauth2

import (
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	jose "gopkg.in/square/go-jose.v2"
	"gopkg.in/square/go-jose.v2/jwt"
)

// ClientClaims is the struct that represents the JWT claims
// according to https://tools.ietf.org/html/rfc7523#section-3.
type ClientClaims struct {
	Issuer   string
	Subject  string
	Audience []string
	Expiry   time.Duration
}

type jwtClientAuthenticator struct {
	claims ClientClaims
	signer jose.Signer
	expiry time.Duration
	next   http.RoundTripper

	now func() time.Time
}

// NewJWTClientAuthenticator returns a http.RoundTripper
// that modifies the request in flight
// to conform to a client authentication request
// according to https://tools.ietf.org/html/rfc7523#section-2.2.
//
// A client assertion is created using the given claims
// and signed using the given signer.
//
// Note that this does not implement an authorization grant,
// it only authenticates an oauth2 client.
//
// This implementation was tested and is being used in scenarios
// where the client is acting on behalf of itself
// as described in https://tools.ietf.org/html/rfc7521#section-6.2.
//
// In that scenario this authenticator can be used
// in conjunction with the oauth2.clientcredentials package.
//
// It is safe for concurrent use.
func NewJWTClientAuthenticator(
	claims ClientClaims,
	signer jose.Signer,
	next http.RoundTripper,
) *jwtClientAuthenticator {
	return &jwtClientAuthenticator{
		claims: claims,
		signer: signer,
		next:   next,
		now:    time.Now,
	}
}

func (rt *jwtClientAuthenticator) RoundTrip(req *http.Request) (*http.Response, error) {
	now := rt.now()

	clientAuthClaims := jwt.Claims{
		Issuer:    rt.claims.Issuer,
		Subject:   rt.claims.Subject,
		Audience:  rt.claims.Audience,
		Expiry:    jwt.NewNumericDate(now.Add(rt.claims.Expiry)),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now.Add(-10 * time.Second)),
	}

	clientAuthJWT, err := jwt.Signed(rt.signer).Claims(clientAuthClaims).CompactSerialize()
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Del("Authorization") // replaced with client assertion

	if err := req.ParseForm(); err != nil {
		return nil, err
	}

	req.Form.Set("client_assertion_type", "urn:ietf:params:oauth:client-assertion-type:jwt-bearer")
	req.Form.Set("client_assertion", clientAuthJWT)

	newBody := req.Form.Encode()
	req.Body = ioutil.NopCloser(strings.NewReader(newBody))
	req.ContentLength = int64(len(newBody))

	return rt.next.RoundTrip(req)
}
