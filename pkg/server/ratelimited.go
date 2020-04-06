package server

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ErrWriteLimitReached is an error that is returned when a cluster has sent too many requests.
type ErrWriteLimitReached string

func (e ErrWriteLimitReached) Error() string {
	return fmt.Sprintf("write limit reached for key %q", string(e))
}

// Ratelimit is a middleware that rate limits requests based on a cluster ID.
func Ratelimit(limit time.Duration, now func() time.Time, next http.HandlerFunc) http.HandlerFunc {
	s := ratelimitStore{
		limits: make(map[string]*rate.Limiter),
		mu:     sync.Mutex{},
	}

	return func(w http.ResponseWriter, r *http.Request) {
		clusterID, ok := ClusterIDFromContext(r.Context())
		if !ok {
			http.Error(w, "failed to get cluster ID from request", http.StatusInternalServerError)
			return
		}

		if err := s.limit(limit, now(), clusterID); err != nil {
			http.Error(w, err.Error(), http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	}
}

type ratelimitStore struct {
	limits map[string]*rate.Limiter
	mu     sync.Mutex
}

func (s *ratelimitStore) limit(limit time.Duration, now time.Time, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	limiter, ok := s.limits[key]
	if !ok {
		limiter = rate.NewLimiter(rate.Every(limit), 1)
		s.limits[key] = limiter
	}

	if !limiter.AllowN(now, 1) {
		return ErrWriteLimitReached(key)
	}

	return nil
}
