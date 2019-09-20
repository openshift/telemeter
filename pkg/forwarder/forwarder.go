package forwarder

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"

	"github.com/openshift/telemeter/pkg/authorize"
	telemeterhttp "github.com/openshift/telemeter/pkg/http"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/metricsclient"
)

type RuleMatcher interface {
	MatchRules() []string
}

var (
	gaugeFederateSamples = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "federate_samples",
		Help: "Tracks the number of samples per federation",
	})
	gaugeFederateFilteredSamples = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "federate_filtered_samples",
		Help: "Tracks the number of samples filtered per federation",
	})
	gaugeFederateErrors = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "federate_errors",
		Help: "The number of times forwarding federated metrics has failed",
	})
)

func init() {
	prometheus.MustRegister(
		gaugeFederateErrors, gaugeFederateSamples, gaugeFederateFilteredSamples,
	)
}

// Config defines the parameters that can be used to configure a worker.
// The only required field is `From`.
type Config struct {
	From          *url.URL
	ToAuthorize   *url.URL
	ToUpload      *url.URL
	FromToken     string
	ToToken       string
	FromTokenFile string
	ToTokenFile   string
	FromCAFile    string

	AnonymizeLabels   []string
	AnonymizeSalt     string
	AnonymizeSaltFile string
	Debug             bool
	Interval          time.Duration
	LimitBytes        int64
	Rules             []string
	RulesFile         string
	Transformer       metricfamily.Transformer

	Logger log.Logger
}

// Worker represents a metrics forwarding agent. It collects metrics from a source URL and forwards them to a sink.
// A Worker should be configured with a `Config` and instantiated with the `New` func.
// Workers are thread safe; all access to shared fields are synchronized.
type Worker struct {
	fromClient *metricsclient.Client
	toClient   *metricsclient.Client
	from       *url.URL
	to         *url.URL

	interval    time.Duration
	transformer metricfamily.Transformer
	rules       []string

	lastMetrics []*clientmodel.MetricFamily
	lock        sync.Mutex
	reconfigure chan struct{}

	logger log.Logger
}

