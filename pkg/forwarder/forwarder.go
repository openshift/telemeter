package forwarder

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"

	"github.com/smarterclayton/telemeter/pkg/metricsclient"
	"github.com/smarterclayton/telemeter/pkg/transform"
)

type Interface interface {
	Transforms() []transform.Interface
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

type Worker struct {
	FromClient *metricsclient.Client
	ToClient   *metricsclient.Client
	Interval   time.Duration
	Timeout    time.Duration
	MaxBytes   int64

	from      url.URL
	to        *url.URL
	forwarder Interface

	lock        sync.Mutex
	lastMetrics []*clientmodel.MetricFamily
}

func New(from url.URL, to *url.URL, f Interface) *Worker {
	return &Worker{
		from:      from,
		to:        to,
		forwarder: f,
	}
}

func (w *Worker) LastMetrics() []*clientmodel.MetricFamily {
	w.lock.Lock()
	defer w.lock.Unlock()
	return w.lastMetrics
}

func (w *Worker) setLastMetrics(families []*clientmodel.MetricFamily) {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.lastMetrics = families
}

func (w *Worker) Run() {
	if w.Interval == 0 {
		w.Interval = 4*time.Minute + 30*time.Second
	}
	if w.Timeout == 0 {
		w.Timeout = 15 * time.Second
	}
	if w.MaxBytes == 0 {
		w.MaxBytes = 500 * 1024
	}
	if w.FromClient == nil {
		w.FromClient = metricsclient.New(&http.Client{Transport: metricsclient.DefaultTransport()}, w.MaxBytes, w.Timeout, "federate_from")
	}
	if w.ToClient == nil {
		w.ToClient = metricsclient.New(&http.Client{Transport: metricsclient.DefaultTransport()}, w.MaxBytes, w.Timeout, "federate_to")
	}

	ctx := context.Background()
	for {
		// load the match rules each time
		from := w.from
		v := from.Query()
		for _, rule := range w.forwarder.MatchRules() {
			v.Add("match[]", rule)
		}
		from.RawQuery = v.Encode()

		transforms := w.forwarder.Transforms()

		if err := w.forward(ctx, &from, transforms); err != nil {
			gaugeFederateErrors.Inc()
			log.Printf("error: unable to forward results: %v", err)
			time.Sleep(time.Minute)
			continue
		}
		time.Sleep(w.Interval)
	}
}

func (w *Worker) forward(ctx context.Context, from *url.URL, transforms []transform.Interface) error {
	req := &http.Request{Method: "GET", URL: from}
	families, err := w.FromClient.Retrieve(ctx, req)
	if err != nil {
		return err
	}

	before := transform.Metrics(families)
	for _, t := range transforms {
		if err := transform.Filter(families, t); err != nil {
			return err
		}
	}
	families = transform.Pack(families)
	after := transform.Metrics(families)

	gaugeFederateSamples.Set(float64(before))
	gaugeFederateFilteredSamples.Set(float64(before - after))

	w.setLastMetrics(families)

	if len(families) == 0 {
		log.Printf("warning: no metrics to send, doing nothing")
		return nil
	}

	if w.to == nil {
		return nil
	}

	req = &http.Request{Method: "POST", URL: w.to}
	return w.ToClient.Send(ctx, req, families)
}
