package jwt

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	jose "gopkg.in/square/go-jose.v2"
	"gopkg.in/square/go-jose.v2/jwt"

	"github.com/smarterclayton/telemeter/pkg/authorizer"
)

func NewForKey(audience string, private crypto.PrivateKey, public crypto.PublicKey) (*Signer, *Authorizer, error) {
	return &Signer{
			iss:        "telemeter.selfsigned",
			privateKey: private,
		}, &Authorizer{
			iss:       "telemeter.selfsigned",
			keys:      []interface{}{public},
			validator: NewValidator([]string{audience}),
		}, nil
}

func New(audience string) (*Signer, *Authorizer, *ecdsa.PublicKey, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return &Signer{
			iss:        "telemeter.selfsigned",
			privateKey: key,
		}, &Authorizer{
			iss:       "telemeter.selfsigned",
			keys:      []interface{}{key.Public()},
			validator: NewValidator([]string{audience}),
		}, &key.PublicKey, key, nil
}

type Signer struct {
	iss        string
	privateKey interface{}
}

func (j *Signer) GenerateToken(claims *jwt.Claims, privateClaims interface{}) (string, error) {
	var alg jose.SignatureAlgorithm
	switch privateKey := j.privateKey.(type) {
	case *rsa.PrivateKey:
		alg = jose.RS256
	case *ecdsa.PrivateKey:
		switch privateKey.Curve {
		case elliptic.P256():
			alg = jose.ES256
		case elliptic.P384():
			alg = jose.ES384
		case elliptic.P521():
			alg = jose.ES512
		default:
			return "", fmt.Errorf("unknown private key curve, must be 256, 384, or 521")
		}
	default:
		return "", fmt.Errorf("unknown private key type %T, must be *rsa.PrivateKey or *ecdsa.PrivateKey", j.privateKey)
	}

	signer, err := jose.NewSigner(
		jose.SigningKey{
			Algorithm: alg,
			Key:       j.privateKey,
		},
		nil,
	)
	if err != nil {
		return "", err
	}

	// claims are applied in reverse precedence
	return jwt.Signed(signer).
		Claims(privateClaims).
		Claims(claims).
		Claims(&jwt.Claims{
			Issuer: j.iss,
		}).
		CompactSerialize()
}

// NewAuthorizer authenticates tokens as JWT tokens produced by JWTTokenGenerator
// Token signatures are verified using each of the given public keys until one works (allowing key rotation)
// If lookup is true, the service account and secret referenced as claims inside the token are retrieved and verified with the provided ServiceAccountTokenGetter
func NewAuthorizer(iss string, keys []interface{}, validator Validator) *Authorizer {
	return &Authorizer{
		iss:       iss,
		keys:      keys,
		validator: validator,
	}
}

type Authorizer struct {
	iss       string
	keys      []interface{}
	validator Validator
}

// Validator is called by the JWT token authentictaor to apply domain specific
// validation to a token and extract user information.
type Validator interface {
	// Validate validates a token and returns user information or an error.
	// Validator can assume that the issuer and signature of a token are already
	// verified when this function is called.
	Validate(tokenData string, public *jwt.Claims, private interface{}) (*authorizer.User, error)
	// NewPrivateClaims returns a struct that the authenticator should
	// deserialize the JWT payload into. The authenticator may then pass this
	// struct back to the Validator as the 'private' argument to a Validate()
	// call. This struct should contain fields for any private claims that the
	// Validator requires to validate the JWT.
	NewPrivateClaims() interface{}
}

var errMismatchedSigningMethod = errors.New("invalid signing method")

func (j *Authorizer) AuthorizeToken(tokenData string) (*authorizer.User, bool, error) {
	if !j.hasCorrectIssuer(tokenData) {
		return nil, false, nil
	}

	tok, err := jwt.ParseSigned(tokenData)
	if err != nil {
		return nil, false, nil
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
		return nil, false, multipleErrors(errs)
	}

	// If we get here, we have a token with a recognized signature and
	// issuer string.
	user, err := j.validator.Validate(tokenData, public, private)
	if err != nil {
		return nil, false, err
	}

	return user, true, nil
}

func multipleErrors(errs []error) error {
	if len(errs) > 1 {
		return listErr(errs)
	}
	if len(errs) == 0 {
		return nil
	}
	return errs[0]
}

type listErr []error

func (errs listErr) Error() string {
	var messages []string
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return "multiple errors: " + strings.Join(messages, ", ")
}

// hasCorrectIssuer returns true if tokenData is a valid JWT in compact
// serialization format and the "iss" claim matches the iss field of this token
// authenticator, and otherwise returns false.
//
// Note: go-jose currently does not allow access to unverified JWS payloads.
// See https://github.com/square/go-jose/issues/169
func (j *Authorizer) hasCorrectIssuer(tokenData string) bool {
	parts := strings.SplitN(tokenData, ".", 4)
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	claims := struct {
		// WARNING: this JWT is not verified. Do not trust these claims.
		Issuer string `json:"iss"`
	}{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false
	}
	if claims.Issuer != j.iss {
		return false
	}
	return true

}

type privateClaims struct {
	Telemeter telemeter `json:"telemeter.openshift.io,omitempty"`
}

type telemeter struct {
	Labels map[string]string `json:"labels,omitempty"`
}

func now() time.Time {
	return time.Now()
}

func Claims(subject string, labels map[string]string, expirationSeconds int64, audience []string) (*jwt.Claims, interface{}) {
	now := now()
	sc := &jwt.Claims{
		Subject:   subject,
		Audience:  jwt.Audience(audience),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Expiry:    jwt.NewNumericDate(now.Add(time.Duration(expirationSeconds) * time.Second)),
	}
	pc := &privateClaims{
		Telemeter: telemeter{
			Labels: labels,
		},
	}
	return sc, pc
}

func NewValidator(audiences []string) Validator {
	return &validator{
		auds: audiences,
	}
}

type validator struct {
	auds []string
}

var _ = Validator(&validator{})

func (v *validator) Validate(_ string, public *jwt.Claims, privateObj interface{}) (*authorizer.User, error) {
	private, ok := privateObj.(*privateClaims)
	if !ok {
		log.Printf("jwt validator expected private claim of type *privateClaims but got: %T", privateObj)
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
		log.Printf("unexpected validation error: %T", err)
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

	return &authorizer.User{
		ID:     public.Subject,
		Labels: private.Telemeter.Labels,
	}, nil
}

func (v *validator) NewPrivateClaims() interface{} {
	return &privateClaims{}
}
