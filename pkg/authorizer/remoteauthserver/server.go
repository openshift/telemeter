package remoteauthserver

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/smarterclayton/telemeter/pkg/authorizer/jwt"
	"github.com/smarterclayton/telemeter/pkg/authorizer/remote"
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
	if req.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	req.Body = http.MaxBytesReader(w, req.Body, 1024)
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

	// skip authentication upstream and reflect token
	if a.to == nil {
		auth := strings.SplitN(req.Header.Get("Authorization"), " ", 2)
		if strings.ToLower(auth[0]) != "bearer" {
			http.Error(w, "Only bearer authorization allowed", http.StatusUnauthorized)
			return
		}
		if len(auth) != 2 || len(strings.TrimSpace(auth[1])) == 0 {
			http.Error(w, "Invalid Authorization header", http.StatusUnauthorized)
			return
		}

		user := fnvHash(auth[1])
		log.Printf("warning: Performing no-op authentication, user will be %s with cluster %s", user, cluster)

		labels := make(map[string]string)
		for k, v := range a.labels {
			labels[k] = v
		}
		labels[a.partitionKey] = cluster

		token, err := a.signer.GenerateToken(jwt.Claims(user, labels, a.expireInSeconds, []string{"federate"}))
		if err != nil {
			log.Printf("error: unable to generate token: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data, err := json.Marshal(remote.TokenResponse{
			Version:          1,
			Token:            token,
			ExpiresInSeconds: a.expireInSeconds,
			Labels:           labels,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(data)
		return
	}

	http.Error(w, "Remote authentication disabled", http.StatusUnauthorized)
	// rate limit based on the incoming token
	// exchange a bearer token via a remote call to an upstream
}

func fnvHash(text string) string {
	h := fnv.New64a()
	h.Write([]byte(text))
	return strconv.FormatUint(h.Sum64(), 32)
}
