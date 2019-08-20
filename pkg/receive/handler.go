package receive

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"
)

type clusterIDKey string

const forwardTimeout = 5 * time.Second
const clusterID clusterIDKey = "tenant"

// ClusterAuthorizer authorizes a cluster by its token and id, returning a subject or error
type ClusterAuthorizer interface {
	AuthorizeCluster(token, cluster string) (subject string, err error)
}

// Handler knows the forwardURL for all requests
type Handler struct {
	ForwardURL string
	client     *http.Client
}

// NewHandler returns a new Handler with a http client
func NewHandler(forwardURL string) *Handler {
	return &Handler{
		ForwardURL: forwardURL,
		client: &http.Client{
			Timeout: forwardTimeout,
		},
	}
}

// Receive a remote-write request after it has been authenticated and forward it to Thanos
func (h *Handler) Receive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	defer r.Body.Close()

	ctx, cancel := context.WithTimeout(r.Context(), forwardTimeout)
	defer cancel()

	req, err := http.NewRequest(http.MethodPost, h.ForwardURL, r.Body)
	if err != nil {
		log.Printf("failed to create forward request: %v\n", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req = req.WithContext(ctx)
	req.Header.Add("THANOS-TENANT", r.Context().Value(clusterID).(string))

	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("failed to foward request: %v\n", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if resp.StatusCode/100 != 2 {
		log.Printf("response status code is %s\n", resp.Status)
		http.Error(w, "upstream response status is not 200 OK", http.StatusBadGateway)
		return
	}
}

type AuthorizationPayload struct {
	Cluster string `json:"cluster"`
	Token   string `json:"token"`
}

// Authorizer is a middlware that uses a ClusterAuthorizer implementation to auth an incoming remote-write request.
func (h *Handler) Authorizer(authorizer ClusterAuthorizer, next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		authParts := strings.Split(string(authHeader), " ")
		if len(authParts) != 2 || strings.ToLower(authParts[0]) != "bearer" {
			http.Error(w, "bad authorization header", http.StatusBadRequest)
			return
		}

		authHeaderDecoded, err := ioutil.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewBufferString(authParts[1])))
		if err != nil {
			http.Error(w, "failed base64 decoding authorization bearer", http.StatusBadRequest)
			return
		}

		var authPayload AuthorizationPayload
		if err := json.Unmarshal([]byte(authHeaderDecoded), &authPayload); err != nil {
			http.Error(w, "failed to unmarshal authorization bearer", http.StatusBadRequest)
			return
		}

		_, err = authorizer.AuthorizeCluster(authPayload.Token, authPayload.Cluster)
		if err != nil {
			log.Printf("unauthorized request made: %v", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		r = r.WithContext(context.WithValue(r.Context(), clusterID, authPayload.Cluster))

		next.ServeHTTP(w, r)
	}
}
