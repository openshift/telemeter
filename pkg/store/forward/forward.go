package forward

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/prompb"

	"github.com/openshift/telemeter/pkg/store"
)

const (
	nameLabelName = "__name__"
)

var (
	forwardSamples = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "telemeter_v1_forward_samples_total",
		Help: "Total amount of successfully forwarded samples from v1 requests.",
	})
	forwardRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "telemeter_v1_forward_requests_total",
		Help: "Total amount of forwarded v1 requests.",
	}, []string{"result"})
	forwardDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "telemeter_v1_forward_request_duration_seconds",
		Help:    "Tracks the duration of all requests forwarded v1.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}, // max = timeout
	}, []string{"status_code"})
	overwrittenTimestamps = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "telemeter_v1_forward_overwritten_timestamps_total",
		Help: "Total number of timestamps from v1 requests that were overwritten.",
	})
)

func init() {
	prometheus.MustRegister(forwardSamples)
	prometheus.MustRegister(forwardRequests)
	prometheus.MustRegister(forwardDuration)
	prometheus.MustRegister(overwrittenTimestamps)
}

type Store struct {
	next   store.Store
	url    *url.URL
	client *http.Client
	logger log.Logger
}

func New(logger log.Logger, url *url.URL, next store.Store) *Store {
	return &Store{
		next:   next,
		url:    url,
		client: &http.Client{},
		logger: log.With(logger, "component", "store/forward"),
	}
}

func (s *Store) ReadMetrics(ctx context.Context, minTimestampMs int64) ([]*store.PartitionedMetrics, error) {
	return s.next.ReadMetrics(ctx, minTimestampMs)
}

func (s *Store) WriteMetrics(ctx context.Context, p *store.PartitionedMetrics) error {
	if p == nil {
		return nil
	}

	err := func() error {
		timeseries, err := convertToTimeseries(p, time.Now())
		if err != nil {
			return err
		}

		if len(timeseries) == 0 {
			level.Info(s.logger).Log("msg", "no time series to forward to receive endpoint")
			return nil
		}

		wreq := &prompb.WriteRequest{
			Timeseries: timeseries,
		}

		data, err := proto.Marshal(wreq)
		if err != nil {
			return err
		}

		compressed := snappy.Encode(nil, data)

		req, err := http.NewRequest(http.MethodPost, s.url.String(), bytes.NewBuffer(compressed))
		if err != nil {
			return err
		}
		req.Header.Add("THANOS-TENANT", p.PartitionKey)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req = req.WithContext(ctx)

		begin := time.Now()
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}

		forwardDuration.
			WithLabelValues(fmt.Sprintf("%d", resp.StatusCode)).
			Observe(time.Since(begin).Seconds())

		meanDrift := timeseriesMeanDrift(timeseries, time.Now().Unix())
		if math.Abs(meanDrift) > 10 {
			level.Info(s.logger).Log("msg", "mean drift from now for clusters", "partitionkey", p.PartitionKey, "drift", fmt.Sprintf("%.3fs", meanDrift))
		}

		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("response status code is %s", resp.Status)
		}

		s := 0
		for _, ts := range wreq.Timeseries {
			s = s + len(ts.Samples)
		}
		forwardSamples.Add(float64(s))

		return nil
	}()
	if err != nil {
		forwardRequests.WithLabelValues("error").Inc()
		level.Error(s.logger).Log("msg", "forwarding error", "err", err)

		return err
	}

	forwardRequests.WithLabelValues("success").Inc()
	return s.next.WriteMetrics(ctx, p)
}

func convertToTimeseries(p *store.PartitionedMetrics, now time.Time) ([]prompb.TimeSeries, error) {
	var timeseries []prompb.TimeSeries

	timestamp := now.UnixNano() / int64(time.Millisecond)
	for _, f := range p.Families {
		for _, m := range f.Metric {
			var ts prompb.TimeSeries

			labelpairs := []prompb.Label{{
				Name:  nameLabelName,
				Value: *f.Name,
			}}

			for _, l := range m.Label {
				labelpairs = append(labelpairs, prompb.Label{
					Name:  *l.Name,
					Value: *l.Value,
				})
			}

			s := prompb.Sample{
				Timestamp: *m.TimestampMs,
			}
			// If the sample is in the future, overwrite it.
			if *m.TimestampMs > timestamp {
				s.Timestamp = timestamp
				overwrittenTimestamps.Inc()
			}

			switch *f.Type {
			case clientmodel.MetricType_COUNTER:
				s.Value = *m.Counter.Value
			case clientmodel.MetricType_GAUGE:
				s.Value = *m.Gauge.Value
			case clientmodel.MetricType_UNTYPED:
				s.Value = *m.Untyped.Value
			default:
				return nil, fmt.Errorf("metric type %s not supported", f.Type.String())
			}

			ts.Labels = append(ts.Labels, labelpairs...)
			ts.Samples = append(ts.Samples, s)

			timeseries = append(timeseries, ts)
		}
	}

	return timeseries, nil
}

func timeseriesMeanDrift(ts []prompb.TimeSeries, timestampSeconds int64) float64 {
	var count float64
	var sum float64

	for _, t := range ts {
		for _, s := range t.Samples {
			sum = sum + (float64(timestampSeconds) - float64(s.Timestamp/1000))
			count++
		}
	}

	return sum / count
}
