package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/openshift/telemeter/pkg/authorizer/jwt"
)

func TestAuthorizer_authorizeRemote(t *testing.T) {
	type fields struct {
		partitionKey    string
		labels          map[string]string
		client          *http.Client
		expireInSeconds int64
		signer          *jwt.Signer
	}
	type args struct {
		token   string
		cluster string
	}
	tests := []struct {
		name          string
		fields        fields
		args          args
		responses     map[Key]*TokenResponse
		want          *TokenResponse
		wantErr       bool
		wantErrString string
	}{
		{name: "no response defined", wantErr: true},
		{
			name:          "generic error",
			args:          args{token: "a", cluster: "b"},
			responses:     map[Key]*TokenResponse{{Token: "a", Cluster: "b"}: {APIVersion: "v1", Code: http.StatusBadGateway}},
			wantErrString: "rejected request with code 502",
		},
		{
			name:          "error when no user",
			args:          args{token: "a", cluster: "b"},
			responses:     map[Key]*TokenResponse{{Token: "a", Cluster: "b"}: {APIVersion: "v1", Status: "ok", Code: http.StatusOK, AccountID: ""}},
			wantErrString: "responded with an empty user string",
		},
		{
			name: "success when user provided",
			args: args{token: "a", cluster: "b"},
			responses: map[Key]*TokenResponse{
				{Token: "a", Cluster: "b"}: {
					APIVersion: "v1",
					Status:     "ok",
					Code:       http.StatusOK,
					AccountID:  "c",
				},
			},
			want: &TokenResponse{
				APIVersion: "v1",
				Status:     "ok",
				Code:       http.StatusOK,
				AccountID:  "c",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer()
			s.Responses = tt.responses
			server := httptest.NewServer(s)
			defer server.Close()
			u, _ := url.Parse(server.URL)
			tt.wantErr = tt.wantErr || len(tt.wantErrString) > 0

			a := &Authorizer{
				partitionKey:    tt.fields.partitionKey,
				labels:          tt.fields.labels,
				to:              u,
				client:          tt.fields.client,
				expireInSeconds: tt.fields.expireInSeconds,
				signer:          tt.fields.signer,
			}
			if a.client == nil {
				a.client = http.DefaultClient
			}

			got, err := a.authorizeRemote(tt.args.token, tt.args.cluster)
			if (err != nil) != tt.wantErr {
				t.Errorf("Authorizer.authorizeRemote() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && len(tt.wantErrString) > 0 {
				if !strings.Contains(err.Error(), tt.wantErrString) {
					t.Errorf("Authorizer.authorizeRemote() error = %v, wantErrString %v", err, tt.wantErrString)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Authorizer.authorizeRemote() = %v, want %v", got, tt.want)
			}
		})
	}
}
