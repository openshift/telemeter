package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/go-msgpack/codec"
	"github.com/hashicorp/memberlist"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/serialx/hashring"

	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/metricsclient"
	"github.com/openshift/telemeter/pkg/store"
)

var (
	metricForwardResult = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "telemeter_server_cluster_forward",
		Help: "Tracks the outcome of forwarding results inside the cluster.",
	}, []string{"result"})
	metricForwardSamples = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "telemeter_server_cluster_forward_samples",
		Help: "Tracks the number of samples forwarded by this server.",
	})
	metricForwardLatency = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Name: "telemeter_server_cluster_forward_latency",
		Help: "Tracks latency of forwarding results inside the cluster.",
	}, []string{"result"})
)

func init() {
	prometheus.MustRegister(metricForwardResult, metricForwardSamples, metricForwardLatency)
}

var msgHandle = &codec.MsgpackHandle{}

type messageType byte

const (
	// protocolVersion is the schema for inter-cluster communication.
	protocolVersion = 1

	// metricMessage carries a pre-validated metric bundle for a given partition key.
	// Format is:
	//   0:      <type(byte)>
	//   1-??:   <header(metricMessageHeader)>
	//   remain: <snappy-compressed(protobuf-delimited-metrics)>
	metricMessage messageType = 1
)

type metricMessageHeader struct {
	PartitionKey string
}

type nodeData struct {
	problems int
	last     time.Time
}

type memberInfo struct {
	Name string
	Addr string
}

type debugInfo struct {
	Name            string
	ProtocolVersion int
	Members         []memberInfo
}

type memberlister interface {
	Members() []*memberlist.Node
	NumMembers() (alive int)
	Join(existing []string) (int, error)
	SendReliable(to *memberlist.Node, msg []byte) error
}

// DynamicCluster is the struct that handles the gossip based hashring cluster state of telemeter server nodes.
//
// It wraps a store.Store and can be used as a drop-in store replacement to
// forward collected metrics to the actual target node based on the current hashring state.
type DynamicCluster struct {
	name  string
	store store.Store
	// skip problematic endpoints for at least this amount of time
	expiration time.Duration

	ml  memberlister
	ctx context.Context

	// queue is the pending message queue.
	// It is populated in the #NotifyMsg callback
	// and is processed in the #handleMessage function.
	queue chan ([]byte)

	lock        sync.Mutex
	updated     time.Time
	ring        *hashring.HashRing
	problematic map[string]*nodeData
}

// NewDynamic returns a new DynamicCluster struct for the given name and underlying store.
func NewDynamic(name string, store store.Store) *DynamicCluster {
	return &DynamicCluster{
		name:       name,
		store:      store,
		expiration: 2 * time.Minute,
		ring:       hashring.New(nil),

		queue:       make(chan []byte, 100),
		problematic: make(map[string]*nodeData),
	}
}

