package receive

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/openshift/telemeter/pkg/authorize"
)

const forwardTimeout = 5 * time.Second

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
	req.Header.Add("THANOS-TENANT", r.Context().Value(authorize.TenantKey).(string))

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
