package server

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-kit/kit/log"
	"golang.org/x/time/rate"

	"github.com/openshift/telemeter/pkg/authorize"
)

func TestRatelimit(t *testing.T) {
	clientID := func() string { return "" }

	fakeAuthorizeHandler := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(authorize.WithClient(r.Context(), &authorize.Client{
				Labels: map[string]string{"_id": clientID()},
			}))
			next.ServeHTTP(w, r)
		}
	}
	server := httptest.NewServer(
		fakeAuthorizeHandler(
			ClusterID(log.NewNopLogger(), "_id",
				Ratelimit(log.NewNopLogger(), time.Minute, time.Now,
					func(w http.ResponseWriter, r *http.Request) {},
				),
			),
		),
	)

	defer server.Close()

	for _, tc := range []struct {
		name           string
		advance        time.Duration
		clientID       string
		expectedStatus int
		expectedErr    error
	}{
		{
			name:           "WriteSuccessForA",
			advance:        0,
			clientID:       "a",
			expectedStatus: http.StatusOK,
			expectedErr:    nil,
		},
		{
			name:           "WriteSuccessForB",
			advance:        0,
			clientID:       "b",
			expectedStatus: http.StatusOK,
			expectedErr:    nil,
		},
		{
			name:           "FailAfter1sForA",
			advance:        time.Second,
			clientID:       "a",
			expectedStatus: http.StatusTooManyRequests,
			expectedErr:    ErrWriteLimitReached("a"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientID = func() string { return tc.clientID }

			time.Sleep(tc.advance)

			req, err := http.NewRequest(http.MethodPost, server.URL, nil)
			if err != nil {
				t.Error("failed to create request")
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("failed to do request: %v", err)
				return
			}

			body, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.expectedStatus {
				t.Errorf("expected status %d and got %d: %s", tc.expectedStatus, resp.StatusCode, string(body))
			}

			if tc.expectedErr != nil {
				if strings.TrimSpace(string(body)) != tc.expectedErr.Error() {
					t.Errorf("expcted body '%s', and got '%s'", tc.expectedErr.Error(), strings.TrimSpace(string(body)))
				}
			}
		})
	}
}

func TestRatelimitStore_Limit(t *testing.T) {
	s := &ratelimitStore{
		limits: map[string]*rate.Limiter{},
		mu:     sync.Mutex{},
	}

	now := time.Time{}.Add(time.Hour)

	for _, tc := range []struct {
		name    string
		advance time.Duration
		key     string
		err     error
	}{
		{
			name:    "first",
			advance: 0,
			key:     "a",
			err:     nil,
		},
		{
			name:    "1sfails",
			advance: time.Second,
			key:     "a",
			err:     ErrWriteLimitReached("a"),
		},
		{
			name:    "10sfails",
			advance: 10 * time.Second,
			key:     "a",
			err:     ErrWriteLimitReached("a"),
		},
		{
			name:    "10sSuccessForB",
			advance: 10 * time.Second,
			key:     "b",
			err:     nil,
		},
		{
			name:    "1minSuccess",
			advance: time.Minute,
			key:     "a",
			err:     nil,
		},
		{
			name:    "2minSuccess",
			advance: 2 * time.Minute,
			key:     "a",
			err:     nil,
		},
		{
			name:    "2minSuccessForB",
			advance: 2 * time.Minute,
			key:     "b",
			err:     nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := s.limit(time.Minute, now.Add(tc.advance), tc.key)
			if err != tc.err {
				t.Errorf("expected '%s' got '%s'", tc.err, err)
			}
		})
	}
}
