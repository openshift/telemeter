package ratelimited

import (
	"context"
	"testing"
	"time"

	"github.com/openshift/telemeter/pkg/store"
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
