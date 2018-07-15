package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	"github.com/openshift/telemeter/pkg/authorizer/remoteauthserver"
)

type SavedResponse struct {
	Token         string                         `json:"token"`
	Cluster       string                         `json:"cluster"`
	TokenResponse remoteauthserver.TokenResponse `json:"response"`
}

func main() {
	if len(os.Args) != 3 {
		log.Fatalf("expected two arguments, the listen address and a path to a JSON file containing responses")
	}

	data, err := ioutil.ReadFile(os.Args[2])
	if err != nil {
		log.Fatalf("unable to read JSON file: %v", err)
	}

	var responses []SavedResponse
	if err := json.Unmarshal(data, &responses); err != nil {
		log.Fatalf("unable to parse contents of %s: %v", os.Args[2], err)
	}

	s := remoteauthserver.NewServer()
	s.AllowNewClusters = true
	s.Responses = make(map[remoteauthserver.Key]*remoteauthserver.TokenResponse)
	for i := range responses {
		r := &responses[i]
		s.Responses[remoteauthserver.Key{Token: r.Token, Cluster: r.Cluster}] = &r.TokenResponse
	}

	if err := http.ListenAndServe(os.Args[1], s); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
