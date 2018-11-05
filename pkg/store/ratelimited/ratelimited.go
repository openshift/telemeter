package ratelimited

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/openshift/telemeter/pkg/store"
	"golang.org/x/time/rate"
)

var ErrWriteLimitReached = errors.New("write limit reached")

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

	s.mu.Lock()
	defer s.mu.Unlock()

	limiter, ok := s.store[p.PartitionKey]

	if !ok {
		limiter = rate.NewLimiter(rate.Every(s.limit), 1)
		s.store[p.PartitionKey] = limiter
	}

	if !limiter.AllowN(now, 1) {
		return ErrWriteLimitReached
	}

	return s.next.WriteMetrics(ctx, p)
}
