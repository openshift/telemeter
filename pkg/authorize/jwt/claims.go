package jwt

import (
	"time"

	"gopkg.in/square/go-jose.v2/jwt"
)

type telemeter struct {
	Labels map[string]string `json:"labels,omitempty"`
}

type privateClaims struct {
	Telemeter telemeter `json:"telemeter.openshift.io,omitempty"`
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
