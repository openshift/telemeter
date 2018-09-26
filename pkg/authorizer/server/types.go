package server

type clusterRegistration struct {
	ClusterID          string `json:"cluster_id"`
	AuthorizationToken string `json:"authorization_token"`
	AccountID          string `json:"account_id"`
}

type registrationError struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}
