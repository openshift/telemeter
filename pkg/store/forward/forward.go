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
	forwardErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "telemeter_forward_request_errors_total",
		Help: "Total amount of errors encountered while forwarding",
	})
	forwardDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "telemeter_forward_request_duration_seconds",
		Help:    "Tracks the current amount of families for a given partition.",
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

	// Run in a func to catch all transient errors
	err := func() error {
		timeseries := convertToTimeseries(p)

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

		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
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
			return fmt.Errorf("response was not 200 OK, but %s", resp.Status)
		}

		return nil
	}()
	if err != nil {
		forwardErrors.Inc()
		log.Printf("forwarding error: %v", err)
	}

	return nil
}

func convertToTimeseries(p *store.PartitionedMetrics) []prompb.TimeSeries {
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
				panic(fmt.Sprintf("metric type %s not supported", f.Type.String()))
			}

			ts.Labels = append(ts.Labels, labelpairs...)
			ts.Samples = append(ts.Samples, s)

			timeseries = append(timeseries, ts)
		}
	}

	return timeseries
}