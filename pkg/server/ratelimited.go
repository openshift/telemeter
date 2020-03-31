package ratelimited

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/openshift/telemeter/pkg/validate"
)

type ErrWriteLimitReached string

func (e ErrWriteLimitReached) Error() string {
	return fmt.Sprintf("write limit reached for key %q", string(e))
}

func Middleware(limit time.Duration, now func() time.Time, next http.HandlerFunc) http.HandlerFunc {
	s := middlewareStore{
		limits: make(map[string]*rate.Limiter),
		mu:     sync.Mutex{},
	}

	return func(w http.ResponseWriter, r *http.Request) {
		partitionKey, ok := validate.PartitionFromContext(r.Context())
		if !ok {
			http.Error(w, "failed to get partition from request", http.StatusInternalServerError)
			return
		}

		if err := s.Limit(limit, now(), partitionKey); err != nil {
			http.Error(w, err.Error(), http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	}
}

type middlewareStore struct {
	limits map[string]*rate.Limiter
	mu     sync.Mutex
}

func (s *middlewareStore) Limit(limit time.Duration, now time.Time, key string) error {
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
