package authorize

import (
	"fmt"
	"net/http"
	"net/url"
)

type ServerRotatingRoundTripper struct {
	endpoint     *url.URL
	initialToken string
	tokenStore   tokenStore

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
	token, err := rt.tokenStore.Load(rt.endpoint, rt.initialToken, rt.wrapper)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
	resp, err := rt.wrapper.RoundTrip(req)
	if resp != nil && resp.StatusCode == http.StatusUnauthorized {
		rt.tokenStore.Invalidate(token)
	}
	return resp, err
}

func (rt *ServerRotatingRoundTripper) Labels() (map[string]string, error) {
	_, err := rt.tokenStore.Load(rt.endpoint, rt.initialToken, rt.wrapper)
	if err != nil {
		return nil, fmt.Errorf("unable to authorize to server: %v", err)
	}
	labels, ok := rt.tokenStore.Labels()
	if !ok {
		return nil, fmt.Errorf("labels from server have expired")
	}
	return labels, nil
}
