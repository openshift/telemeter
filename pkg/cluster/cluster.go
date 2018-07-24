package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/go-msgpack/codec"
	"github.com/hashicorp/memberlist"
	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/serialx/hashring"

	"github.com/openshift/telemeter/pkg/metricsclient"
	"github.com/openshift/telemeter/pkg/transform"
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
	metricForwardLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
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

type LocalStore interface {
	ReadMetrics(ctx context.Context, minTimestampMs int64, fn func(partitionKey string, families []*clientmodel.MetricFamily) error) error
	WriteMetrics(ctx context.Context, partitionKey string, families []*clientmodel.MetricFamily) error
}

type nodeData struct {
	problems int
	last     time.Time
}

type DynamicCluster struct {
	name  string
	store LocalStore
	// skip problematic endpoints for at least this amount of time
	expiration time.Duration

	ml *memberlist.Memberlist

	queue chan ([]byte)

	lock      sync.Mutex
	updated   time.Time
	ring      *hashring.HashRing
	instances map[string]*nodeData
}

func NewDynamic(name string, addr string, secret []byte, store LocalStore, verbose bool) (*DynamicCluster, error) {
	if len(secret) != 32 {
		return nil, fmt.Errorf("invalid secret size, must be 32 bytes: %d", len(secret))
	}

	host, portString, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("address must be a host:port: %v", err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		return nil, fmt.Errorf("address must be a host:port: %v", err)
	}

	c := &DynamicCluster{
		name:       name,
		store:      store,
		expiration: 2 * time.Minute,

		queue:     make(chan []byte, 100),
		instances: make(map[string]*nodeData),
	}

	cfg := memberlist.DefaultWANConfig()
	cfg.DelegateProtocolVersion = protocolVersion
	cfg.DelegateProtocolMax = protocolVersion
	cfg.DelegateProtocolMin = protocolVersion

	cfg.TCPTimeout = 10 * time.Second
	cfg.BindAddr = host
	cfg.BindPort = port
	cfg.AdvertisePort = port

	if !verbose {
		cfg.LogOutput = ioutil.Discard
	}

	cfg.SecretKey = secret
	cfg.Name = name

	cfg.Events = c
	cfg.Delegate = c

	ml, err := memberlist.Create(cfg)
	if err != nil {
		return nil, err
	}

	c.ml = ml

	go func() {
		for data := range c.queue {
			if err := c.handleMessage(data); err != nil {
				log.Printf("error: Unable to handle incoming message: %v", err)
			}
		}
	}()

	return c, nil
}

type MemberInfo struct {
	Name string
	Addr string
}

type debugInfo struct {
	Name            string
	ProtocolVersion int
	Members         []MemberInfo
}

func (c *DynamicCluster) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	info := c.debugInfo()
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(data)
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
			info.Members = append(info.Members, MemberInfo{Name: n.Name, Addr: n.Address()})
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
	return
}

func (c *DynamicCluster) getNodeForKey(partitionKey string) (string, bool) {
	c.refreshRing()
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.ring.GetNode(partitionKey)
}

func (c *DynamicCluster) Join(seeds []string) error {
	_, err := c.ml.Join(seeds)
	return err
}

func (c *DynamicCluster) NotifyJoin(node *memberlist.Node) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.ml == nil {
		c.ring = hashring.New([]string{node.Name})
		return
	}
	log.Printf("[%s] node joined %s", c.name, node.Name)
	c.ring.AddNode(node.Name)
}

func (c *DynamicCluster) NotifyLeave(node *memberlist.Node) {
	c.lock.Lock()
	defer c.lock.Unlock()
	log.Printf("[%s] node left %s", c.name, node.Name)
	c.ring.RemoveNode(node.Name)
}
func (c *DynamicCluster) NotifyUpdate(node *memberlist.Node) {
	log.Printf("[%s] node update %s", c.name, node.Name)
}

func (c *DynamicCluster) NodeMeta(limit int) []byte { return nil }

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

func (c *DynamicCluster) GetBroadcasts(overhead, limit int) [][]byte { return nil }
func (c *DynamicCluster) LocalState(join bool) []byte                { return nil }
func (c *DynamicCluster) MergeRemoteState(buf []byte, join bool)     {}

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
		return c.store.WriteMetrics(context.TODO(), header.PartitionKey, families)

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
	p, ok := c.instances[name]
	if !ok {
		return false
	}
	if p.problems < 4 {
		if now.Sub(p.last) < c.expiration {
			return false
		}
		delete(c.instances, name)
	}
	return true
}

func (c *DynamicCluster) problemDetected(name string, now time.Time) {
	c.lock.Lock()
	defer c.lock.Unlock()
	p, ok := c.instances[name]
	if !ok {
		p = &nodeData{}
		c.instances[name] = p
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

func (c *DynamicCluster) forwardMetrics(ctx context.Context, partitionKey string, families []*clientmodel.MetricFamily) (ok bool, err error) {
	now := time.Now()

	node, ok := c.findRemote(partitionKey, now)
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
	if err := enc.Encode(&metricMessageHeader{PartitionKey: partitionKey}); err != nil {
		metricForwardResult.WithLabelValues("encode_header").Inc()
		return false, err
	}
	if err := metricsclient.Write(buf, families); err != nil {
		metricForwardResult.WithLabelValues("encode").Inc()
		return false, fmt.Errorf("unable to write metrics: %v", err)
	}

	metricForwardSamples.Add(float64(transform.Metrics(families)))

	if err := c.ml.SendReliable(node, buf.Bytes()); err != nil {
		log.Printf("error: Failed to forward metrics to %s: %v", node, err)
		c.problemDetected(node.Name, now)
		metricForwardResult.WithLabelValues("send").Inc()
		metricForwardLatency.WithLabelValues("send").Observe(time.Now().Sub(now).Seconds())
	} else {
		metricForwardLatency.WithLabelValues("").Observe(time.Now().Sub(now).Seconds())
	}

	return true, nil
}

func (c *DynamicCluster) ReadMetrics(ctx context.Context, minTimestampMs int64, fn func(partitionKey string, families []*clientmodel.MetricFamily) error) error {
	return c.store.ReadMetrics(ctx, minTimestampMs, fn)
}

func (c *DynamicCluster) WriteMetrics(ctx context.Context, partitionKey string, families []*clientmodel.MetricFamily) error {
	ok, err := c.forwardMetrics(ctx, partitionKey, families)
	if err != nil {
		// fallthrough to local metrics
		log.Printf("error: Unable to write to remote metrics, falling back to local: %v", err)
		return c.store.WriteMetrics(ctx, partitionKey, families)
	}
	if ok {
		// metrics were forwarded successfully
		metricForwardResult.WithLabelValues("").Inc()
		return nil
	}

	metricForwardResult.WithLabelValues("self").Inc()
	return c.store.WriteMetrics(ctx, partitionKey, families)
}
