package ratelimited

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/openshift/telemeter/pkg/store"
	"github.com/openshift/telemeter/pkg/validate"
)

type ErrWriteLimitReached string

func (e ErrWriteLimitReached) Error() string {
	return fmt.Sprintf("write limit reached for key %q", string(e))
}

type lstore struct {
	limit time.Duration
	next  store.Store

	mu    sync.RWMutex // protects fields below
	store map[string]*rate.Limiter
}

// New returns a store that wraps next and limits writes to it.
// Writes can happen at most at intervals specified by limit per partition key.
func New(limit time.Duration, next store.Store) *lstore {
	return &lstore{
		limit: limit,
		next:  next,
		store: make(map[string]*rate.Limiter),
	}
}

func (s *lstore) ReadMetrics(ctx context.Context, minTimestampMs int64) ([]*store.PartitionedMetrics, error) {
	return s.next.ReadMetrics(ctx, minTimestampMs)
}

func (s *lstore) WriteMetrics(ctx context.Context, p *store.PartitionedMetrics) error {
	return s.writeMetrics(ctx, p, time.Now())
}

func (s *lstore) writeMetrics(ctx context.Context, p *store.PartitionedMetrics, now time.Time) error {
	if p == nil {
		return nil
	}

	if limiter := s.limiter(p.PartitionKey); !limiter.AllowN(now, 1) {
		return ErrWriteLimitReached(p.PartitionKey)
	}

	return s.next.WriteMetrics(ctx, p)
}

func (s *lstore) limiter(partitionKey string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()

	limiter, ok := s.store[partitionKey]
	if !ok {
		limiter = rate.NewLimiter(rate.Every(s.limit), 1)
		s.store[partitionKey] = limiter
	}

	return limiter
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
