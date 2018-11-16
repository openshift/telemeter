package memstore

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"

	"github.com/openshift/telemeter/pkg/store"
	dto "github.com/prometheus/client_model/go"
)

func TestCleanup(t *testing.T) {
	type checkFunc func(testData []*store.PartitionedMetrics, s *memoryStore) error

	checks := func(cs ...checkFunc) checkFunc {
		return func(testData []*store.PartitionedMetrics, s *memoryStore) error {
			for _, c := range cs {
				if e := c(testData, s); e != nil {
					return e
				}
			}
			return nil
		}
	}

	metricCountIs := func(want int) checkFunc {
		return func(_ []*store.PartitionedMetrics, s *memoryStore) error {
			got := 0
			for _, slice := range s.store {
				for _, f := range slice.families {
					got += len(f.Metric)
				}
			}
			if got != want {
				return fmt.Errorf("want %d metrics in store, got %d", want, got)
			}
			return nil
		}
	}

	storedPartitions := func(want ...string) checkFunc {
		return func(_ []*store.PartitionedMetrics, s *memoryStore) error {
			for _, p := range want {
				if _, ok := s.store[p]; !ok {
					return fmt.Errorf("want store to have partition %q, but it doesn't", p)
				}
			}

			return nil
		}
	}

	data := []*store.PartitionedMetrics{
		partitionedMetrics{
			partitionKey: "p1",
			start:        time.Time{},
			span:         30 * time.Minute,
			families:     10, values: 10,
		}.build(),

		partitionedMetrics{
			partitionKey: "p2",
			start:        time.Time{}.Add(30 * time.Minute),
			span:         30 * time.Minute,
			families:     10, values: 10,
		}.build(),
	}

	for _, tc := range []struct {
		name  string
		now   time.Time
		check checkFunc
	}{
		{
			name: "cleanup immediately",
			// all newest metric timestamps are beyond (p1 is 30m, p2 is 60m)
			now: time.Time{},
			check: checks(
				metricCountIs(200), // 10 families * 10 values * 2 partitions
				storedPartitions("p1", "p2"),
			),
		},
		{
			name: "cleanup after 50 minutes",
			// newest metric timestamp in p1 is 30m, ttl is 20m, hence p1 should be deleted after 50m
			now: time.Time{}.Add(51 * time.Minute),
			check: checks(
				metricCountIs(100), // 10 families * 10 values * 1 partitions
				storedPartitions("p2"),
			),
		},
		{
			name: "cleanup after 80 minutes",
			// newest metric timestamp in p2 is 60m, ttl is 20m, hence p2 should be deleted after 80m
			now: time.Time{}.Add(81 * time.Minute),
			check: checks(
				metricCountIs(0), // all cleaned up
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(20 * time.Minute)

			for _, d := range data {
				if err := s.WriteMetrics(context.Background(), d); err != nil {
					t.Error(err)
					return
				}
			}

			s.cleanup(tc.now)

			if err := tc.check(data, s); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestReadWriteMetrics(t *testing.T) {
	testData := partitionedMetrics{
		partitionKey: "foo",
		start:        time.Time{},
		span:         30 * time.Minute,
		families:     10,
		values:       10,
	}.build()

	type checkFunc func([]*store.PartitionedMetrics, error) error

	hasErr := func(want error) checkFunc {
		return func(_ []*store.PartitionedMetrics, got error) error {
			if want != got {
				return fmt.Errorf("want error %v, got %v", want, got)
			}
			return nil
		}
	}

	deepEquals := func(want []*store.PartitionedMetrics) checkFunc {
		return func(got []*store.PartitionedMetrics, _ error) error {
			if !reflect.DeepEqual(want, got) {
				return fmt.Errorf("want written metrics to be %v, got %v", want, got)

			}
			return nil
		}
	}

	lenPartitionedMetricsIs := func(want int) checkFunc {
		return func(pms []*store.PartitionedMetrics, _ error) error {
			if got := len(pms); got != want {
				return fmt.Errorf("want length metrics %d, got %d", want, got)

			}
			return nil
		}
	}

	checks := func(cs ...checkFunc) checkFunc {
		return func(got []*store.PartitionedMetrics, err error) error {
			for _, c := range cs {
				if e := c(got, err); e != nil {
					return e
				}
			}
			return nil
		}
	}

	for _, tc := range []struct {
		name         string
		minTimestamp time.Time
		data         *store.PartitionedMetrics
		check        checkFunc
	}{
		{
			name:         "read metrics immediately",
			minTimestamp: time.Time{},
			data:         testData,
			check: checks(
				hasErr(nil),
				lenPartitionedMetricsIs(1),
				deepEquals([]*store.PartitionedMetrics{testData}),
			),
		},
		{
			name:         "read metrics after 10 minutes",
			minTimestamp: time.Time{}.Add(10 * time.Minute),
			data:         testData,
			check: checks(
				hasErr(nil),
				lenPartitionedMetricsIs(1),
				deepEquals([]*store.PartitionedMetrics{testData}),
			),
		},
		{
			name:         "read metrics after 40 minutes",
			minTimestamp: time.Time{}.Add(40 * time.Minute),
			data:         testData,
			check: checks(
				hasErr(nil),
				lenPartitionedMetricsIs(0),
			),
		},
		{
			name:         "read empty metrics",
			minTimestamp: time.Time{}.Add(40 * time.Minute),
			data: &store.PartitionedMetrics{
				PartitionKey: "test",
				Families: []*dto.MetricFamily{
					{
						Name:   proto.String("test"),
						Metric: nil,
					},
				},
			},
			check: checks(
				hasErr(nil),
				lenPartitionedMetricsIs(0),
			),
		},
		{
			name:         "read nil metrics",
			minTimestamp: time.Time{}.Add(40 * time.Minute),
			data:         nil,
			check: checks(
				hasErr(nil),
				lenPartitionedMetricsIs(0),
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(time.Second)

			if err := s.WriteMetrics(context.Background(), tc.data); err != nil {
				t.Error(err)
				return
			}

			if err := tc.check(s.ReadMetrics(
				context.Background(),
				tc.minTimestamp.UnixNano()/int64(time.Millisecond)),
			); err != nil {
				t.Error(err)
			}
		})
	}
}

type partitionedMetrics struct {
	partitionKey     string
	start            time.Time
	span             time.Duration
	families, values int
}

func (pm partitionedMetrics) build() *store.PartitionedMetrics {
	tDelta := time.Duration(float64(pm.span) / float64(pm.values-1))

	var families []*dto.MetricFamily

	for i := 0; i < pm.families; i++ {
		f := &dto.MetricFamily{
			Name: proto.String("test" + strconv.Itoa(i)),
			Help: proto.String("help test" + strconv.Itoa(i)),
		}

		for j := 0; j < pm.values; j++ {
			m := &dto.Metric{
				Gauge: &dto.Gauge{
					Value: proto.Float64(rand.Float64()),
				},
				TimestampMs: proto.Int64(
					pm.start.Add(tDelta*time.Duration(j)).UnixNano() / int64(time.Millisecond),
				),
			}

			f.Metric = append(f.Metric, m)
		}

		families = append(families, f)
	}

	return &store.PartitionedMetrics{
		PartitionKey: pm.partitionKey,
		Families:     families,
	}
}
