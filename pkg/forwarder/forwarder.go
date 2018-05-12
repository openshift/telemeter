package forwarder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/smarterclayton/telemeter/pkg/reader"
	"github.com/smarterclayton/telemeter/pkg/transform"
)

type Interface interface {
	Transforms() []transform.Interface
	MatchRules() []string
}

var (
	gaugeFederateRequests = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "federate_requests",
		Help: "Tracks the number of federation requests",
	}, []string{"status_code"})
	gaugeFederateRequestBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "federate_request_bytes",
		Help: "The number of bytes returned by the last federate call",
	})
	gaugeFederateSamples = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "federate_samples",
		Help: "Tracks the number of samples per federation",
	})
	gaugeFederateFilteredSamples = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "federate_filtered_samples",
		Help: "Tracks the number of samples filtered per federation",
	})
	gaugePushRequests = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "push_requests",
		Help: "Tracks the number of pushes",
	}, []string{"status_code"})
	gaugeFederateErrors = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "federate_errors",
		Help: "The number of times forwarding federated metrics has failed",
	})
	gaugeFederateTransformedResponseBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "federate_transformed_response_bytes",
		Help: "Tracks the transformed data returned by federate requests",
	})
)

func init() {
	prometheus.MustRegister(
		gaugeFederateRequests, gaugePushRequests,
		gaugeFederateRequestBytes,
		gaugeFederateErrors,
		gaugeFederateSamples, gaugeFederateFilteredSamples,
		gaugeFederateTransformedResponseBytes,
	)
}

type Worker struct {
	FromClient *http.Client
	ToClient   *http.Client
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

func (w *Worker) setLastMetrics(mf []*clientmodel.MetricFamily) {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.lastMetrics = mf
}

func (w *Worker) Run() {
	if w.FromClient == nil {
		w.FromClient = &http.Client{Transport: DefaultTransport()}
	}
	if w.ToClient == nil {
		w.ToClient = &http.Client{Transport: DefaultTransport()}
	}
	if w.Interval == 0 {
		w.Interval = 4*time.Minute + 30*time.Second
	}
	if w.Timeout == 0 {
		w.Timeout = 15 * time.Second
	}
	if w.MaxBytes == 0 {
		w.MaxBytes = 500 * 1024
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
	families, err := fetch(ctx, from, w.FromClient, w.MaxBytes, w.Timeout)
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

	return send(ctx, w.to, w.ToClient, w.MaxBytes, w.Timeout, families)
}

func fetch(ctx context.Context, u *url.URL, client *http.Client, maxBytes int64, timeout time.Duration) ([]*clientmodel.MetricFamily, error) {
	req := &http.Request{}
	req.Method = "GET"
	req.Header = make(http.Header)
	req.Header.Add("Accept", strings.Join([]string{string(expfmt.FmtProtoDelim), string(expfmt.FmtText)}, " , "))
	req.URL = u
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	req = req.WithContext(ctx)
	defer cancel()

	families := make([]*clientmodel.MetricFamily, 0, 100)
	err := withCancel(ctx, client, req, func(resp *http.Response) error {
		switch resp.StatusCode {
		case http.StatusOK:
			gaugeFederateRequests.WithLabelValues("200").Inc()
		case http.StatusUnauthorized:
			gaugeFederateRequests.WithLabelValues("401").Inc()
			return fmt.Errorf("server requires authentication: %s", resp.Request.URL)
		case http.StatusForbidden:
			gaugeFederateRequests.WithLabelValues("403").Inc()
			return fmt.Errorf("server forbidden: %s", resp.Request.URL)
		case http.StatusBadRequest:
			gaugeFederateRequests.WithLabelValues("400").Inc()
			return fmt.Errorf("bad request: %s", resp.Request.URL)
		default:
			gaugeFederateRequests.WithLabelValues(strconv.Itoa(resp.StatusCode)).Inc()
			return fmt.Errorf("server reported unexpected error code: %d", resp.StatusCode)
		}

		// read the response into memory
		format := expfmt.ResponseFormat(resp.Header)
		r := &reader.LimitedReader{R: resp.Body, N: maxBytes}
		decoder := expfmt.NewDecoder(r, format)
		for {
			family := &clientmodel.MetricFamily{}
			families = append(families, family)
			if err := decoder.Decode(family); err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
		}

		gaugeFederateRequestBytes.Set(float64(maxBytes - r.N))

		return nil
	})
	if err != nil {
		return nil, err
	}
	return families, nil
}

func send(ctx context.Context, u *url.URL, client *http.Client, maxBytes int64, timeout time.Duration, families []*clientmodel.MetricFamily) error {
	r, err := write(families)
	if err != nil {
		return err
	}

	req := &http.Request{}
	req.Method = "POST"
	req.Header = make(http.Header)
	req.Header.Add("Content-Type", string(expfmt.FmtProtoDelim))
	req.Header.Add("Content-Encoding", "snappy")
	req.URL = u
	req.Body = ioutil.NopCloser(r)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	req = req.WithContext(ctx)
	defer cancel()

	gaugeFederateTransformedResponseBytes.Set(float64(r.Len()))

	return withCancel(ctx, client, req, func(resp *http.Response) error {
		defer func() {
			io.Copy(ioutil.Discard, resp.Body)
			resp.Body.Close()
		}()

		switch resp.StatusCode {
		case http.StatusOK:
			gaugePushRequests.WithLabelValues("200").Inc()
		case http.StatusUnauthorized:
			gaugePushRequests.WithLabelValues("401").Inc()
			return fmt.Errorf("push server requires authentication: %s", resp.Request.URL)
		case http.StatusForbidden:
			gaugePushRequests.WithLabelValues("403").Inc()
			return fmt.Errorf("push server forbidden: %s", resp.Request.URL)
		case http.StatusBadRequest:
			gaugePushRequests.WithLabelValues("400").Inc()
			return fmt.Errorf("push bad request: %s", resp.Request.URL)
		default:
			gaugePushRequests.WithLabelValues(strconv.Itoa(resp.StatusCode)).Inc()
			body, _ := ioutil.ReadAll(resp.Body)
			if len(body) > 1024 {
				body = body[:1024]
			}
			return fmt.Errorf("push server reported unexpected error code: %d: %s", resp.StatusCode, string(body))
		}

		return nil
	})
}

func write(families []*clientmodel.MetricFamily) (*bytes.Buffer, error) {
	// output the filtered set
	buf := &bytes.Buffer{}
	compress := snappy.NewWriter(buf)
	encoder := expfmt.NewEncoder(compress, expfmt.FmtProtoDelim)
	for _, family := range families {
		if family == nil {
			continue
		}
		if err := encoder.Encode(family); err != nil {
			return nil, err
		}
	}

	if err := compress.Flush(); err != nil {
		return nil, err
	}
	return buf, nil
}

func withCancel(ctx context.Context, client *http.Client, req *http.Request, fn func(*http.Response) error) error {
	resp, err := client.Do(req)
	defer func() {
		if resp != nil {
			resp.Body.Close()
		}
	}()
	if err != nil {
		return err
	}

	done := make(chan struct{})
	go func() {
		err = fn(resp)
		close(done)
	}()

	select {
	case <-ctx.Done():
		err = resp.Body.Close()
		<-done
		if err == nil {
			err = ctx.Err()
		}
	case <-done:
	}

	return err
}

func DefaultTransport() http.RoundTripper {
	var rt http.RoundTripper = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return rt
}
