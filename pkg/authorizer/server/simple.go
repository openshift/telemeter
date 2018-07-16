package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Key struct {
	Token   string
	Cluster string
}

type Server struct {
	AllowNewClusters bool
	Responses        map[Key]*TokenResponse
	Received         map[Key]struct{}
}

func NewServer() *Server {
	return &Server{
		Received: make(map[Key]struct{}),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	if req.Method != "POST" {
		Write(w, &TokenResponse{APIVersion: "v1", Status: "failure", Code: http.StatusMethodNotAllowed, Reason: "MethodNotAllowed", Message: "Only requests of type 'POST' are accepted."})
		return
	}
	if req.Header.Get("Content-Type") != "application/json" {
		Write(w, &TokenResponse{APIVersion: "v1", Status: "failure", Code: http.StatusBadRequest, Reason: "InvalidContentType", Message: "Only requests with Content-Type application/json are accepted."})
		return
	}
	tokenRequest := &TokenRequest{}
	if err := json.NewDecoder(req.Body).Decode(tokenRequest); err != nil {
		Write(w, &TokenResponse{APIVersion: "v1", Status: "failure", Code: http.StatusBadRequest, Reason: "InvalidBody", Message: fmt.Sprintf("Unable to parse body as JSON: %v", err)})
		return
	}
	if tokenRequest.APIVersion != "v1" {
		Write(w, &TokenResponse{APIVersion: "v1", Status: "failure", Code: http.StatusBadRequest, Reason: "InvalidAPIVersion", Message: "Only requests with api_version 'v1' are accepted."})
		return
	}
	key := Key{Token: tokenRequest.AuthorizationToken, Cluster: tokenRequest.ClusterID}
	resp, ok := s.Responses[key]
	if !s.AllowNewClusters {
		if !ok {
			Write(w, &TokenResponse{APIVersion: "v1", Status: "failure", Code: http.StatusInternalServerError, Reason: "UnknownError", Message: "Generic error."})
			return
		}
		s.Received[key] = struct{}{}
		Write(w, resp)
		return
	}

	// lookup without cluster ID specified
	key.Cluster = ""
	resp, ok = s.Responses[key]
	if !ok {
		Write(w, &TokenResponse{APIVersion: "v1", Status: "failure", Code: http.StatusUnauthorized, Reason: "NotAuthorized", Message: "The provided token is not recognized."})
		return
	}

	// provide simple 201 vs 200 behavior if we have already received this request
	if _, ok := s.Received[key]; ok && resp.Status == "ok" && resp.Code == http.StatusCreated {
		copied := *resp
		copied.Code = http.StatusOK
		resp = &copied
	}

	Write(w, resp)
}

func Write(w http.ResponseWriter, resp *TokenResponse) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Code)
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return err
	}
	w.Write(data)
	return nil
}
