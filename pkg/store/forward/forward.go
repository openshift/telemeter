package forward

import (
	"bytes"
	"context"
	"fmt"
	"log"
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

const metricName = "__name__"

var (
	forwardSamples = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "telemeter_forward_samples_total",
		Help: "Total amount of samples successfully forwarded",
	})
	forwardErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "telemeter_forward_request_errors_total",
		Help: "Total amount of errors encountered while forwarding",
	})
	forwardDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "telemeter_forward_request_duration_seconds",
		Help:    "Tracks the duration of all forwarding requests",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}, // max = timeout
	}, []string{"status_code"})
)

func init() {
	prometheus.MustRegister(forwardErrors)
	prometheus.MustRegister(forwardDuration)
}

type Store struct {
	next   store.Store
	url    *url.URL
	client *http.Client
}

func New(url *url.URL, next store.Store) *Store {
	return &Store{
		next:   next,
		url:    url,
		client: &http.Client{},
	}
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
			timeseries, err := convertToTimeseries(p)
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

			if resp.StatusCode/100 != 2 {
				mean := timeseriesMeanDrift(timeseries, time.Now().Unix()*1000)
				return fmt.Errorf("response status code is %s - mean drift from now for clusters %S is: %.3fms",
					resp.Status,
					p.PartitionKey,
					float64(time.Now().Second())-mean,
				)
			}

			s := 0
			for _, ts := range wreq.Timeseries {
				s = s + len(ts.Samples)
			}
			forwardSamples.Add(float64(s))

			return nil
		}()
		if err != nil {
			forwardErrors.Inc()
			log.Printf("forwarding error: %v", err)
		}
	}()

	return s.next.WriteMetrics(ctx, p)
}

func convertToTimeseries(p *store.PartitionedMetrics) ([]prompb.TimeSeries, error) {
	var timeseries []prompb.TimeSeries

	for _, f := range p.Families {
		for _, m := range f.Metric {
			var ts prompb.TimeSeries

			labelpairs := []prompb.Label{{
				Name:  metricName,
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
