package main

import (
	"fmt"
	stdlog "log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/run"
	"github.com/spf13/cobra"

	"github.com/openshift/telemeter/pkg/benchmark"
	telemeterhttp "github.com/openshift/telemeter/pkg/http"
	"github.com/openshift/telemeter/pkg/logger"
)

type options struct {
	Listen string

	To          string
	ToAuthorize string
	ToUpload    string

	ToCAFile    string
	ToToken     string
	ToTokenFile string

	Interval    time.Duration
	MetricsFile string
	Workers     int

	LogLevel string
	Logger   log.Logger
}

var opt options = options{
	Interval: benchmark.DefaultSyncPeriod,
	Listen:   "localhost:8080",
	Workers:  1000,
}

func main() {
	cmd := &cobra.Command{
		Short: "Benchmark Telemeter",

		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCmd()
		},
	}

	cmd.Flags().StringVar(&opt.To, "to", opt.To, "A telemeter server to send metrics to.")
	cmd.Flags().StringVar(&opt.ToUpload, "to-upload", opt.ToUpload, "A telemeter server endpoint to push metrics to. Will be defaulted for standard servers.")
	cmd.Flags().StringVar(&opt.ToAuthorize, "to-auth", opt.ToAuthorize, "A telemeter server endpoint to exchange the bearer token for an access token. Will be defaulted for standard servers.")
	cmd.Flags().StringVar(&opt.ToCAFile, "to-ca-file", opt.ToCAFile, "A file containing the CA certificate to use to verify the --to URL in addition to the system roots certificates.")
	cmd.Flags().StringVar(&opt.ToToken, "to-token", opt.ToToken, "A bearer token to use when authenticating to the destination telemeter server.")
	cmd.Flags().StringVar(&opt.ToTokenFile, "to-token-file", opt.ToTokenFile, "A file containing a bearer token to use when authenticating to the destination telemeter server.")
	cmd.Flags().StringVar(&opt.MetricsFile, "metrics-file", opt.MetricsFile, "A file containing Prometheus metrics to send to the destination telemeter server.")
	cmd.Flags().DurationVar(&opt.Interval, "interval", opt.Interval, "The interval between scrapes. Prometheus returns the last 5 minutes of metrics when invoking the federation endpoint.")
	cmd.Flags().StringVar(&opt.Listen, "listen", opt.Listen, "A host:port to listen on for health and metrics.")
	cmd.Flags().IntVar(&opt.Workers, "workers", opt.Workers, "The number of workers to run in parallel.")

	cmd.Flags().StringVar(&opt.LogLevel, "log-level", opt.LogLevel, "Log filtering level. e.g info, debug, warn, error")

	l := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	lvl, err := cmd.Flags().GetString("log-level")
	if err != nil {
		level.Error(l).Log("msg", "could not parse log-level.")
	}
	l = level.NewFilter(l, logger.LogLevelFromString(lvl))
	l = log.WithPrefix(l, "ts", log.DefaultTimestampUTC)
	l = log.WithPrefix(l, "caller", log.DefaultCaller)
	stdlog.SetOutput(log.NewStdlibAdapter(l))
	opt.Logger = l

	if err := cmd.Execute(); err != nil {
		level.Error(l).Log("err", err)
		os.Exit(1)
	}
}

func runCmd() error {
	var to, toUpload, toAuthorize *url.URL
	var err error
	if len(opt.MetricsFile) == 0 {
		return fmt.Errorf("--metrics-file must be specified")
	}
	to, err = url.Parse(opt.ToUpload)
	if err != nil {
		return fmt.Errorf("--to-upload is not a valid URL: %v", err)
	}
	if len(opt.ToUpload) > 0 {
		to, err = url.Parse(opt.ToUpload)
		if err != nil {
			return fmt.Errorf("--to-upload is not a valid URL: %v", err)
		}
	}
	if len(opt.ToAuthorize) > 0 {
		toAuthorize, err = url.Parse(opt.ToAuthorize)
		if err != nil {
			return fmt.Errorf("--to-auth is not a valid URL: %v", err)
		}
	}
	if len(opt.To) > 0 {
		to, err = url.Parse(opt.To)
		if err != nil {
			return fmt.Errorf("--to is not a valid URL: %v", err)
		}
		if len(to.Path) == 0 {
			to.Path = "/"
		}
		if toAuthorize == nil {
			u := *to
			u.Path = path.Join(to.Path, "authorize")
			toAuthorize = &u
		}
		u := *to
		u.Path = path.Join(to.Path, "upload")
		toUpload = &u
	}

	if toUpload == nil || toAuthorize == nil {
		return fmt.Errorf("either --to or --to-auth and --to-upload must be specified")
	}

	cfg := &benchmark.Config{
		ToAuthorize: toAuthorize,
		ToUpload:    toUpload,
		ToCAFile:    opt.ToCAFile,
		ToToken:     opt.ToToken,
		ToTokenFile: opt.ToTokenFile,
		Interval:    opt.Interval,
		MetricsFile: opt.MetricsFile,
		Workers:     opt.Workers,
		Logger:      opt.Logger,
	}

	b, err := benchmark.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to configure the Telemeter benchmarking tool: %v", err)
	}

	level.Info(opt.Logger).Log("msg", "starting telemeter-benchmark", "to", opt.To, "addr", opt.Listen)

	var g run.Group
	{
		// Execute the worker's `Run` func.
		g.Add(func() error {
			b.Run()
			return nil
		}, func(error) {
			b.Stop()
		})
	}

	{
		// Notify and reload on SIGHUP.
		hup := make(chan os.Signal, 1)
		signal.Notify(hup, syscall.SIGHUP)
		// Cleanup on SIGINT.
		in := make(chan os.Signal, 1)
		signal.Notify(in, syscall.SIGINT)
		cancel := make(chan struct{})
		g.Add(func() error {
			for {
				select {
				case <-hup:
					if err := b.Reconfigure(cfg); err != nil {
						level.Error(opt.Logger).Log("msg", "failed to reload config", "err", err)
						return err
					}
				case <-in:
					level.Warn(opt.Logger).Log("msg", "caught interrupt; exiting gracefully...")
					b.Stop()
					return nil
				case <-cancel:
					return nil
				}
			}
		}, func(error) {
			close(cancel)
		})
	}

	if len(opt.Listen) > 0 {
		handlers := http.NewServeMux()
		telemeterhttp.DebugRoutes(handlers)
		telemeterhttp.HealthRoutes(handlers)
		telemeterhttp.MetricRoutes(handlers)
		telemeterhttp.ReloadRoutes(handlers, func() error {
			return b.Reconfigure(cfg)
		})
		l, err := net.Listen("tcp", opt.Listen)
		if err != nil {
			return fmt.Errorf("failed to listen: %v", err)
		}

		// Run the HTTP server.
		g.Add(func() error {
			if err := http.Serve(l, handlers); err != nil && err != http.ErrServerClosed {
				level.Error(opt.Logger).Log("msg", "server exited unexpectedly", "err", err)
				return err
			}
			return nil
		}, func(error) {
			l.Close()
		})
	}

	return g.Run()
}
