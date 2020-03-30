package ratelimited

import (
	"context"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/store"
	"github.com/openshift/telemeter/pkg/validate"
)

type testStore struct{}

func (s *testStore) ReadMetrics(ctx context.Context, minTimestampMs int64) ([]*store.PartitionedMetrics, error) {
	return nil, nil
}

func (s *testStore) WriteMetrics(context.Context, *store.PartitionedMetrics) error {
	return nil
}

func TestWriteMetrics(t *testing.T) {
	var (
		s   = New(time.Minute, &testStore{})
		ctx = context.Background()
		now = time.Time{}.Add(time.Hour)
	)

	for _, tc := range []struct {
		name        string
		advance     time.Duration
		expectedErr error
		metrics     *store.PartitionedMetrics
	}{
		{
			name:        "write of nil metric is silently dropped",
			advance:     0,
			metrics:     nil,
			expectedErr: nil,
		},
		{
			name:        "immediate write succeeds",
			advance:     0,
			metrics:     &store.PartitionedMetrics{PartitionKey: "a"},
			expectedErr: nil,
		},
		{
			name:        "write after 1 second fails",
			advance:     time.Second,
			metrics:     &store.PartitionedMetrics{PartitionKey: "a"},
			expectedErr: ErrWriteLimitReached("a"),
		},
		{
			name:        "write after 10 seconds still fails",
			advance:     9 * time.Second,
			metrics:     &store.PartitionedMetrics{PartitionKey: "a"},
			expectedErr: ErrWriteLimitReached("a"),
		},
		{
			name:        "write after 10 seconds for another partition succeeds",
			advance:     0,
			metrics:     &store.PartitionedMetrics{PartitionKey: "b"},
			expectedErr: nil,
		},
		{
			name:        "write after 1 minute succeeds",
			advance:     50 * time.Second,
			metrics:     &store.PartitionedMetrics{PartitionKey: "a"},
			expectedErr: nil,
		},
		{
			name:        "write after 2 minutes succeeds",
			advance:     time.Minute,
			metrics:     &store.PartitionedMetrics{PartitionKey: "a"},
			expectedErr: nil,
		},
		{
			name:        "write after 2 minutes for another partition succeeds",
			advance:     time.Minute,
			metrics:     &store.PartitionedMetrics{PartitionKey: "b"},
			expectedErr: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now = now.Add(tc.advance)

			if got := s.writeMetrics(ctx, tc.metrics, now); got != tc.expectedErr {
				t.Errorf("expected err %v, got %v", tc.expectedErr, got)
			}
		})
	}
}

func TestMiddleware(t *testing.T) {
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
			validate.PartitionKey("_id",
				Middleware(time.Minute, time.Now,
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

func Test_middlewareStore_Limit(t *testing.T) {
	s := &middlewareStore{
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
			err := s.Limit(time.Minute, now.Add(tc.advance), tc.key)
			if err != tc.err {
				t.Errorf("expected '%s' got '%s'", tc.err, err)
			}
		})
	}
}
