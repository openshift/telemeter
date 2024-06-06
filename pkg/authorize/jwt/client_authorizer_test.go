package jwt_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openshift/telemeter/pkg/authorize/jwt"
	josejwt "gopkg.in/square/go-jose.v2/jwt"
)

// ECDSA P-256 private key
// Generated with:
// openssl ecparam -name prime256v1 -genkey -noout -out private.pem
const privateKeyStr = `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIJjB5iUXX59ZvVCh6HU+SilUbO/HLt1bA8XCRuj/6AttoAoGCCqGSM49
AwEHoUQDQgAEO+kxbrjv6htr6GogRhU42i+gKBPes5uQ6V9gXEVxrk31HFzbH9rS
qpNJg76IHeESzsZhL4y6N1AcU0w69Sq/UQ==
-----END EC PRIVATE KEY-----`

const privateKeyStrAlt = `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIMlm+7txiCr/bV+7K7NADY1UxP/qwe3fRO/ACNvr/86voAoGCCqGSM49
AwEHoUQDQgAEHVPmS2HrAu6JFzNCNOTGUQiUQDsTxGUMR9zu1/5GBax5NUZK7316
8NNIScJ40AAKcGh6jw3NphD9WBWfzaZw+g==
-----END EC PRIVATE KEY-----`

func TestClientAuthorizer_AuthorizeClient(t *testing.T) {
	privateKey := parseKey(t, privateKeyStr)
	publicKey := privateKey.PublicKey
	validIssuer := "test-issuer"
	authorizer := jwt.NewClientAuthorizer(validIssuer, []crypto.PublicKey{&publicKey}, jwt.NewValidator(nil, []string{"audience"}))

	// Test with valid token
	token := generateToken(t, privateKey, validIssuer)
	client, err := authorizer.AuthorizeClient(token)
	if err != nil {
		t.Fatalf("error authorizing client: %v", err)
	}
	if client == nil {
		t.Fatalf("client is nil")
	}

	// Test with invalid token
	privateKeyAlt := parseKey(t, privateKeyStrAlt)
	token = generateToken(t, privateKeyAlt, validIssuer)
	_, err = authorizer.AuthorizeClient(token)
	if err == nil {
		t.Fatalf("token was authorized with invalid signature")
	}

	// Test with invalid issuer
	token = generateToken(t, privateKey, "invalid-issuer")
	_, err = authorizer.AuthorizeClient(token)
	if err == nil {
		t.Fatalf("token was authorized with invalid issuer")
	}

	// Test with invalid issuer
	token = generateToken(t, privateKey, "invalid-issuer")
	tokenParts := strings.Split(token, ".")
	fakeiss := base64.URLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"iss":"%s"}`, validIssuer)))
	forgedJWSToken := fmt.Sprintf(`{"fakeiss":".%s.","protected":%q,"payload":%q,"signature":%q}`, fakeiss, tokenParts[0], tokenParts[1], tokenParts[2])

	_, err = authorizer.AuthorizeClient(forgedJWSToken)
	if err == nil {
		t.Fatalf("token was authorized with invalid issuer")
	}
}

func generateToken(t *testing.T, privateKey *ecdsa.PrivateKey, issuer string) string {
	signer := jwt.NewSigner(issuer, privateKey)
	token, err := signer.GenerateToken(&josejwt.Claims{
		Subject:  "test-sub",
		Audience: []string{"audience"},
		Expiry:   josejwt.NewNumericDate(time.Now().Add(josejwt.DefaultLeeway)),
	}, struct{}{})
	if err != nil {
		t.Fatalf("error generating token: %v", err)
	}

	return token
}

func parseKey(t *testing.T, keyStr string) *ecdsa.PrivateKey {
	block, _ := pem.Decode([]byte(keyStr))
	if block == nil {
		t.Fatal("failed to decode PEM block containing the key")
	}

	privateKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("error parsing private key: %v", err)
	}

	return privateKey
}
