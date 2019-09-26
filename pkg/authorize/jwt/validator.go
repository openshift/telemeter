package jwt

import (
	"errors"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	"github.com/openshift/telemeter/pkg/authorize"

	"gopkg.in/square/go-jose.v2/jwt"
)

// Validator is called by the JWT token authentictaor to apply domain specific
// validation to a token and extract user information.
type Validator interface {
	// Validate validates a token and returns user information or an error.
	// Validator can assume that the issuer and signature of a token are already
	// verified when this function is called.
	Validate(tokenData string, public *jwt.Claims, private interface{}) (*authorize.Client, error)
	// NewPrivateClaims returns a struct that the authenticator should
	// deserialize the JWT payload into. The authenticator may then pass this
	// struct back to the Validator as the 'private' argument to a Validate()
	// call. This struct should contain fields for any private claims that the
	// Validator requires to validate the JWT.
	NewPrivateClaims() interface{}
}

func NewValidator(logger log.Logger, audiences []string) Validator {
	return &validator{
		auds:   audiences,
		logger: log.With(logger, "component", "authorize/jwt"),
	}
}

type validator struct {
	auds   []string
	logger log.Logger
}

var _ = Validator(&validator{})

func (v *validator) Validate(_ string, public *jwt.Claims, privateObj interface{}) (*authorize.Client, error) {
	private, ok := privateObj.(*privateClaims)
	if !ok {
		level.Info(v.logger).Log("msg", "jwt validator expected private claim of type *privateClaims", "got", privateObj)
		return nil, errors.New("token could not be validated")
	}
	err := public.Validate(jwt.Expected{
		Time: now(),
	})
	switch {
	case err == nil:
	case err == jwt.ErrExpired:
		return nil, errors.New("token has expired")
	default:
		level.Info(v.logger).Log("msg", "unexpected validation", "err", err)
		return nil, errors.New("token could not be validated")
	}

	var audValid bool

	for _, aud := range v.auds {
		audValid = public.Audience.Contains(aud)
		if audValid {
			break
		}
	}

	if !audValid {
		return nil, errors.New("token is invalid for this audience")
	}

	return &authorize.Client{
		ID:     public.Subject,
		Labels: private.Telemeter.Labels,
	}, nil
}

func (v *validator) NewPrivateClaims() interface{} {
	return &privateClaims{}
}
