package jwt

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	"github.com/openshift/telemeter/pkg/authorize"
)

type authorizeClusterHandler struct {
	clusterIDKey    string
	labels          map[string]string
	expireInSeconds int64
	signer          *Signer
	clusterAuth     authorize.ClusterAuthorizer
	logger          log.Logger
}

// NewAuthorizerHandler creates an authorizer HTTP endpoint that will authorize the cluster
// given by the "id" form request parameter using the given cluster authorizer.
//
// Upon success, the given cluster authorizer returns a subject which is used as the client identifier
// in a generated signed JWT which is returned to the client, along with any labels.
//
// A single cluster ID key parameter must be passed to uniquely identify the caller's data.
func NewAuthorizeClusterHandler(logger log.Logger, clusterIDKey string, expireInSeconds int64, signer *Signer, labels map[string]string, ca authorize.ClusterAuthorizer) *authorizeClusterHandler {
	return &authorizeClusterHandler{
		clusterIDKey:    clusterIDKey,
		expireInSeconds: expireInSeconds,
		signer:          signer,
		labels:          labels,
		clusterAuth:     ca,
		logger:          log.With(logger, "component", "authorize/jwt"),
	}
}

func (a *authorizeClusterHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		http.Error(w, "Only POST is allowed to this endpoint", http.StatusMethodNotAllowed)
		return
	}

	req.Body = http.MaxBytesReader(w, req.Body, 4*1024)
	defer req.Body.Close()

	if err := req.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	uniqueIDKey := "id"
	clusterID := req.Form.Get(uniqueIDKey)
	if len(clusterID) == 0 {
		http.Error(w, fmt.Sprintf("The '%s' parameter must be specified via URL or url-encoded form body", uniqueIDKey), http.StatusBadRequest)
		return
	}

	auth := strings.SplitN(req.Header.Get("Authorization"), " ", 2)
	if strings.ToLower(auth[0]) != "bearer" {
		http.Error(w, "Only bearer authorization allowed", http.StatusUnauthorized)
		return
	}
	if len(auth) != 2 || len(strings.TrimSpace(auth[1])) == 0 {
		http.Error(w, "Invalid Authorization header", http.StatusUnauthorized)
		return
	}
	clientToken := auth[1]

	subject, err := a.clusterAuth.AuthorizeCluster(clientToken, clusterID)
	if err != nil {
		if scerr, ok := err.(authorize.ErrorWithCode); ok {
			if scerr.HTTPStatusCode() >= http.StatusInternalServerError {
				level.Error(a.logger).Log("msg", "unable to authorize request", "err", scerr)
			}
			if scerr.HTTPStatusCode() == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "300")
			}
			http.Error(w, scerr.Error(), scerr.HTTPStatusCode())
			return
		}

		// always hide errors from the upstream service from the client
		uid := rand.Int63()
		level.Error(a.logger).Log("msg", "unable to authorize request", "uid", uid, "err", err)
		http.Error(w, fmt.Sprintf("Internal server error, requestid=%d", uid), http.StatusInternalServerError)
		return
	}

	labels := map[string]string{
		a.clusterIDKey: clusterID,
	}
	for k, v := range a.labels {
		labels[k] = v
	}

	// create a token that asserts the client and the labels
	authToken, err := a.signer.GenerateToken(Claims(subject, labels, a.expireInSeconds, []string{"telemeter-client"}))
	if err != nil {
		level.Error(a.logger).Log("msg", "unable to generate token", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// write the data back to the client
	data, err := json.Marshal(authorize.TokenResponse{
		Version:          1,
		Token:            authToken,
		ExpiresInSeconds: a.expireInSeconds,
		Labels:           labels,
	})

	if err != nil {
		level.Error(a.logger).Log("msg", "unable to marshal token", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err := w.Write(data); err != nil {
		level.Error(a.logger).Log("msg", "writing auth token failed", "err", err)
	}
}
