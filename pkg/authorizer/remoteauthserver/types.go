package remoteauthserver

type TokenResponse struct {
	Version int `json:"version"`

	User   string            `json:"user"`
	Labels map[string]string `json:"labels"`
}
