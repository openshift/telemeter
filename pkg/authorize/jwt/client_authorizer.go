package jwt

import (
	"crypto"
	"fmt"

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

	if public.Issuer != j.iss {
		return nil, fmt.Errorf("invalid JWT issuer, expected %q, got %q", j.iss, public.Issuer)
	}

	// If we get here, we have a token with a recognized signature and
	// issuer string.
	client, err := j.validator.Validate(tokenData, public, private)
	if err != nil {
		return nil, err
	}

	return client, nil
}
