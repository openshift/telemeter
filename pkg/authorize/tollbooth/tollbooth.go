package tollbooth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"
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

	req, err := http.NewRequest("POST", a.to.String(), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		// read the body to keep the upstream connection open
		if resp.Body != nil {
			io.Copy(ioutil.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return "", errWithCode{error: fmt.Errorf("unauthorized"), code: http.StatusUnauthorized}
	case http.StatusTooManyRequests:
		return "", errWithCode{error: fmt.Errorf("rate limited, please try again later"), code: http.StatusTooManyRequests}
	case http.StatusConflict:
		return "", errWithCode{error: fmt.Errorf("the provided cluster identifier is already in use under a different account or is not sufficiently random"), code: http.StatusConflict}
	case http.StatusOK, http.StatusCreated:
		// allowed
	default:
		tryLogBody(resp.Body, 4*1024, fmt.Sprintf("warning: Upstream server rejected request for cluster %q with body:\n%%s", cluster))
		return "", errWithCode{error: fmt.Errorf("upstream rejected request with code %d", resp.StatusCode), code: http.StatusInternalServerError}
	}
	contentType := resp.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		log.Printf("warning: Upstream server %s responded with an unknown content type %q", a.to, contentType)
		return "", fmt.Errorf("unrecognized token response content-type %q", contentType)
	}

	regResponse, err := tryReadResponse(resp.Body, 32*1024)
	if err != nil {
		log.Printf("warning: Upstream server %s response could not be parsed", a.to)
		return "", fmt.Errorf("unable to parse response body: %v", err)
	}

	if len(regResponse.AccountID) == 0 {
		log.Printf("warning: Upstream server %s responded with an empty user string", a.to)
		return "", fmt.Errorf("server responded with an empty user string")
	}

	return regResponse.AccountID, nil
}

func tryReadResponse(r io.Reader, limitBytes int64) (*clusterRegistration, error) {
	body, err := ioutil.ReadAll(io.LimitReader(r, limitBytes))
	if err != nil {
		return nil, err
	}
	response := &clusterRegistration{}
	if err := json.Unmarshal(body, response); err != nil {
		return nil, err
	}
	return response, nil
}

type errWithCode struct {
	error
	code int
}

func (e errWithCode) HTTPStatusCode() int {
	return e.code
}

func tryLogBody(r io.Reader, limitBytes int64, format string) {
	body, _ := ioutil.ReadAll(io.LimitReader(r, limitBytes))
	log.Printf(format, string(body))
}
