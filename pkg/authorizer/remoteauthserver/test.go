package remoteauthserver

import (
	"net/http"
	"testing"
)

type validator struct {
	http.Client
}

func (v *validator) Validate(t *testing.T) {
	testCases := map[string]struct {
		partitionKey string
	}{
		"invalid client authorization returns an error": {},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Logf(testCase.partitionKey)
		})
	}
}
