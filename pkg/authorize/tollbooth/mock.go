package tollbooth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

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
	logger    log.Logger
}

func NewMock(logger log.Logger, tokenSet map[string]struct{}) *mock {
	return &mock{
		Tokens:    tokenSet,
		Responses: make(map[Key]clusterRegistration),
		logger:    log.With(logger, "component", "authorize/toolbooth"),
	}
}

func (s *mock) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	if req.Method != "POST" {
		write(w, http.StatusMethodNotAllowed, &registrationError{Name: "MethodNotAllowed", Reason: "Only requests of type 'POST' are accepted."}, s.logger)
		return
	}
	if req.Header.Get("Content-Type") != "application/json" {
		write(w, http.StatusBadRequest, &registrationError{Name: "InvalidContentType", Reason: "Only requests with Content-Type application/json are accepted."}, s.logger)
		return
	}
	regRequest := &clusterRegistration{}
	if err := json.NewDecoder(req.Body).Decode(regRequest); err != nil {
		write(w, http.StatusBadRequest, &registrationError{Name: "InvalidBody", Reason: fmt.Sprintf("Unable to parse body as JSON: %v", err)}, s.logger)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if regRequest.ClusterID == "" {
		write(w, http.StatusBadRequest, &registrationError{Name: "BadRequest", Reason: "No cluster ID provided."}, s.logger)
		return
	}

	if _, tokenFound := s.Tokens[regRequest.AuthorizationToken]; !tokenFound {
		write(w, http.StatusUnauthorized, &registrationError{Name: "NotAuthorized", Reason: "The provided token is not recognized."}, s.logger)
		return
	}

	key := Key{Token: regRequest.AuthorizationToken, Cluster: regRequest.ClusterID}
	resp, clusterFound := s.Responses[key]
	code := http.StatusOK

	accountID, err := fnv.Hash(regRequest.ClusterID)
	if err != nil {
		level.Warn(s.logger).Log("msg", "hashing cluster ID failed", "err", err)
		write(w, http.StatusInternalServerError, &registrationError{Name: "", Reason: "hashing cluster ID failed"}, s.logger)
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

	write(w, code, resp, s.logger)
}

func write(w http.ResponseWriter, statusCode int, resp interface{}, logger log.Logger) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		level.Error(logger).Log("err", "marshaling response failed", "err", err)
		return
	}
	if _, err := w.Write(data); err != nil {
		level.Error(logger).Log("err", "writing response failed", "err", err)
		return
	}
}
