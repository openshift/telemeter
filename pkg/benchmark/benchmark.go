package benchmark

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	uuid "github.com/satori/go.uuid"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/metricsclient"
)

const (
	DefaultSyncPeriod = 4*time.Minute + 30*time.Second
	LimitBytes        = 200 * 1024
)

var (
	forwardErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "forward_errors",
		Help: "The number of times forwarding federated metrics has failed",
	})
	forwardedSamples = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "forwarded_samples",
		Help: "The total number of forwarded samples for all time series",
	})
)

func init() {
	prometheus.MustRegister(
		forwardErrors,
		forwardedSamples,
	)
}

type Benchmark struct {
	cancel      context.CancelFunc
	lock        sync.Mutex
	reconfigure chan struct{}
	running     bool
	workers     []*worker
}

// Config defines the parameters that can be used to configure a worker.
// The only required field is `From`.
type Config struct {
	ToAuthorize *url.URL
	ToUpload    *url.URL
	ToCAFile    string
	ToToken     string
	ToTokenFile string
	Interval    time.Duration
	MetricsFile string
	Workers     int
}

// worker represents a metrics forwarding agent. It collects metrics from a source URL and forwards them to a sink.
// A worker should be configured with a `Config` and instantiated with the `New` func.
// workers are thread safe; all access to shared fields is synchronized.
type worker struct {
	client      *metricsclient.Client
	id          string
	interval    time.Duration
	metrics     []*clientmodel.MetricFamily
	to          *url.URL
	transformer metricfamily.Transformer
}

// New creates a new Benchmark based on the provided Config. If the Config contains invalid
// values, then an error is returned.
func New(cfg *Config) (*Benchmark, error) {
	b := Benchmark{
		reconfigure: make(chan struct{}),
		workers:     make([]*worker, cfg.Workers),
	}

	interval := cfg.Interval
	if interval == 0 {
		interval = DefaultSyncPeriod
	}

	if len(cfg.ToToken) == 0 && len(cfg.ToTokenFile) > 0 {
		data, err := ioutil.ReadFile(cfg.ToTokenFile)
		if err != nil {
			return nil, fmt.Errorf("unable to read to-token-file: %v", err)
		}
		cfg.ToToken = strings.TrimSpace(string(data))
	}
	if (len(cfg.ToToken) > 0) != (cfg.ToAuthorize != nil) {
		return nil, errors.New("an authorization URL and authorization token must both specified or empty")
	}

	f, err := os.Open(cfg.MetricsFile)
	if err != nil {
		return nil, fmt.Errorf("unable to read metrics-file: %v", err)
	}

	var pool *x509.CertPool
	if len(cfg.ToCAFile) > 0 {
		pool, err = x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("failed to read system certificates: %v", err)
		}
		data, err := ioutil.ReadFile(cfg.ToCAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read to-ca-file: %v", err)
		}
		if !pool.AppendCertsFromPEM(data) {
			log.Printf("warning: no certs found in to-ca-file")
		}
	}

	for i := range b.workers {
		w := &worker{
			id:       uuid.Must(uuid.NewV4()).String(),
			interval: interval,
			to:       cfg.ToUpload,
		}

		if _, err := f.Seek(0, 0); err != nil {
			return nil, fmt.Errorf("failed to rewind file: %v", err)
		}
		dec := expfmt.NewDecoder(f, expfmt.FmtText)
		for {
			var m clientmodel.MetricFamily
			err := dec.Decode(&m)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("unable to parse metrics: %v", err)
			}
			w.metrics = append(w.metrics, &m)
		}

		transport := metricsclient.DefaultTransport()
		if pool != nil {
			if transport.TLSClientConfig == nil {
				transport.TLSClientConfig = &tls.Config{}
			}
			transport.TLSClientConfig.RootCAs = pool
		}
		client := &http.Client{Transport: transport}
		transformer := metricfamily.MultiTransformer{}
		if len(cfg.ToToken) > 0 {
			u, err := url.Parse(cfg.ToAuthorize.String())
			if err != nil {
				panic(err)
			}
			q := u.Query()
			q.Add("id", w.id)
			u.RawQuery = q.Encode()

			// Exchange our token for a token from the authorize endpoint, which also gives us a
			// set of expected labels we must include.
			rt := authorize.NewServerRotatingRoundTripper(cfg.ToToken, u, client.Transport)
			client.Transport = rt
			transformer.With(metricfamily.NewLabel(nil, rt))
		}
		w.client = metricsclient.New(client, LimitBytes, w.interval, "federate_to")
		w.transformer = transformer
		b.workers[i] = w
	}

	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("failed to close file: %v", err)
	}

	return &b, nil
}

