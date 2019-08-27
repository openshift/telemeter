package authorize

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
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

func AgainstEndpoint(client *http.Client, endpoint *url.URL, buf io.Reader, cluster string, validate func(*http.Response) error) ([]byte, error) {
	req, err := http.NewRequest("POST", endpoint.String(), buf)
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
				log.Printf("error copying body: %v", err)
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
		tryLogBody(res.Body, 4*1024, fmt.Sprintf("warning: Upstream server rejected request for cluster %q with body:\n%%s", cluster))
		return body, NewErrorWithCode(fmt.Errorf("upstream rejected request with code %d", res.StatusCode), http.StatusInternalServerError)
	}
}

func tryLogBody(r io.Reader, limitBytes int64, format string) {
	body, _ := ioutil.ReadAll(io.LimitReader(r, limitBytes))
	log.Printf(format, string(body))
}

func NewHandler(client *http.Client, endpoint *url.URL, tenantKey string, next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		authParts := strings.Split(string(authHeader), " ")
		if len(authParts) != 2 || strings.ToLower(authParts[0]) != "bearer" {
			http.Error(w, "bad authorization header", http.StatusBadRequest)
			return
		}

		var tenant string
		if tenantKey != "" {
			token := make(map[string]string)
			if err := json.Unmarshal([]byte(authParts[1]), &token); err != nil {
				log.Printf("failed to read token: %v", err)
				return
			}
			tenant = token[tenantKey]
		}
		if _, err := AgainstEndpoint(client, endpoint, strings.NewReader(authParts[1]), tenant, nil); err != nil {
			log.Printf("unauthorized request made: %v", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), TenantKey, tenant)))
	}
}
