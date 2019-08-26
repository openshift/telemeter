package tollbooth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/http"
	"net/url"

	"github.com/openshift/telemeter/pkg/authorize"
)

type clusterRegistration struct {
	ClusterID          string `json:"cluster_id"`
	AuthorizationToken string `json:"authorization_token"`
	AccountID          string `json:"account_id"`
}

type registrationError struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type authorizer struct {
	to     *url.URL
	client *http.Client
}

func NewAuthorizer(c *http.Client, to *url.URL) *authorizer {
	return &authorizer{
		to:     to,
		client: c,
	}
}

func (a *authorizer) AuthorizeCluster(token, cluster string) (string, error) {
	regReq := &clusterRegistration{
		ClusterID:          cluster,
		AuthorizationToken: token,
	}

	data, err := json.Marshal(regReq)
	if err != nil {
		return "", err
	}

	body, err := authorize.AgainstEndpoint(a.client, a.to, bytes.NewReader(data), cluster, func(res *http.Response) error {
		contentType := res.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			log.Printf("warning: Upstream server %s responded with an unknown content type %q", a.to, contentType)
			return fmt.Errorf("unrecognized token response content-type %q", contentType)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	response := &clusterRegistration{}
	if err := json.Unmarshal(body, response); err != nil {
		log.Printf("warning: Upstream server %s response could not be parsed", a.to)
		return "", fmt.Errorf("unable to parse response body: %v", err)
	}

	if len(response.AccountID) == 0 {
		log.Printf("warning: Upstream server %s responded with an empty user string", a.to)
		return "", fmt.Errorf("server responded with an empty user string")
	}

	return response.AccountID, nil
}
