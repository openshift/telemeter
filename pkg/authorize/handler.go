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

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
)

func NewAuthorizeClientHandler(authorizer ClientAuthorizer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		auth := strings.SplitN(req.Header.Get("Authorization"), " ", 2)
		if strings.ToLower(auth[0]) != "bearer" {
			http.Error(w, "Only bearer authorization allowed", http.StatusUnauthorized)
			return
		}
		if len(auth) != 2 || len(strings.TrimSpace(auth[1])) == 0 {
			http.Error(w, "Invalid Authorization header", http.StatusUnauthorized)
			return
		}

		client, ok, err := authorizer.AuthorizeClient(auth[1])
		if err != nil {
			http.Error(w, fmt.Sprintf("Not authorized: %v", err), http.StatusUnauthorized)
			return
		}
		if !ok {
			http.Error(w, "Not authorized", http.StatusUnauthorized)
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
		level.Warn(logger).Log("msg", "upstream server rejected request", "cluster", cluster, "body", string(body))
		return body, NewErrorWithCode(fmt.Errorf("upstream rejected request with code %d", res.StatusCode), http.StatusInternalServerError)
	}
}

// NewHandler returns an http.HandlerFunc that is able to authorize requests against Tollbooth.
// The handler function expects a bearer token in the Authorization header consisting of a
// base64-encoded JSON object containing "authorization_token" and "cluster_id" fields.
func NewHandler(logger log.Logger, client *http.Client, endpoint *url.URL, tenantKey string, next http.Handler) http.HandlerFunc {
	logger = log.With(logger, "component", "authorize")
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		authParts := strings.Split(string(authHeader), " ")
		if len(authParts) != 2 || strings.ToLower(authParts[0]) != "bearer" {
			level.Warn(logger).Log("msg", "bad authorization header")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		token, err := base64.StdEncoding.DecodeString(authParts[1])
		if err != nil {
			level.Warn(logger).Log("msg", "failed to extract token", "err", err)
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
		if _, err := AgainstEndpoint(logger, client, endpoint, token, tenant, nil); err != nil {
			level.Warn(logger).Log("msg", "unauthorized request made:", "err", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), TenantKey, tenant)))
	}
}
