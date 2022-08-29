package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/go-chi/chi/middleware"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/openshift/telemeter/pkg/runutil"
	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/prometheus/prompb"

	"github.com/openshift/telemeter/pkg/metricfamily"
)

const (
	nameLabelName = "__name__"
)

var (
	forwardSamples = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "telemeter_v1_forward_samples_total",
		Help: "Total amount of successfully forwarded samples from v1 requests.",
	})
	// Uses labelvalue "telemeter_error" to denote errors when converting metric families
	// to a remote write request, and "error"/"success" to indicate upstream errors.
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

// ForwardHandler gets a request containing metric families and
// converts it to a remote write request forwarding it to the upstream at fowardURL.
func ForwardHandler(logger log.Logger, forwardURL *url.URL, tenantID string, client *http.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rlogger := log.With(logger, "request", middleware.GetReqID(r.Context()))

		clusterID, ok := ClusterIDFromContext(r.Context())
		if !ok {
			msg := "failed to retrieve clusterID"
			level.Warn(rlogger).Log("msg", msg)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}

		decoder := expfmt.NewDecoder(r.Body, expfmt.ResponseFormat(r.Header))
		families := make([]*clientmodel.MetricFamily, 0, 100)
		for {
			family := &clientmodel.MetricFamily{}
			if err := decoder.Decode(family); err != nil {
				if err == io.EOF {
					break
				}
				msg := err.Error()
				forwardRequests.WithLabelValues("telemeter_error").Inc()
				level.Warn(rlogger).Log("msg", msg, "err", err)
				http.Error(w, msg, http.StatusInternalServerError)
				return
			}

			families = append(families, family)
		}
		families = metricfamily.Pack(families)

		timeseries, err := convertToTimeseries(&PartitionedMetrics{ClusterID: clusterID, Families: families}, time.Now())
		if err != nil {
			msg := "failed to convert timeseries"
			forwardRequests.WithLabelValues("telemeter_error").Inc()
			level.Warn(rlogger).Log("msg", msg, "err", err)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}

		if len(timeseries) == 0 {
			level.Info(rlogger).Log("msg", "no time series to forward to receive endpoint")
			return
		}

		wreq := &prompb.WriteRequest{Timeseries: timeseries}

		data, err := proto.Marshal(wreq)
		if err != nil {
			msg := "failed to marshal proto"
			level.Warn(rlogger).Log("msg", msg, "err", err)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}

		compressed := snappy.Encode(nil, data)

		req, err := http.NewRequest(http.MethodPost, forwardURL.String(), bytes.NewBuffer(compressed))
		if err != nil {
			forwardRequests.WithLabelValues("telemeter_error").Inc()
			msg := "failed to create forwarding request"
			level.Warn(rlogger).Log("msg", msg, "err", err)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}
		// TODO: Parametrize that.
		req.Header.Add("THANOS-TENANT", tenantID)

		clientCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		begin := time.Now()
		resp, err := client.Do(req.WithContext(clientCtx))
		if err != nil {
			forwardRequests.WithLabelValues("error").Inc()
			msg := "failed to forward request"
			level.Warn(rlogger).Log("msg", msg, "err", err)
			http.Error(w, msg, http.StatusBadGateway)
			return
		}
		defer runutil.ExhaustCloseWithLogOnErr(rlogger, resp.Body, "close forward resp body")

		forwardDuration.WithLabelValues(fmt.Sprintf("%d", resp.StatusCode)).Observe(time.Since(begin).Seconds())

		meanDrift := timeseriesMeanDrift(timeseries, time.Now().Unix())
		if math.Abs(meanDrift) > 10 {
			level.Info(rlogger).Log("msg", "mean drift from now for clusters", "clusterID", clusterID, "drift", fmt.Sprintf("%.3fs", meanDrift))
		}

		if resp.StatusCode/100 != 2 {
			// surfacing upstreams error to our users too
			forwardRequests.WithLabelValues("error").Inc()
			msg := fmt.Sprintf("response status code is %s", resp.Status)
			level.Warn(rlogger).Log("msg", msg)
			http.Error(w, msg, resp.StatusCode)
			return
		}

		forwardRequests.WithLabelValues("success").Inc()

		s := 0
		for _, ts := range wreq.Timeseries {
			s = s + len(ts.Samples)
		}
		forwardSamples.Add(float64(s))
	}
}

func convertToTimeseries(p *PartitionedMetrics, now time.Time) ([]prompb.TimeSeries, error) {
	var timeseries []prompb.TimeSeries

	timestamp := now.UnixNano() / int64(time.Millisecond)
	for _, f := range p.Families {
		for _, m := range f.Metric {
			var ts prompb.TimeSeries

			labelpairs := []prompb.Label{{
				Name:  nameLabelName,
				Value: *f.Name,
			}}

			dedup := make(map[string]struct{})
			for _, l := range m.Label {
				// Skip empty labels.
				if *l.Name == "" || *l.Value == "" {
					continue
				}
				// Check for duplicates
				if _, ok := dedup[*l.Name]; ok {
					continue
				}
				labelpairs = append(labelpairs, prompb.Label{
					Name:  *l.Name,
					Value: *l.Value,
				})
				dedup[*l.Name] = struct{}{}
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
			sortLabels(ts.Labels)

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

func sortLabels(labels []prompb.Label) {
	lset := sortableLabels(labels)
	sort.Sort(&lset)
}

// Extension on top of prompb.Label to allow for easier sorting.
// Based on https://github.com/prometheus/prometheus/blob/main/model/labels/labels.go#L44
type sortableLabels []prompb.Label

func (sl *sortableLabels) Len() int           { return len(*sl) }
func (sl *sortableLabels) Swap(i, j int)      { (*sl)[i], (*sl)[j] = (*sl)[j], (*sl)[i] }
func (sl *sortableLabels) Less(i, j int) bool { return (*sl)[i].Name < (*sl)[j].Name }
