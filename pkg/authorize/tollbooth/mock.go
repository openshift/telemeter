package tollbooth

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/openshift/telemeter/pkg/fnv"
)

type Key struct {
	Token   string
	Cluster string
}

type mock struct {
	mu        sync.Mutex
	Tokens    map[string]struct{}
	Responses map[Key]clusterRegistration
}

func NewMock(tokenSet map[string]struct{}) *mock {
	return &mock{
		Tokens:    tokenSet,
		Responses: make(map[Key]clusterRegistration),
	}
}

func (s *mock) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	if req.Method != "POST" {
		Write(w, http.StatusMethodNotAllowed, &registrationError{Name: "MethodNotAllowed", Reason: "Only requests of type 'POST' are accepted."})
		return
	}
	if req.Header.Get("Content-Type") != "application/json" {
		Write(w, http.StatusBadRequest, &registrationError{Name: "InvalidContentType", Reason: "Only requests with Content-Type application/json are accepted."})
		return
	}
	regRequest := &clusterRegistration{}
	if err := json.NewDecoder(req.Body).Decode(regRequest); err != nil {
		Write(w, http.StatusBadRequest, &registrationError{Name: "InvalidBody", Reason: fmt.Sprintf("Unable to parse body as JSON: %v", err)})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if regRequest.ClusterID == "" {
		Write(w, http.StatusBadRequest, &registrationError{Name: "BadRequest", Reason: "No cluster ID provided."})
		return
	}

	if _, tokenFound := s.Tokens[regRequest.AuthorizationToken]; !tokenFound {
		Write(w, http.StatusUnauthorized, &registrationError{Name: "NotAuthorized", Reason: "The provided token is not recognized."})
		return
	}

	key := Key{Token: regRequest.AuthorizationToken, Cluster: regRequest.ClusterID}
	resp, clusterFound := s.Responses[key]
	code := http.StatusOK

	accountID, err := fnv.Hash(regRequest.ClusterID)
	if err != nil {
		log.Printf("hashing cluster ID failed: %v", err)
		Write(w, http.StatusInternalServerError, &registrationError{Name: "", Reason: "hashing cluster ID failed"})
		return
	}

	if !clusterFound {
		resp = clusterRegistration{
			AccountID:          accountID,
			AuthorizationToken: regRequest.AuthorizationToken,
			ClusterID:          regRequest.ClusterID,
		}
		s.Responses[key] = resp
		code = http.StatusCreated
	}

	Write(w, code, resp)
}

func Write(w http.ResponseWriter, statusCode int, resp interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		log.Printf("marshaling response failed: %v", err)
		return
	}
	if _, err := w.Write(data); err != nil {
		log.Printf("writing response failed %v", err)
		return
	}
}
