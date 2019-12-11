package tollbooth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mime"
	"net/http"
	"net/url"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/pkg/errors"

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
	logger log.Logger
}

func NewAuthorizer(logger log.Logger, c *http.Client, to *url.URL) *authorizer {
	return &authorizer{
		to:     to,
		client: c,
		logger: log.With(logger, "component", "authorize/toolbooth"),
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

	body, err := authorize.AgainstEndpoint(a.logger, a.client, a.to, data, cluster, func(res *http.Response) error {
		contentType := res.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			level.Warn(a.logger).Log("msg", "upstream server responded with an unknown content type", "to", a.to, "contenttype", contentType)
			return fmt.Errorf("unrecognized token response content-type %q", contentType)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	response := &clusterRegistration{}
	if err := json.Unmarshal(body, response); err != nil {
		level.Warn(a.logger).Log("msg", "upstream server response could not be parsed", "to", a.to)
		return "", fmt.Errorf("unable to parse response body: %v", err)
	}

	if len(response.AccountID) == 0 {
		level.Warn(a.logger).Log("msg", "upstream server responded with an empty user string", "to", a.to)
		return "", fmt.Errorf("server responded with an empty user string")
	}

	return response.AccountID, nil
}

// ExtractToken extracts the token from an auth request.
// In the case of a request to Tollbooth, the token
// is the entire contents of the request body.
func ExtractToken(r *http.Request) (string, error) {
	body, err := ioutil.ReadAll(r.Body)
	if err := r.Body.Close(); err != nil {
		return "", errors.Wrap(err, "failed to close body")
	}
	r.Body = ioutil.NopCloser(bytes.NewBuffer(body))
	return string(body), err
}