// Run starts a Benchmark instance.
func (b *Benchmark) Run() {
	b.lock.Lock()
	r := b.running
	b.lock.Unlock()
	if r {
		return
	}

	for {
		var wg sync.WaitGroup
		done := make(chan struct{})
		b.lock.Lock()
		b.running = true
		ctx, cancel := context.WithCancel(context.Background())
		b.cancel = cancel
		for i, w := range b.workers {
			wg.Add(1)
			go func(i int, w *worker) {
				log.Printf("Started worker %d of %d: %s", i+1, len(b.workers), w.id)
				select {
				case <-time.After(time.Duration(rand.Int63n(int64(w.interval)))):
					w.run(ctx)
				case <-ctx.Done():
				}
				wg.Done()
			}(i, w)
		}
		b.lock.Unlock()
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			return
		case <-b.reconfigure:
			log.Print("Restarting workers...")
			continue
		}
	}
}

// Stop will pause a Benchmark instance.
func (b *Benchmark) Stop() {
	b.lock.Lock()
	defer b.lock.Unlock()
	if b.running {
		b.cancel()
		b.running = false
	}
}

// Reconfigure reconfigures an existing Benchmark instnace.
func (b *Benchmark) Reconfigure(cfg *Config) error {
	benchmark, err := New(cfg)
	if err != nil {
		return fmt.Errorf("failed to reconfigure: %v", err)
	}

	b.lock.Lock()
	defer b.lock.Unlock()

	if b.running {
		b.reconfigure <- struct{}{}
		b.cancel()
	}
	b.workers = benchmark.workers
	return nil
}

func (w *worker) run(ctx context.Context) {
	for {
		m := w.generate()
		wait := w.interval
		if err := w.forward(ctx, m); err != nil {
			forwardErrors.Inc()
			log.Printf("error from worker %s: unable to forward results: %v", w.id, err)
			wait = time.Minute
		}
		var n int
		for i := range m {
			n += len(m[i].Metric)
		}
		forwardedSamples.Add(float64(n))
		select {
		// If the context is cancelled, then we're done.
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (w *worker) generate() []*clientmodel.MetricFamily {
	rand.Seed(time.Now().UnixNano())
	mfs := make([]*clientmodel.MetricFamily, len(w.metrics))
	now := time.Now().UnixNano() / int64(time.Millisecond)
	for i := range w.metrics {
		mf := *w.metrics[i]
		mf.Metric = make([]*clientmodel.Metric, len(w.metrics[i].Metric))
		for j := range w.metrics[i].Metric {
			m := randomize(w.metrics[i].Metric[j])
			ts := now - rand.Int63n(int64(w.interval/time.Millisecond))
			m.TimestampMs = &ts
			mf.Metric[j] = m
		}
		// Sort the time series within the metric family by timestamp so Prometheus will accept them.
		sort.Slice(mf.Metric, func(i, j int) bool {
			return mf.Metric[i].GetTimestampMs() < mf.Metric[j].GetTimestampMs()
		})
		mfs[i] = &mf
	}
	return mfs
}

// randomize copies and randomizes the values of a metric.
func randomize(metric *clientmodel.Metric) *clientmodel.Metric {
	m := *metric
	if m.GetUntyped() != nil {
		v := *m.GetUntyped()
		f := math.Round(rand.Float64() * v.GetValue())
		v.Value = &f
		m.Untyped = &v
	}
	if m.GetGauge() != nil {
		v := *m.GetGauge()
		f := math.Round(rand.Float64() * v.GetValue())
		v.Value = &f
		m.Gauge = &v
	}
	if m.GetCounter() != nil {
		if rand.Intn(2) == 1 {
			v := *m.GetCounter()
			f := v.GetValue() + 1
			v.Value = &f
			m.Counter = &v
		}
	}
	return &m
}

func (w *worker) forward(ctx context.Context, metrics []*clientmodel.MetricFamily) error {
	if w.to == nil {
		log.Printf("warning from worker %s: no destination configured; doing nothing", w.id)
		return nil
	}
	if err := metricfamily.Filter(metrics, w.transformer); err != nil {
		return err
	}
	if len(metrics) == 0 {
		log.Printf("warning from worker %s: no metrics to send; doing nothing", w.id)
		return nil
	}

	req := &http.Request{Method: "POST", URL: w.to}
	return w.client.Send(ctx, req, metrics)
}
