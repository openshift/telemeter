package cluster

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"reflect"

	"github.com/hashicorp/memberlist"
	"github.com/openshift/telemeter/pkg/store"
	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"
)

type testStore struct {
	readErr, writeErr error

	partitionKey string
	families     []*clientmodel.MetricFamily
}

func (s *testStore) ReadMetrics(ctx context.Context, minTimestampMs int64) ([]*store.PartitionedMetrics, error) {
	return nil, s.readErr
}

func (s *testStore) WriteMetrics(_ context.Context, p *store.PartitionedMetrics) error {
	s.partitionKey = p.PartitionKey
	s.families = p.Families
	return s.writeErr
}

type testMemberlister struct {
	numMembers      int
	members         []*memberlist.Node
	sendReliableErr error

	sendReliableNode    *memberlist.Node
	sendReliablePayload []byte
}

func (l *testMemberlister) Members() []*memberlist.Node { return l.members }
func (l *testMemberlister) NumMembers() int             { return l.numMembers }
func (l *testMemberlister) Join([]string) (int, error)  { return 0, nil }

func (l *testMemberlister) SendReliable(n *memberlist.Node, payload []byte) error {
	l.sendReliableNode = n
	l.sendReliablePayload = payload
	return l.sendReliableErr
}

func TestWriteMetrics(t *testing.T) {
	pr := prometheus.NewRegistry()
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "test", Help: "test"})
	pr.MustRegister(g)
	g.Set(rand.Float64())
	families, err := pr.Gather()
	if err != nil {
		t.Fatal(err)
	}

	type dynamicClusterCheck func(*DynamicCluster) error

	nodeHasProblems := func(want bool, node string, now time.Time) dynamicClusterCheck {
		return func(d *DynamicCluster) error {
			if got := d.hasProblems(node, now); got != want {
				return fmt.Errorf("want node %v to have problems %t, got %t", node, want, got)
			}
			return nil
		}
	}

	type memberlisterCheck func(*testMemberlister) error

	forwardedToNode := func(want *memberlist.Node) memberlisterCheck {
		return func(m *testMemberlister) error {
			if equal := reflect.DeepEqual(m.sendReliableNode, want); !equal {
				return fmt.Errorf("want data to be forwarded to node %v, got %v", want, m.sendReliableNode)
			}
			return nil
		}
	}

	type storeCheck func(*testStore) error

	storeChecks := func(cs ...storeCheck) storeCheck {
		return func(s *testStore) error {
			for _, c := range cs {
				if err := c(s); err != nil {
					return err
				}
			}
			return nil
		}
	}

	writtenPartitionKeyIs := func(want string) storeCheck {
		return func(store *testStore) error {
			if got := store.partitionKey; got != want {
				return fmt.Errorf("want partitionKey %s, got %s", want, got)
			}
			return nil
		}
	}

	writtenFamiliesEqual := func(want []*clientmodel.MetricFamily) storeCheck {
		return func(store *testStore) error {
			if got := reflect.DeepEqual(store.families, want); !got {
				return fmt.Errorf("want written families to be equal, but they arent")
			}
			return nil
		}
	}

	noWrite := storeChecks(
		writtenPartitionKeyIs(""),
		writtenFamiliesEqual(nil),
	)

	type errCheck func(error) error

	errIs := func(want error) errCheck {
		return func(got error) error {
			if got != want {
				return fmt.Errorf("want err %v, got %v", want, got)
			}
			return nil
		}
	}

	writeErr := errors.New("write error")

	for _, tc := range []struct {
		name, partitionKey string
		localStore         *testStore
		memberlister       *testMemberlister

		initDynamicCluster func(*DynamicCluster)

		writeMetricsCheck   errCheck
		localStoreCheck     storeCheck
		memberlisterCheck   memberlisterCheck
		dynamicClusterCheck dynamicClusterCheck
	}{
		{
			name: "1 ring member local write",

			partitionKey: "a",
			memberlister: &testMemberlister{
				numMembers: 1,
				members: []*memberlist.Node{
					{Name: "local"},
				},
			},
			localStore: &testStore{readErr: nil, writeErr: nil},

			writeMetricsCheck: errIs(nil),
			localStoreCheck: storeChecks(
				writtenPartitionKeyIs("a"),
				writtenFamiliesEqual(families),
			),
			memberlisterCheck: forwardedToNode(nil),
		},
		{
			name: "1 ring member local write failure",

			partitionKey: "a",
			memberlister: &testMemberlister{
				numMembers: 1,
				members: []*memberlist.Node{
					{Name: "local"},
				},
			},
			localStore: &testStore{readErr: nil, writeErr: writeErr},

			writeMetricsCheck: errIs(writeErr),
			memberlisterCheck: forwardedToNode(nil),
		},
		{
			name: "2 ring members unknown remote node",

			partitionKey: "a",
			memberlister: &testMemberlister{numMembers: 2},
			localStore:   &testStore{readErr: nil, writeErr: nil},

			writeMetricsCheck: errIs(nil),
			localStoreCheck: storeChecks(
				writtenPartitionKeyIs("a"),
				writtenFamiliesEqual(families),
			),
			memberlisterCheck: forwardedToNode(nil),
		},
		{
			name: "2 ring members local write",

			partitionKey: "c",
			memberlister: &testMemberlister{
				numMembers: 2,
				members: []*memberlist.Node{
					{Name: "local"},
					{Name: "remote"},
				},
			},
			localStore: &testStore{readErr: nil, writeErr: nil},

			writeMetricsCheck: errIs(nil),
			localStoreCheck: storeChecks(
				writtenPartitionKeyIs("c"),
				writtenFamiliesEqual(families),
			),
			memberlisterCheck: forwardedToNode(nil),
		},
		{
			name: "2 ring members remote forward",

			partitionKey: "a",
			memberlister: &testMemberlister{
				numMembers: 2,
				members: []*memberlist.Node{
					{Name: "local"},
					{Name: "remote"},
				},
			},
			localStore: &testStore{readErr: nil, writeErr: nil},

			writeMetricsCheck: errIs(nil),
			localStoreCheck:   noWrite,
			memberlisterCheck: forwardedToNode(&memberlist.Node{Name: "remote"}),
		},
		{
			name: "2 ring members remote forward failure",

			partitionKey: "a",
			memberlister: &testMemberlister{
				numMembers: 2,
				members: []*memberlist.Node{
					{Name: "local"},
					{Name: "remote"},
				},
				sendReliableErr: writeErr,
			},
			localStore: &testStore{readErr: nil, writeErr: nil},

			writeMetricsCheck: errIs(nil),
			localStoreCheck:   noWrite,
			memberlisterCheck: forwardedToNode(&memberlist.Node{Name: "remote"}),
		},
		{
			name: "2 ring members remote forward not yet problematic",

			partitionKey: "a",
			memberlister: &testMemberlister{
				numMembers: 2,
				members: []*memberlist.Node{
					{Name: "local"},
					{Name: "remote"},
				},
				sendReliableErr: writeErr,
			},
			localStore: &testStore{readErr: nil, writeErr: nil},

			initDynamicCluster: func(dc *DynamicCluster) {
				dc.problemDetected("remote", time.Now().Add(-time.Minute))
			},

			writeMetricsCheck:   errIs(nil),
			localStoreCheck:     noWrite,
			memberlisterCheck:   forwardedToNode(&memberlist.Node{Name: "remote"}),
			dynamicClusterCheck: nodeHasProblems(false, "remote", time.Now()),
		},
		{
			name: "2 ring members remote forward still problematic",

			partitionKey: "a",
			memberlister: &testMemberlister{
				numMembers: 2,
				members: []*memberlist.Node{
					{Name: "local"},
					{Name: "remote"},
				},
				sendReliableErr: writeErr,
			},
			localStore: &testStore{readErr: nil, writeErr: nil},

			initDynamicCluster: func(dc *DynamicCluster) {
				for i := 0; i < 4; i++ {
					dc.problemDetected("remote", time.Now().Add(-time.Hour))
				}
			},

			writeMetricsCheck: errIs(nil),
			localStoreCheck: storeChecks(
				writtenPartitionKeyIs("a"),
				writtenFamiliesEqual(families),
			),
			memberlisterCheck:   forwardedToNode(nil),
			dynamicClusterCheck: nodeHasProblems(true, "remote", time.Now()),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := NewDynamic("local", tc.localStore)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			dc.Start(tc.memberlister, ctx)

			if tc.initDynamicCluster != nil {
				tc.initDynamicCluster(dc)
			}

			if err := tc.writeMetricsCheck(dc.WriteMetrics(ctx, &store.PartitionedMetrics{
				PartitionKey: tc.partitionKey,
				Families:     families,
			})); err != nil {
				t.Error(err)
			}

			if tc.localStoreCheck != nil {
				if err := tc.localStoreCheck(tc.localStore); err != nil {
					t.Error(err)
				}
			}

			if tc.dynamicClusterCheck != nil {
				if err := tc.dynamicClusterCheck(dc); err != nil {
					t.Error(err)
				}
			}

			if tc.memberlisterCheck != nil {
				if err := tc.memberlisterCheck(tc.memberlister); err != nil {
					t.Error(err)
				}
			}
		})
	}
}
