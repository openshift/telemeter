package ssl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

type OrganizationContextKey struct{}
type CommonNameContextKey struct{}

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
	if conf.Config.CommonNameHeader == "" {
		return errors.New("common_name_header must be set")
	}
	return nil
}

// ClientCertInfoAsHeaders is middleware validates the pre-shared key and
// extracts client information from the request and adds it to the request context
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

			cnInfo := r.Header.Get(config.Config.CommonNameHeader)
			if cnInfo == "" {
				level.Info(logger).Log("msg", "invalid format for O and CN", "cnInfo", cnInfo)
				http.Error(w, fmt.Sprintf("subject is empty. Organisation and Common Name name must be sent in request header %s",
					config.Config.CommonNameHeader), http.StatusForbidden)
				return
			}

			// cnInfo must be provided in the format of "/O = xyz, /CN = 123
			parts := strings.Split(cnInfo, ",")
			if len(parts) != 2 {
				level.Info(logger).Log("msg", "invalid format for organisation and CN", "cnInfo", cnInfo)
				http.Error(w, fmt.Sprintf("invalid format for Organisation and Common Name in http header %s",
					config.Config.CommonNameHeader), http.StatusForbidden)
				return
			}

			ctx := r.Context()
			for i, part := range parts {
				subPart := strings.Split(part, "=")
				if len(subPart) != 2 {
					level.Info(logger).Log("msg", "invalid format for O and CN", "cnInfo", cnInfo)
					http.Error(w, fmt.Sprintf("invalid format for Organisation and Common Name in http header %s",
						config.Config.CommonNameHeader), http.StatusForbidden)
					return
				}
				switch i {
				case 0:
					if strings.TrimSpace(subPart[0]) != "/O" {
						level.Info(logger).Log("msg", "invalid format for organisation and CN", "cnInfo", cnInfo)
						http.Error(w, fmt.Sprintf("invalid format for Organisation and Common Name in http header %s",
							config.Config.CommonNameHeader), http.StatusForbidden)
						return

					}
					ctx = context.WithValue(ctx, OrganizationContextKey{}, strings.TrimSpace(subPart[1]))

				case 1:
					if strings.TrimSpace(subPart[0]) != "/CN" {
						level.Info(logger).Log("msg", "invalid format for organisation and CN", "cnInfo", cnInfo)
						http.Error(w, fmt.Sprintf("invalid format for Organisation and Common Name in http header %s",
							config.Config.CommonNameHeader), http.StatusForbidden)
						return
					}
					ctx = context.WithValue(ctx, CommonNameContextKey{}, strings.TrimSpace(subPart[1]))
				}

			}

			next.ServeHTTP(w, r.WithContext(ctx))
		}

		return http.HandlerFunc(fn)
	}
}
