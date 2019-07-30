package authorize

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type TokenResponse struct {
	Version int `json:"version"`

	Token            string `json:"token"`
	ExpiresInSeconds int64  `json:"expiresInSeconds"`

	Labels map[string]string `json:"labels"`
}

type tokenStore struct {
	lock    sync.Mutex
	value   string
	expires time.Time
	labels  map[string]string
}

func (t *tokenStore) Load(endpoint *url.URL, initialToken string, rt http.RoundTripper) (string, error) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if len(t.value) > 0 && (t.expires.IsZero() || t.expires.After(time.Now())) {
		return t.value, nil
	}

	c := http.Client{Transport: rt, Timeout: 30 * time.Second}
	req, err := http.NewRequest("POST", endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("unable to create authentication request: %v", err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", initialToken))
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("unable to perform authentication request: %v", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
	case http.StatusUnauthorized:
		return "", fmt.Errorf("initial authentication token is expired or invalid")
	default:
		body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return "", fmt.Errorf("unable to exchange initial token for a long lived token: %d:\n%s", resp.StatusCode, string(body))
	}

	response, parseErr := parseTokenFromBody(resp.Body, 16*1024)
	if parseErr != nil {
		return "", parseErr
	}

	t.value = response.Token
	t.labels = response.Labels
	if response.ExpiresInSeconds >= 60 {
		t.expires = time.Now().Add(time.Duration(response.ExpiresInSeconds-15) * time.Second)
	} else {
		t.expires = time.Time{}
	}

	return t.value, nil
}

func (t *tokenStore) Invalidate(token string) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if token == t.value {
		t.value = ""
		t.labels = nil
		t.expires = time.Time{}
	}
}

func (t *tokenStore) Labels() (map[string]string, bool) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if len(t.value) == 0 {
		return nil, false
	}
	labels := make(map[string]string)
	for k, v := range t.labels {
		labels[k] = v
	}
	return labels, true
}

func parseTokenFromBody(r io.Reader, limitBytes int64) (*TokenResponse, error) {
	data, err := ioutil.ReadAll(io.LimitReader(r, limitBytes))
	if err != nil {
		return nil, fmt.Errorf("unable to read the authentication response: %v", err)
	}
	response := &TokenResponse{}
	if err := json.Unmarshal(data, response); err != nil {
		return nil, fmt.Errorf("unable to parse the authentication response: %v", err)
	}
	return response, nil
}
