package main

import (
	"bytes"
	"context"
	"flag"
	stdlog "log"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/oklog/run"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/prompb"
)

var (
	remoteWriteRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "up_remote_writes_total",
		Help: "Total number of remote write requests.",
	},
		[]string{"result"},
	)
)

type labelArg []prompb.Label

func (la *labelArg) String() string {
	var ls []string
	for _, l := range *la {
		ls = append(ls, l.Name+"="+l.Value)
	}
	return strings.Join(ls, ", ")
}

func (la *labelArg) Set(v string) error {
	var lset []prompb.Label
	for _, l := range strings.Split(v, ",") {
		parts := strings.SplitN(l, "=", 2)
		if len(parts) != 2 {
			return errors.Errorf("unrecognized label %q", l)
		}
		if !model.LabelName.IsValid(model.LabelName(string(parts[0]))) {
			return errors.Errorf("unsupported format for label %s", l)
		}
		val, err := strconv.Unquote(parts[1])
		if err != nil {
			return errors.Wrap(err, "unquote label value")
		}
		lset = append(lset, prompb.Label{Name: parts[0], Value: val})
	}
	*la = labelArg(lset)
	return nil
}

func main() {
	opts := struct {
		Endpoint string
		Labels   labelArg
		Listen   string
		Name     string
		Period   string
		Token    string
	}{}

	flag.StringVar(&opts.Endpoint, "endpoint", "", "The endpoint to which to make remote-write requests.")
	flag.Var(&opts.Labels, "labels", "The labels that should be applied to remote-write requests.")
	flag.StringVar(&opts.Listen, "listen", ":8080", "The address on which internal server runs.")
	flag.StringVar(&opts.Name, "name", "up", "The name of the metric to send in remote-write requests.")
	flag.StringVar(&opts.Token, "token", "", "The bearer token to set in the authorization header on remote-write requests.")
	flag.StringVar(&opts.Period, "period", "1m", "The time to wait between remote-write requests.")
	flag.Parse()

	logger := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	logger = log.WithPrefix(logger, "ts", log.DefaultTimestampUTC)
	logger = log.WithPrefix(logger, "caller", log.DefaultCaller)

	endpoint, err := url.ParseRequestURI(opts.Endpoint)
	if err != nil {
		level.Error(logger).Log("msg", "--endpoint is invalid", "err", err)
		return
	}
	period, err := time.ParseDuration(opts.Period)
	if err != nil {
		level.Error(logger).Log("msg", "--period is invalid", "err", err)
		return
	}
	opts.Labels = append(opts.Labels, prompb.Label{
		Name:  "__name__",
		Value: opts.Name,
	})

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
		remoteWriteRequests,
	)

	var g run.Group
	{
		// Signal chans must be buffered.
		sig := make(chan os.Signal, 1)
		g.Add(func() error {
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			<-sig
			return nil
		}, func(_ error) {
			level.Info(logger).Log("msg", "caught interrrupt")
			close(sig)
		})
	}
	{
		router := http.NewServeMux()
		router.Handle("/metrics", promhttp.InstrumentMetricHandler(reg, promhttp.HandlerFor(reg, promhttp.HandlerOpts{})))
		router.HandleFunc("/debug/pprof/", pprof.Index)

		srv := &http.Server{Addr: opts.Listen, Handler: router}

		g.Add(func() error {
			level.Info(logger).Log("msg", "starting the HTTP server", "address", opts.Listen)
			return srv.ListenAndServe()
		}, func(err error) {
			if err == http.ErrServerClosed {
				level.Warn(logger).Log("msg", "internal server closed unexpectedly")
				return
			}
			level.Info(logger).Log("msg", "shutting down internal server")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := srv.Shutdown(ctx); err != nil {
				stdlog.Fatal(err)
			}
		})
	}
	{
		t := time.NewTicker(period)
		bg, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			level.Info(logger).Log("msg", "starting the remote-write client")
			for {
				select {
				case <-t.C:
					ctx, cancel := context.WithTimeout(bg, period)
					if err := post(ctx, endpoint, opts.Token, generate(opts.Labels)); err != nil {
						level.Error(logger).Log("msg", "failed to make request", "err", err)
					}
					cancel()
				case <-bg.Done():
					return nil
				}
			}
		}, func(_ error) {
			t.Stop()
			cancel()
		})
	}

	if err := g.Run(); err != nil {
		stdlog.Fatal(err)
	}
}

func generate(labels []prompb.Label) *prompb.WriteRequest {
	now := time.Now().UnixNano() / int64(time.Millisecond)
	w := prompb.WriteRequest{
		Timeseries: []prompb.TimeSeries{
			{
				Labels: labels,
				Samples: []prompb.Sample{
					{
						Value:     float64(now),
						Timestamp: now,
					},
				},
			},
		},
	}
	return &w
}

func post(ctx context.Context, endpoint *url.URL, token string, wreq *prompb.WriteRequest) error {
	var (
		buf []byte
		err error
		req *http.Request
		res *http.Response
	)
	defer func() {
		if err != nil {
			remoteWriteRequests.WithLabelValues("error").Inc()
			return
		}
		remoteWriteRequests.WithLabelValues("success").Inc()
	}()
	buf, err = proto.Marshal(wreq)
	if err != nil {
		return errors.Wrap(err, "marshalling proto")
	}
	req, err = http.NewRequest("POST", endpoint.String(), bytes.NewBuffer(snappy.Encode(nil, buf)))
	if err != nil {
		return errors.Wrap(err, "creating request")
	}
	req.Header.Add("Authorization", "Bearer "+token)
	res, err = http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return errors.Wrap(err, "making request")
	}
	if res.StatusCode != http.StatusOK {
		err = errors.New(res.Status)
		return errors.Wrap(err, "non-200 status")
	}
	return nil
}