// New creates a new Worker based on the provided Config. If the Config contains invalid
// values, then an error is returned.
func New(cfg Config) (*Worker, error) {
	if cfg.From == nil {
		return nil, errors.New("a URL from which to scrape is required")
	}
	logger := log.With(cfg.Logger, "component", "forwarder")
	w := Worker{
		from:        cfg.From,
		interval:    cfg.Interval,
		reconfigure: make(chan struct{}),
		to:          cfg.ToUpload,
		logger:      log.With(cfg.Logger, "component", "forwarder/worker"),
	}

	if w.interval == 0 {
		w.interval = 4*time.Minute + 30*time.Second
	}

	// Configure the anonymization.
	anonymizeSalt := cfg.AnonymizeSalt
	if len(cfg.AnonymizeSalt) == 0 && len(cfg.AnonymizeSaltFile) > 0 {
		data, err := ioutil.ReadFile(cfg.AnonymizeSaltFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read anonymize-salt-file: %v", err)
		}
		anonymizeSalt = strings.TrimSpace(string(data))
	}
	if len(cfg.AnonymizeLabels) != 0 && len(anonymizeSalt) == 0 {
		return nil, fmt.Errorf("anonymize-salt must be specified if anonymize-labels is set")
	}
	if len(cfg.AnonymizeLabels) == 0 {
		level.Warn(logger).Log("msg", "not anonymizing any labels")
	}

	// Configure a transformer.
	var transformer metricfamily.MultiTransformer
	if cfg.Transformer != nil {
		transformer.With(cfg.Transformer)
	}
	if len(cfg.AnonymizeLabels) > 0 {
		transformer.With(metricfamily.NewMetricsAnonymizer(anonymizeSalt, cfg.AnonymizeLabels, nil))
	}

	// Create the `fromClient`.
	fromTransport := metricsclient.DefaultTransport()
	if len(cfg.FromCAFile) > 0 {
		if fromTransport.TLSClientConfig == nil {
			fromTransport.TLSClientConfig = &tls.Config{}
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("failed to read system certificates: %v", err)
		}
		data, err := ioutil.ReadFile(cfg.FromCAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read from-ca-file: %v", err)
		}
		if !pool.AppendCertsFromPEM(data) {
			level.Warn(logger).Log("msg", "no certs found in from-ca-file")
		}
		fromTransport.TLSClientConfig.RootCAs = pool
	}
	fromClient := &http.Client{Transport: fromTransport}
	if cfg.Debug {
		fromClient.Transport = telemeterhttp.NewDebugRoundTripper(logger, fromClient.Transport)
	}
	if len(cfg.FromToken) == 0 && len(cfg.FromTokenFile) > 0 {
		data, err := ioutil.ReadFile(cfg.FromTokenFile)
		if err != nil {
			return nil, fmt.Errorf("unable to read from-token-file: %v", err)
		}
		cfg.FromToken = strings.TrimSpace(string(data))
	}
	if len(cfg.FromToken) > 0 {
		fromClient.Transport = telemeterhttp.NewBearerRoundTripper(cfg.FromToken, fromClient.Transport)
	}
	w.fromClient = metricsclient.New(logger, fromClient, cfg.LimitBytes, w.interval, "federate_from")

	// Create the `toClient`.
	toTransport := metricsclient.DefaultTransport()
	toTransport.Proxy = http.ProxyFromEnvironment
	toClient := &http.Client{Transport: toTransport}
	if cfg.Debug {
		toClient.Transport = telemeterhttp.NewDebugRoundTripper(logger, toClient.Transport)
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
	if len(cfg.ToToken) > 0 {
		// Exchange our token for a token from the authorize endpoint, which also gives us a
		// set of expected labels we must include.
		rt := authorize.NewServerRotatingRoundTripper(cfg.ToToken, cfg.ToAuthorize, toClient.Transport)
		toClient.Transport = rt
		transformer.With(metricfamily.NewLabel(nil, rt))
	}
	w.toClient = metricsclient.New(logger, toClient, cfg.LimitBytes, w.interval, "federate_to")
	w.transformer = transformer

	// Configure the matching rules.
	rules := cfg.Rules
	if len(cfg.RulesFile) > 0 {
		data, err := ioutil.ReadFile(cfg.RulesFile)
		if err != nil {
			return nil, fmt.Errorf("unable to read match-file: %v", err)
		}
		rules = append(rules, strings.Split(string(data), "\n")...)
	}
	for i := 0; i < len(rules); {
		s := strings.TrimSpace(rules[i])
		if len(s) == 0 {
			rules = append(rules[:i], rules[i+1:]...)
			continue
		}
		rules[i] = s
		i++
	}
	w.rules = rules

	return &w, nil
}

// Reconfigure temporarily stops a worker and reconfigures is with the provided Config.
// Is thread safe and can run concurrently with `LastMetrics` and `Run`.
func (w *Worker) Reconfigure(cfg Config) error {
	worker, err := New(cfg)
	if err != nil {
		return fmt.Errorf("failed to reconfigure: %v", err)
	}

	w.lock.Lock()
	defer w.lock.Unlock()

	w.fromClient = worker.fromClient
	w.toClient = worker.toClient
	w.interval = worker.interval
	w.from = worker.from
	w.to = worker.to
	w.transformer = worker.transformer
	w.rules = worker.rules

	// Signal a restart to Run func.
	// Do this in a goroutine since we do not care if restarting the Run loop is asynchronous.
	go func() { w.reconfigure <- struct{}{} }()
	return nil
}

func (w *Worker) LastMetrics() []*clientmodel.MetricFamily {
	w.lock.Lock()
	defer w.lock.Unlock()
	return w.lastMetrics
}

func (w *Worker) Run(ctx context.Context) {
	for {
		// Ensure that the Worker does not access critical configuration during a reconfiguration.
		w.lock.Lock()
		wait := w.interval
		// The critical section ends here.
		w.lock.Unlock()

		if err := w.forward(ctx); err != nil {
			gaugeFederateErrors.Inc()
			level.Error(w.logger).Log("msg", "unable to forward results", "err", err)
			wait = time.Minute
		}

		select {
		// If the context is cancelled, then we're done.
		case <-ctx.Done():
			return
		case <-time.After(wait):
		// We want to be able to interrupt a sleep to immediately apply a new configuration.
		case <-w.reconfigure:
		}
	}
}

func (w *Worker) forward(ctx context.Context) error {
	w.lock.Lock()
	defer w.lock.Unlock()

	// Load the match rules each time.
	from := w.from

	// reset query from last invocation, otherwise match rules will be appended
	w.from.RawQuery = ""
	v := from.Query()
	for _, rule := range w.rules {
		v.Add("match[]", rule)
	}
	from.RawQuery = v.Encode()

	req := &http.Request{Method: "GET", URL: from}
	families, err := w.fromClient.Retrieve(ctx, req)
	if err != nil {
		return err
	}

	before := metricfamily.MetricsCount(families)
	if err := metricfamily.Filter(families, w.transformer); err != nil {
		return err
	}

	families = metricfamily.Pack(families)
	after := metricfamily.MetricsCount(families)

	gaugeFederateSamples.Set(float64(before))
	gaugeFederateFilteredSamples.Set(float64(before - after))

	w.lastMetrics = families

	if len(families) == 0 {
		level.Warn(w.logger).Log("msg", "no metrics to send, doing nothing")
		return nil
	}

	if w.to == nil {
		return nil
	}

	req = &http.Request{Method: "POST", URL: w.to}
	return w.toClient.Send(ctx, req, families)
}
