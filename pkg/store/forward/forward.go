package forward

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"time"

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

type Store struct {
	next   store.Store
	url    *url.URL
	client *http.Client

	forwardSamples        prometheus.Counter
	forwardErrors         prometheus.Counter
	forwardDuration       *prometheus.HistogramVec
	overwrittenTimestamps prometheus.Counter
}

func New(reg *prometheus.Registry, url *url.URL, next store.Store) *Store {
	s := &Store{
		next:   next,
		url:    url,
		client: &http.Client{},

		forwardSamples: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "telemeter_forward_samples_total",
			Help: "Total amount of samples successfully forwarded",
		}),
		forwardErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "telemeter_forward_request_errors_total",
			Help: "Total amount of errors encountered while forwarding",
		}),
		forwardDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "telemeter_forward_request_duration_seconds",
			Help:    "Tracks the duration of all forwarding requests",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}, // max = timeout
		}, []string{"status_code"}),
		overwrittenTimestamps: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "telemeter_overwritten_timestamps_total",
			Help: "Total number of timestamps that were overwritten",
		}),
	}

	reg.MustRegister(
		s.forwardSamples,
		s.forwardErrors,
		s.forwardDuration,
		s.overwrittenTimestamps,
	)

	return s
}

func (s *Store) ReadMetrics(ctx context.Context, minTimestampMs int64) ([]*store.PartitionedMetrics, error) {
	return s.next.ReadMetrics(ctx, minTimestampMs)
}

func (s *Store) WriteMetrics(ctx context.Context, p *store.PartitionedMetrics) error {
	if p == nil {
		return nil
	}

	go func() {
		// Run in a func to catch all transient errors
		err := func() error {
			timeseries, err := convertToTimeseries(s.overwrittenTimestamps, p, time.Now())
			if err != nil {
				return err
			}

			if len(timeseries) == 0 {
				log.Println("no time series to forward to receive endpoint")
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

			s.forwardDuration.
				WithLabelValues(fmt.Sprintf("%d", resp.StatusCode)).
				Observe(time.Since(begin).Seconds())

			meanDrift := timeseriesMeanDrift(timeseries, time.Now().Unix())
			if math.Abs(meanDrift) > 10 {
				log.Printf("mean drift from now for clusters %s is: %.3fs",
					p.PartitionKey,
					meanDrift,
				)
			}

			if resp.StatusCode/100 != 2 {
				return fmt.Errorf("response status code is %s", resp.Status)
			}

			sc := 0
			for _, ts := range wreq.Timeseries {
				sc = sc + len(ts.Samples)
			}
			s.forwardSamples.Add(float64(sc))

			return nil
		}()
		if err != nil {
			s.forwardErrors.Inc()
			log.Printf("forwarding error: %v", err)
		}
	}()

	return s.next.WriteMetrics(ctx, p)
}

func convertToTimeseries(overwritten prometheus.Counter, p *store.PartitionedMetrics, now time.Time) ([]prompb.TimeSeries, error) {
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

			sample := prompb.Sample{
				Timestamp: *m.TimestampMs,
			}
			// If the sample is in the future, overrite it.
			if *m.TimestampMs > timestamp {
				sample.Timestamp = timestamp
				overwritten.Inc()
			}

			switch *f.Type {
			case clientmodel.MetricType_COUNTER:
				sample.Value = *m.Counter.Value
			case clientmodel.MetricType_GAUGE:
				sample.Value = *m.Gauge.Value
			case clientmodel.MetricType_UNTYPED:
				sample.Value = *m.Untyped.Value
			default:
				return nil, fmt.Errorf("metric type %s not supported", f.Type.String())
			}

			ts.Labels = append(ts.Labels, labelpairs...)
			ts.Samples = append(ts.Samples, sample)

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
