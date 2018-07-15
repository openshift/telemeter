package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/openshift/telemeter/pkg/authorizer/jwt"
	"github.com/openshift/telemeter/pkg/authorizer/remote"
)

type Authorizer struct {
	partitionKey string
	labels       map[string]string

	to     *url.URL
	client *http.Client

	expireInSeconds int64
	signer          *jwt.Signer
}

// New creates an authorizer HTTP endpoint that will invoke the remote URL with the user's provided authorization
// credentials and parse the TokenResponse that endpoint returns. The user identifier and the labels the upstream
// provides will become part of a signed JWT returned to the client, along with the labels. If to is nil a special
// debug loopback mode will be enabled that takes the incoming token and hashes it and returns the current label
// set. A single partition key parameter must be passed to uniquely identify the caller's data.
func New(partitionKey string, to *url.URL, client *http.Client, expireInSeconds int64, signer *jwt.Signer, labels map[string]string) *Authorizer {
	return &Authorizer{
		partitionKey:    partitionKey,
		to:              to,
		client:          client,
		expireInSeconds: expireInSeconds,
		signer:          signer,
		labels:          labels,
	}
}

func (a *Authorizer) AuthorizeHTTP(w http.ResponseWriter, req *http.Request) {
	log.Printf("Performing authorization check")

	if req.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	req.Body = http.MaxBytesReader(w, req.Body, 4*1024)
	defer req.Body.Close()

	if err := req.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cluster := req.Form.Get(a.partitionKey)
	if len(cluster) == 0 {
		http.Error(w, fmt.Sprintf("The '%s' parameter must be specified via URL or url-encoded form body", a.partitionKey), http.StatusBadRequest)
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
	userToken := auth[1]

	var (
		resp *TokenResponse
		err  error
	)
	if a.to == nil {
		// skip authentication upstream and reflect token
		resp, err = a.authorizeStub(userToken, cluster)
	} else {
		// rate limit based on the incoming token
		// exchange a bearer token via a remote call to an upstream
		resp, err = a.authorizeRemote(userToken, cluster)
	}
	if err != nil {
		if code, ok := err.(errWithCode); ok {
			log.Printf("error: unable to authorize request: %v", err)
			if code.code == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "300")
			}
			http.Error(w, err.Error(), code.code)
			return
		}
		// always hide errors from the upstream service from the client
		uid := rand.Int63()
		log.Printf("error: unable to authorize request %d: %v", uid, err)
		http.Error(w, fmt.Sprintf("Internal server error, requestid=%d", uid), http.StatusInternalServerError)
		return
	}

	// ensure labels are consistent
	labels := resp.Labels
	if labels == nil {
		labels = make(map[string]string)
		resp.Labels = labels
	}
	for k, v := range a.labels {
		labels[k] = v
	}
	labels[a.partitionKey] = cluster

	// create a token that asserts the user and the labels
	authToken, err := a.signer.GenerateToken(jwt.Claims(resp.AccountID, resp.Labels, a.expireInSeconds, []string{"federate"}))
	if err != nil {
		log.Printf("error: unable to generate token: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// write the data back to the client
	data, err := json.Marshal(remote.TokenResponse{
		Version:          1,
		Token:            authToken,
		ExpiresInSeconds: a.expireInSeconds,
		Labels:           resp.Labels,
	})
	if err != nil {
		log.Printf("error: unable to marshal token: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(data)
}

func (a *Authorizer) authorizeStub(token, cluster string) (*TokenResponse, error) {
	user := fnvHash(token)
	log.Printf("warning: Performing no-op authentication, user will be %s with cluster %s", user, cluster)

	return &TokenResponse{
		APIVersion: "v1",
		AccountID:  user,
	}, nil
}

func (a *Authorizer) authorizeRemote(token, cluster string) (*TokenResponse, error) {
	tokenRequest := &TokenRequest{
		APIVersion:         "v1",
		AuthorizationToken: token,
		ClusterID:          cluster,
	}
	data, err := json.Marshal(tokenRequest)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", a.to.String(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
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
		return nil, errWithCode{error: fmt.Errorf("Unauthorized"), code: http.StatusUnauthorized}
	case http.StatusTooManyRequests:
		return nil, errWithCode{error: fmt.Errorf("Rate limited, please try again later"), code: http.StatusTooManyRequests}
	case http.StatusConflict:
		return nil, errWithCode{error: fmt.Errorf("The provided cluster identifier is already in use under a different account or is not sufficiently random."), code: http.StatusConflict}
	case http.StatusOK, http.StatusCreated:
		// allowed
	default:
		tryLogBody(resp.Body, 4*1024, "warning: Upstream server rejected with body:\n%s")
		return nil, errWithCode{error: fmt.Errorf("Upstream rejected request with code %d", resp.StatusCode), code: http.StatusInternalServerError}
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "application/json" {
		log.Printf("warning: Upstream server %s responded with an unknown content type %q", a.to, contentType)
		return nil, fmt.Errorf("unrecognized token response content-type %q", contentType)
	}

	tokenResponse, err := tryReadResponse(resp.Body, 32*1024)
	if err != nil {
		log.Printf("warning: Upstream server %s response could not be parsed", a.to)
		return nil, fmt.Errorf("unable to parse response body: %v", err)
	}

	if tokenResponse.APIVersion != "v1" {
		log.Printf("warning: Upstream server %s responded with an unknown schema version %q", a.to, tokenResponse.APIVersion)
		return nil, fmt.Errorf("unrecognized token response version %q", tokenResponse.APIVersion)
	}
	if len(tokenResponse.AccountID) == 0 {
		log.Printf("warning: Upstream server %s responded with an empty user string", a.to)
		return nil, fmt.Errorf("server responded with an empty user string")
	}

	return tokenResponse, nil
}

func tryLogBody(r io.Reader, limitBytes int64, format string) {
	body, _ := ioutil.ReadAll(io.LimitReader(r, limitBytes))
	log.Printf(format, string(body))
}

func tryReadResponse(r io.Reader, limitBytes int64) (*TokenResponse, error) {
	body, err := ioutil.ReadAll(io.LimitReader(r, limitBytes))
	if err != nil {
		return nil, err
	}
	tokenResponse := &TokenResponse{}
	if err := json.Unmarshal(body, tokenResponse); err != nil {
		return nil, err
	}
	return tokenResponse, nil
}

type errWithCode struct {
	error
	code int
}

func fnvHash(text string) string {
	h := fnv.New64a()
	h.Write([]byte(text))
	return strconv.FormatUint(h.Sum64(), 32)
}
