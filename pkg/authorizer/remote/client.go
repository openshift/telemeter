package remote

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

type token struct {
	lock    sync.Mutex
	value   string
	expires time.Time
	labels  map[string]string
}

func now() time.Time {
	return time.Now()
}

func (t *token) Load(endpoint *url.URL, initialToken string, rt http.RoundTripper) (string, error) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if len(t.value) > 0 && (t.expires.IsZero() || t.expires.After(time.Now())) {
		return t.value, nil
	}

	c := http.Client{Transport: rt, Timeout: 10 * time.Second}
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
		body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("unable to exchange initial token for a long lived token: %d: %v", resp.StatusCode, string(body))
	}

	data, err := ioutil.ReadAll(io.LimitReader(resp.Body, 16384))
	if err != nil {
		return "", fmt.Errorf("unable to read the authentication response: %v", err)
	}
	response := &TokenResponse{}
	if err := json.Unmarshal(data, response); err != nil {
		return "", fmt.Errorf("unable to parse the authentication response: %v", err)
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

func (t *token) Invalidate(token string) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if token == t.value {
		t.value = ""
		t.labels = nil
		t.expires = time.Time{}
	}
}

func (t *token) Labels() (map[string]string, bool) {
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

type ServerRotatingRoundTripper struct {
	endpoint     *url.URL
	initialToken string
	token        token

	wrapper http.RoundTripper
}

func NewServerRotatingRoundTripper(initialToken string, endpoint *url.URL, rt http.RoundTripper) *ServerRotatingRoundTripper {
	return &ServerRotatingRoundTripper{
		initialToken: initialToken,
		endpoint:     endpoint,
		wrapper:      rt,
	}
}

func (rt *ServerRotatingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := rt.token.Load(rt.endpoint, rt.initialToken, rt.wrapper)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
	resp, err := rt.wrapper.RoundTrip(req)
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		rt.token.Invalidate(token)
	}
	return resp, err
}

func (rt *ServerRotatingRoundTripper) Labels() (map[string]string, error) {
	_, err := rt.token.Load(rt.endpoint, rt.initialToken, rt.wrapper)
	if err != nil {
		return nil, fmt.Errorf("unable to access labels: %v", err)
	}
	labels, ok := rt.token.Labels()
	if !ok {
		return nil, fmt.Errorf("labels from server have expired")
	}
	return labels, nil
}
