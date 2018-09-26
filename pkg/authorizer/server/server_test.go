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
		tokens        map[string]struct{}
		want          *clusterRegistration
		wantErr       bool
		wantErrString string
	}{
		{
			name:          "no cluster ID",
			tokens:        make(map[string]struct{}),
			wantErr:       true,
			wantErrString: "rejected request with code 400",
		},
		{
			name:          "unknown token",
			args:          args{token: "a", cluster: "b"},
			tokens:        map[string]struct{}{"c": struct{}{}},
			wantErr:       true,
			wantErrString: "unauthorized",
		},
		{
			name:    "known token",
			args:    args{token: "a", cluster: "b"},
			tokens:  map[string]struct{}{"a": struct{}{}},
			wantErr: false,
			want: &clusterRegistration{
				AuthorizationToken: "a",
				ClusterID:          "b",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer(tt.tokens)
			server := httptest.NewServer(s)
			defer server.Close()
			u, _ := url.Parse(server.URL)

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
				return
			}
			if got.AccountID == "" {
				t.Error("expected account ID, got none")
			}

			// reset account ID to compare only cluster ID and auth token
			got.AccountID = ""
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Authorizer.authorizeRemote() = %v, want %v", got, tt.want)
			}
		})
	}
}
