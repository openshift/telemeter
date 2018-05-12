package remote

type TokenResponse struct {
	Version int `json:"version"`

	Token            string `json:"token"`
	ExpiresInSeconds int64  `json:"expiresInSeconds"`

	Labels map[string]string `json:"labels"`
}
