package ssl

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-kit/log"
)

// ClientCertConfig allows middleware to extract client information from the request
type ClientCertConfig struct {
	// Secret is used to validate the pre-shared key in clientCertInfo.SecretHeader
	Secret string `json:"secret,omitempty"`
	// Config holds the configuration that tells the server how to extract the client information from the request
	Config ClientCertInfo `json:"config,omitempty"`
}

// ClientCertInfo holds the configuration that tells the server how to extract the client information from the request
type ClientCertInfo struct {
	// SecretHeader is the header that holds the pre-shared key
	SecretHeader string `json:"secret_header,omitempty"`
	// CommonNameHeader is the header that holds the common name extracted from the client certificate
	CommonNameHeader string `json:"common_name_header,omitempty"`
	// IssuerHeader is the header that holds the issuer extracted from the client certificate
	IssuerHeader string `json:"issuer_header,omitempty"`
}

// Validate validates the configuration
func (conf ClientCertConfig) Validate() error {
	if conf.Config.SecretHeader == "" {
		return errors.New("secret_header must be set")
	}
	if conf.Secret == "" {
		return errors.New("secret must be set")
	}
	return nil
}

// ClientCertInfoAsHeaders is middleware that extracts client information from the request and validates
// the pre-shared key
func ClientCertInfoAsHeaders(config ClientCertConfig, logger log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			secret := r.Header.Get(config.Config.SecretHeader)
			if secret == "" {
				http.Error(w, fmt.Sprintf("secret must be sent in request header %s",
					config.Config.SecretHeader), http.StatusForbidden)
				return
			}
			if secret != config.Secret {
				http.Error(w, "invalid secret", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		}

		return http.HandlerFunc(fn)
	}
}
