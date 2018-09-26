package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	"github.com/openshift/telemeter/pkg/authorizer/server"
)

type tokenEntry struct {
	Token string `json:"token"`
}

func main() {
	if len(os.Args) != 3 {
		log.Fatalf("expected two arguments, the listen address and a path to a JSON file containing responses")
	}

	data, err := ioutil.ReadFile(os.Args[2])
	if err != nil {
		log.Fatalf("unable to read JSON file: %v", err)
	}

	var tokenEntries []tokenEntry
	if err := json.Unmarshal(data, &tokenEntries); err != nil {
		log.Fatalf("unable to parse contents of %s: %v", os.Args[2], err)
	}

	tokenSet := make(map[string]struct{})
	for i := range tokenEntries {
		tokenSet[tokenEntries[i].Token] = struct{}{}
	}

	s := server.NewServer(tokenSet)

	if err := http.ListenAndServe(os.Args[1], s); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
