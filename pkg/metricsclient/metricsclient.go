package metricsclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/golang/snappy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/openshift/telemeter/pkg/prom"
	"github.com/openshift/telemeter/pkg/reader"
)

var (
	metricsCache = map[string]*Metrics{}
	mu           = sync.RWMutex{}
)

type Metrics struct {
	gaugeRequestRetrieve *prometheus.GaugeVec
	gaugeRequestSend     *prometheus.GaugeVec
}

func newMetrics(reg prometheus.Registerer, client string) *Metrics {
	mu.RLock()
	if m, ok := metricsCache[client]; ok {
		mu.RUnlock()
		return m
	}
	mu.RUnlock()

	reg = prom.WrapRegistererWith(prometheus.Labels{"client": client}, reg)
	m := &Metrics{
		gaugeRequestRetrieve: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "metricsclient_request_retrieve",
			Help: "Tracks the number of metrics retrievals",
		}, []string{"status_code"}),
		gaugeRequestSend: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "metricsclient_request_send",
			Help: "Tracks the number of metrics sends",
		}, []string{"status_code"}),
	}
	mu.Lock()
	metricsCache[client] = m
	mu.Unlock()
	return m
}

type Client struct {
	logger  log.Logger
	metrics *Metrics

	client   *http.Client
	maxBytes int64
	timeout  time.Duration
}

func New(logger log.Logger, reg prometheus.Registerer, client *http.Client, maxBytes int64, timeout time.Duration, clientName string) *Client {
	return &Client{
		logger:  log.With(logger, "component", "metricsclient"),
		metrics: newMetrics(reg, clientName),

		client:   client,
		maxBytes: maxBytes,
		timeout:  timeout,
	}
}

func (c *Client) Retrieve(ctx context.Context, req *http.Request) ([]*clientmodel.MetricFamily, error) {
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Accept", strings.Join([]string{string(expfmt.FmtProtoDelim), string(expfmt.FmtText)}, " , "))

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	req = req.WithContext(ctx)
	defer cancel()

	families := make([]*clientmodel.MetricFamily, 0, 100)
	err := withCancel(ctx, c.client, req, func(resp *http.Response) error {
		switch resp.StatusCode {
		case http.StatusOK:
			c.metrics.gaugeRequestRetrieve.WithLabelValues("200").Inc()
		case http.StatusUnauthorized:
			c.metrics.gaugeRequestRetrieve.WithLabelValues("401").Inc()
			return fmt.Errorf("Prometheus server requires authentication: %s", resp.Request.URL)
		case http.StatusForbidden:
			c.metrics.gaugeRequestRetrieve.WithLabelValues("403").Inc()
			return fmt.Errorf("Prometheus server forbidden: %s", resp.Request.URL)
		case http.StatusBadRequest:
			c.metrics.gaugeRequestRetrieve.WithLabelValues("400").Inc()
			return fmt.Errorf("bad request: %s", resp.Request.URL)
		default:
			c.metrics.gaugeRequestRetrieve.WithLabelValues(strconv.Itoa(resp.StatusCode)).Inc()
			return fmt.Errorf("Prometheus server reported unexpected error code: %d", resp.StatusCode)
		}

		// read the response into memory
		format := expfmt.ResponseFormat(resp.Header)
		r := &reader.LimitedReader{R: resp.Body, N: c.maxBytes}
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

		return nil
	})
	if err != nil {
		return nil, err
	}
	return families, nil
}

func (c *Client) Send(ctx context.Context, req *http.Request, families []*clientmodel.MetricFamily) error {
	buf := &bytes.Buffer{}
	if err := Write(buf, families); err != nil {
		return err
	}

	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Content-Type", string(expfmt.FmtProtoDelim))
	req.Header.Set("Content-Encoding", "snappy")
	req.Body = ioutil.NopCloser(buf)

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	req = req.WithContext(ctx)
	defer cancel()

	return withCancel(ctx, c.client, req, func(resp *http.Response) error {
		defer func() {
			if _, err := io.Copy(ioutil.Discard, resp.Body); err != nil {
				level.Error(c.logger).Log("msg", "error copying body", "err", err)
			}
			resp.Body.Close()
		}()

		switch resp.StatusCode {
		case http.StatusOK:
			c.metrics.gaugeRequestSend.WithLabelValues("200").Inc()
		case http.StatusUnauthorized:
			c.metrics.gaugeRequestSend.WithLabelValues("401").Inc()
			return fmt.Errorf("gateway server requires authentication: %s", resp.Request.URL)
		case http.StatusForbidden:
			c.metrics.gaugeRequestSend.WithLabelValues("403").Inc()
			return fmt.Errorf("gateway server forbidden: %s", resp.Request.URL)
		case http.StatusBadRequest:
			c.metrics.gaugeRequestSend.WithLabelValues("400").Inc()
			return fmt.Errorf("gateway server bad request: %s", resp.Request.URL)
		default:
			c.metrics.gaugeRequestSend.WithLabelValues(strconv.Itoa(resp.StatusCode)).Inc()
			body, _ := ioutil.ReadAll(resp.Body)
			if len(body) > 1024 {
				body = body[:1024]
			}
			return fmt.Errorf("gateway server reported unexpected error code: %d: %s", resp.StatusCode, string(body))
		}

		return nil
	})
}

func Read(r io.Reader) ([]*clientmodel.MetricFamily, error) {
	decompress := snappy.NewReader(r)
	decoder := expfmt.NewDecoder(decompress, expfmt.FmtProtoDelim)
	families := make([]*clientmodel.MetricFamily, 0, 100)
	for {
		family := &clientmodel.MetricFamily{}
		if err := decoder.Decode(family); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		families = append(families, family)
	}
	return families, nil
}

func Write(w io.Writer, families []*clientmodel.MetricFamily) error {
	// output the filtered set
	compress := snappy.NewBufferedWriter(w)
	encoder := expfmt.NewEncoder(compress, expfmt.FmtProtoDelim)
	for _, family := range families {
		if family == nil {
			continue
		}
		if err := encoder.Encode(family); err != nil {
			return err
		}
	}
	if err := compress.Flush(); err != nil {
		return err
	}
	return nil
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
		closeErr := resp.Body.Close()

		// wait for the goroutine to finish.
		<-done

		// err is propagated from the goroutine above
		// if it is nil, we bubble up the close err, if any.
		if err == nil {
			err = closeErr
		}

		// if there is no close err,
		// we propagate the context context error.
		if err == nil {
			err = ctx.Err()
		}
	case <-done:
		// propagate the err from the spawned goroutine, if any.
	}

	return err
}

func DefaultTransport() *http.Transport {
	return &http.Transport{
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   true,
	}
}
