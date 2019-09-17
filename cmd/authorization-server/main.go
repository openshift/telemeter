package main

import (
	"encoding/json"
	"io/ioutil"
	stdlog "log"
	"net/http"
	"os"

	"github.com/go-kit/kit/log/level"

	"github.com/openshift/telemeter/pkg/authorize/tollbooth"
	"github.com/openshift/telemeter/pkg/logger"
)

type tokenEntry struct {
	Token string `json:"token"`
}

func main() {
	if len(os.Args) != 3 {
		stdlog.Fatalf("expected two arguments, the listen address and a path to a JSON file containing responses")
	}

	data, err := ioutil.ReadFile(os.Args[2])
	if err != nil {
		stdlog.Fatalf("unable to read JSON file: %v", err)
	}

	var tokenEntries []tokenEntry
	if err := json.Unmarshal(data, &tokenEntries); err != nil {
		stdlog.Fatalf("unable to parse contents of %s: %v", os.Args[2], err)
	}

	tokenSet := make(map[string]struct{})
	for i := range tokenEntries {
		tokenSet[tokenEntries[i].Token] = struct{}{}
	}

	lgr := logger.Default()
	level.Info(lgr).Log("msg", "Telemeter authorization-server initialized.")

	s := tollbooth.NewMock(lgr, tokenSet)

	if err := http.ListenAndServe(os.Args[1], s); err != nil {
		stdlog.Fatalf("server exited: %v", err)
	}
}
