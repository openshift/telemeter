package remoteauthserver

type TokenRequest struct {
	APIVersion string `json:"api_version"`

	AuthorizationToken string `json:"authorization_token"`
	ClusterID          string `json:"cluster_id"`
}

type TokenResponse struct {
	APIVersion string `json:"api_version"`

	Status  string `json:"status"`
	Code    int    `json:"code"`
	Reason  string `json:"reason"`
	Message string `json:"message"`

	AccountID string `json:"account_id"`

	Labels map[string]string `json:"labels"`
}