// Start starts processing the internal message queue
// until the given context is done.
func (c *DynamicCluster) Start(ml memberlister, ctx context.Context) {
	c.ml = ml
	c.ctx = ctx

	go func() {
		for {
			select {
			case data := <-c.queue:
				if err := c.handleMessage(data); err != nil {
					log.Printf("error: Unable to handle incoming message: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// ServeHTTP implements a simple http handler exposing debug info about the
// cluster state.
func (c *DynamicCluster) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	info := c.debugInfo()
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		log.Printf("marshaling debug info failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(data); err != nil {
		log.Printf("writing debug info failed: %v", err)
	}
}

func (c *DynamicCluster) debugInfo() debugInfo {
	info := debugInfo{
		Name:            c.name,
		ProtocolVersion: protocolVersion,
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.ml != nil {
		for _, n := range c.ml.Members() {
			info.Members = append(info.Members, memberInfo{Name: n.Name, Addr: n.Address()})
		}
	}
	return info
}

func (c *DynamicCluster) refreshRing() {
	c.lock.Lock()
	defer c.lock.Unlock()
	if !c.updated.IsZero() && c.updated.Before(time.Now().Add(-time.Minute)) {
		return
	}

	members := make([]string, 0, c.ml.NumMembers())
	for _, n := range c.ml.Members() {
		members = append(members, n.Name)
	}
	c.ring = hashring.New(members)
}

func (c *DynamicCluster) getNodeForKey(partitionKey string) (string, bool) {
	c.refreshRing()
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.ring.GetNode(partitionKey)
}

// Join attempts to join a cluster by contacting all the given seed hosts.
//
// This simply delegates to github.com/hashicorp/memberlist#Memberlist.Join.
func (c *DynamicCluster) Join(seeds []string) error {
	_, err := c.ml.Join(seeds)
	return err
}

// NotifyJoin is the callback that is invoked when a node is detected to have joined.
//
// See github.com/hashicorp/memberlist#EventDelegate.NotifyJoin
func (c *DynamicCluster) NotifyJoin(node *memberlist.Node) {
	c.lock.Lock()
	defer c.lock.Unlock()
	log.Printf("[%s] node joined %s", c.name, node.Name)
	c.ring.AddNode(node.Name)
}

// NotifyLeave is the callback that is invoked when a node is detected to have left.
//
// See github.com/hashicorp/memberlist#EventDelegate.NotifyLeave
func (c *DynamicCluster) NotifyLeave(node *memberlist.Node) {
	c.lock.Lock()
	defer c.lock.Unlock()
	log.Printf("[%s] node left %s", c.name, node.Name)
	c.ring.RemoveNode(node.Name)
}

// NotifyUpdate is the callback that is invoked when a node to have updated.
//
// See github.com/hashicorp/memberlist#EventDelegate.NotifyUpdate
func (c *DynamicCluster) NotifyUpdate(node *memberlist.Node) {
	log.Printf("[%s] node update %s", c.name, node.Name)
}

// NodeMeta is the callback that is invoked when metadata is retrieved about this node.
// Currently, no metadata is returned.
//
// See github.com/hashicorp/memberlist#Delegate.NodeMeta
func (c *DynamicCluster) NodeMeta(limit int) []byte { return nil }

// NotifyMsg is the callback that is invoked, when a message is received.
// Any data received here is enqueued in the message queue and processed
// asynchronously in the #handleMessage method.
//
// See github.com/hashicorp/memberlist#Delegate.NotifyMsg
func (c *DynamicCluster) NotifyMsg(data []byte) {
	if len(data) == 0 {
		return
	}
	copied := make([]byte, len(data))
	copy(copied, data)
	select {
	case c.queue <- copied:
	default:
		log.Printf("error: Too many incoming requests queued, dropped data")
	}
}

// GetBroadcasts is the callback that is invoked, when user data messages can be broadcast.
// It is unused.
func (c *DynamicCluster) GetBroadcasts(overhead, limit int) [][]byte { return nil }

// LocalState is the callback that is invoked for a TCP Push/Pull.
// It is unused.
func (c *DynamicCluster) LocalState(join bool) []byte { return nil }

// MergeRemoteState is the callback that is invoked after a TCP Push/Pull.
// It is unused.
func (c *DynamicCluster) MergeRemoteState(buf []byte, join bool) {}

// handleMessage is invoked as soon as there is data available in the message queue.
// It decodes the underlying metric families and stores it using the given metrics store.
func (c *DynamicCluster) handleMessage(data []byte) error {
	switch messageType(data[0]) {
	case metricMessage:
		buf := bytes.NewBuffer(data[1:])
		d := codec.NewDecoder(buf, msgHandle)
		var header metricMessageHeader
		if err := d.Decode(&header); err != nil {
			return err
		}
		if len(header.PartitionKey) == 0 {
			return fmt.Errorf("metric message must have a partition key")
		}
		// TODO: possible optimization opportunity - don't decode the bytes and
		// pass them down to the underlying storage directly.
		families, err := metricsclient.Read(buf)
		if err != nil {
			return err
		}
		if len(families) == 0 {
			return nil
		}
		return c.store.WriteMetrics(c.ctx, &store.PartitionedMetrics{
			PartitionKey: header.PartitionKey,
			Families:     families,
		})

	default:
		return fmt.Errorf("unrecognized message %0x, len=%d", data[0], len(data))
	}
}

func (c *DynamicCluster) memberByName(name string) *memberlist.Node {
	for _, n := range c.ml.Members() {
		if n.Name == name {
			return n
		}
	}
	return nil
}

func (c *DynamicCluster) hasProblems(name string, now time.Time) bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	p, ok := c.problematic[name]
	if !ok {
		return false
	}

	if p.problems < 4 {
		if now.Sub(p.last) < c.expiration {
			return false
		}
		delete(c.problematic, name)
	} else if now.Sub(p.last) >= c.expiration {
		delete(c.problematic, name)
		return false
	}

	return true
}

func (c *DynamicCluster) problemDetected(name string, now time.Time) {
	c.lock.Lock()
	defer c.lock.Unlock()
	p, ok := c.problematic[name]
	if !ok {
		p = &nodeData{}
		c.problematic[name] = p
	}
	p.problems++
	p.last = now
}

func (c *DynamicCluster) findRemote(partitionKey string, now time.Time) (*memberlist.Node, bool) {
	if c.ml.NumMembers() < 2 {
		log.Printf("Only a single node, do nothing")
		metricForwardResult.WithLabelValues("singleton").Inc()
		return nil, false
	}

	nodeName, ok := c.getNodeForKey(partitionKey)
	if !ok {
		log.Printf("No node found in ring for %s", partitionKey)
		metricForwardResult.WithLabelValues("no_key").Inc()
		return nil, false
	}

	if c.hasProblems(nodeName, now) {
		log.Printf("Node %s has failed recently, using local storage", nodeName)
		metricForwardResult.WithLabelValues("recently_failed").Inc()
		return nil, false
	}

	node := c.memberByName(nodeName)
	if node == nil {
		log.Printf("No node found named %s", nodeName)
		metricForwardResult.WithLabelValues("no_member").Inc()
		return nil, false
	}
	return node, true
}

func (c *DynamicCluster) forwardMetrics(ctx context.Context, p *store.PartitionedMetrics) (ok bool, err error) {
	now := time.Now()

	node, ok := c.findRemote(p.PartitionKey, now)
	if !ok {
		return false, fmt.Errorf("cannot forward")
	}
	if node.Name == c.name {
		return false, nil
	}

	buf := &bytes.Buffer{}
	enc := codec.NewEncoder(buf, msgHandle)

	// write the metric message
	buf.WriteByte(byte(metricMessage))
	if err := enc.Encode(&metricMessageHeader{PartitionKey: p.PartitionKey}); err != nil {
		metricForwardResult.WithLabelValues("encode_header").Inc()
		return false, err
	}
	if err := metricsclient.Write(buf, p.Families); err != nil {
		metricForwardResult.WithLabelValues("encode").Inc()
		return false, fmt.Errorf("unable to write metrics: %v", err)
	}

	metricForwardSamples.Add(float64(metricfamily.MetricsCount(p.Families)))

	if err := c.ml.SendReliable(node, buf.Bytes()); err != nil {
		log.Printf("error: Failed to forward metrics to %s: %v", node, err)
		c.problemDetected(node.Name, now)
		metricForwardResult.WithLabelValues("send").Inc()
		metricForwardLatency.WithLabelValues("send").Observe(time.Since(now).Seconds())
	} else {
		metricForwardLatency.WithLabelValues("").Observe(time.Since(now).Seconds())
	}

	return true, nil
}

// ReadMetrics simply forwards to the underlying store.
func (c *DynamicCluster) ReadMetrics(ctx context.Context, minTimestampMs int64) ([]*store.PartitionedMetrics, error) {
	return c.store.ReadMetrics(ctx, minTimestampMs)
}

// WriteMetrics stores metrics locally if they were meant for this node
// and forwards them to the target node matching the given partition key.
func (c *DynamicCluster) WriteMetrics(ctx context.Context, p *store.PartitionedMetrics) error {
	ok, err := c.forwardMetrics(ctx, p)
	if err != nil {
		// fallthrough to local metrics
		log.Printf("error: Unable to write to remote metrics, falling back to local: %v", err)
		return c.store.WriteMetrics(ctx, p)
	}
	if ok {
		// metrics were forwarded successfully
		metricForwardResult.WithLabelValues("").Inc()
		return nil
	}

	metricForwardResult.WithLabelValues("self").Inc()
	return c.store.WriteMetrics(ctx, p)
}
