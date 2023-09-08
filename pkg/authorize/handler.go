package authorize

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/middleware"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

func NewAuthorizeClientHandler(logger log.Logger, authorizer ClientAuthorizer, next http.Handler) http.Handler {
	logger = log.With(logger, "component", "authorize")

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		logger := log.With(logger, "request", middleware.GetReqID(req.Context()))

		auth := strings.SplitN(req.Header.Get("Authorization"), " ", 2)
		if strings.ToLower(auth[0]) != "bearer" {
			http.Error(w, "Only bearer authorization allowed", http.StatusUnauthorized)
			level.Debug(logger).Log("msg", "Only bearer authorization allowed", "auth", auth[0])
			return
		}
		if len(auth) != 2 || len(strings.TrimSpace(auth[1])) == 0 {
			http.Error(w, "Invalid Authorization header", http.StatusUnauthorized)
			level.Debug(logger).Log("msg", "Invalid Authorization header", "auth", auth)
			return
		}

		client, err := authorizer.AuthorizeClient(auth[1])
		if err != nil {
			http.Error(w, fmt.Sprintf("Not authorized: %v", err), http.StatusUnauthorized)
			level.Warn(logger).Log("msg", "Not authorized", "err", err)
			return
		}

		next.ServeHTTP(w, req.WithContext(WithClient(req.Context(), client)))
	})
}

type errorWithCode struct {
	error
	code int
}

type ErrorWithCode interface {
	error
	HTTPStatusCode() int
}

func NewErrorWithCode(err error, code int) ErrorWithCode {
	return errorWithCode{error: err, code: code}
}

func (e errorWithCode) HTTPStatusCode() int {
	return e.code
}

const requestBodyLimit = 32 * 1024 // 32MiB

func AgainstEndpoint(logger log.Logger, client *http.Client, endpoint *url.URL, token []byte, cluster string, validate func(*http.Response) error) ([]byte, error) {
	logger = log.With(logger, "component", "authorize")
	req, err := http.NewRequest("POST", endpoint.String(), bytes.NewReader(token))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		// read the body to keep the upstream connection open
		if res.Body != nil {
			if _, err := io.Copy(ioutil.Discard, res.Body); err != nil {
				level.Error(logger).Log("msg", "error copying body", "err", err)
			}
			res.Body.Close()
		}
	}()

	body, err := ioutil.ReadAll(io.LimitReader(res.Body, requestBodyLimit))
	if err != nil {
		return nil, err
	}

	if validate != nil {
		if err := validate(res); err != nil {
			return body, err
		}
	}

	switch res.StatusCode {
	case http.StatusUnauthorized:
		return body, NewErrorWithCode(fmt.Errorf("unauthorized"), http.StatusUnauthorized)
	case http.StatusTooManyRequests:
		return body, NewErrorWithCode(fmt.Errorf("rate limited, please try again later"), http.StatusTooManyRequests)
	case http.StatusConflict:
		return body, NewErrorWithCode(fmt.Errorf("the provided cluster identifier is already in use under a different account or is not sufficiently random"), http.StatusConflict)
	case http.StatusNotFound:
		return body, NewErrorWithCode(fmt.Errorf("not found"), http.StatusNotFound)
	case http.StatusOK, http.StatusCreated:
		return body, nil
	default:
		return body, fmt.Errorf("upstream rejected request with code %d and body %q", res.StatusCode, string(body))
	}
}

// NewHandler returns an http.HandlerFunc that is able to authorize requests against Tollbooth.
// The handler function expects a bearer token in the Authorization header consisting of a
// base64-encoded JSON object containing "authorization_token" and "cluster_id" fields.
func NewHandler(logger log.Logger, client *http.Client, endpoint *url.URL, tenantKey string, next http.Handler) http.HandlerFunc {
	logger = log.With(logger, "component", "authorize")

	return func(w http.ResponseWriter, r *http.Request) {
		logger := log.With(logger, "request", middleware.GetReqID(r.Context()))

		authHeader := r.Header.Get("Authorization")
		authParts := strings.Split(string(authHeader), " ")
		if len(authParts) != 2 || strings.ToLower(authParts[0]) != "bearer" {
			level.Warn(logger).Log("msg", "bad authorization header", "header", authHeader)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		token, err := base64.StdEncoding.DecodeString(authParts[1])
		if err != nil {
			level.Warn(logger).Log("msg", "failed to extract token", "err", err, "token", authParts[1])
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var tenant string
		if tenantKey != "" {
			fields := make(map[string]string)
			if err := json.Unmarshal(token, &fields); err != nil {
				level.Warn(logger).Log("msg", "failed to read token", "err", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			tenant = fields[tenantKey]
		}

		logger = log.With(logger, "tenant", tenant)

		if _, err := AgainstEndpoint(logger, client, endpoint, token, tenant, nil); err != nil {
			level.Warn(logger).Log("msg", "unauthorized request made:", "err", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Log the tenant for debugging purposes.
		level.Info(logger).Log("msg", "authorized request")

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), TenantKey, tenant)))
	}
}
