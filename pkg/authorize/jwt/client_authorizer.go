package jwt

import (
	"crypto"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/openshift/telemeter/pkg/authorize"
)

// NewClientAuthorizer authenticates tokens as JWT tokens produced by JWTTokenGenerator
// Token signatures are verified using each of the given public keys until one works (allowing key rotation)
// If lookup is true, the service account and secret referenced as claims inside the token are retrieved and verified with the provided ServiceAccountTokenGetter
func NewClientAuthorizer(issuer string, keys []crypto.PublicKey, v Validator) *clientAuthorizer {
	return &clientAuthorizer{
		iss:       issuer,
		keys:      keys,
		validator: v,
	}
}

type clientAuthorizer struct {
	iss       string
	keys      []crypto.PublicKey
	validator Validator
}

func (j *clientAuthorizer) AuthorizeClient(tokenData string) (*authorize.Client, error) {
	if err := j.hasCorrectIssuer(tokenData); err != nil {
		return nil, err
	}

	tok, err := jwt.ParseSigned(tokenData)
	if err != nil {
		return nil, err
	}

	public := &jwt.Claims{}
	private := j.validator.NewPrivateClaims()

	var (
		found bool
		errs  []error
	)
	for _, key := range j.keys {
		if err := tok.Claims(key, public, private); err != nil {
			errs = append(errs, err)
			continue
		}
		found = true
		break
	}

	if !found {
		return nil, multipleErrors(errs)
	}

	// If we get here, we have a token with a recognized signature and
	// issuer string.
	client, err := j.validator.Validate(tokenData, public, private)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// hasCorrectIssuer returns true if tokenData is a valid JWT in compact
// serialization format and the "iss" claim matches the iss field of this token
// authenticator, and otherwise returns false.
//
// Note: go-jose currently does not allow access to unverified JWS payloads.
// See https://github.com/square/go-jose/issues/169
func (j *clientAuthorizer) hasCorrectIssuer(tokenData string) error {
	parts := strings.SplitN(tokenData, ".", 4)
	if len(parts) != 3 {
		return fmt.Errorf("invalid JWT token format, expected 3 parts, got %d", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("invalid JWT payload, expected base64")
	}

	claims := struct {
		// WARNING: this JWT is not verified. Do not trust these claims.
		Issuer string `json:"iss"`
	}{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return fmt.Errorf("invalid JWT payload, expected JSON object")
	}

	if claims.Issuer != j.iss {
		return fmt.Errorf("invalid JWT issuer, expected %q, got %q", j.iss, claims.Issuer)
	}

	return nil
}
