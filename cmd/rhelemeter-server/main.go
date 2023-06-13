package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	stdlog "log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/run"
	"github.com/openshift/telemeter/pkg/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	telemeter_http "github.com/openshift/telemeter/pkg/http"
	"github.com/openshift/telemeter/pkg/logger"
	"github.com/openshift/telemeter/pkg/receive"
	"github.com/openshift/telemeter/pkg/server"
)

const desc = `
Server for receiving Prometheus metrics through the remote_write API. Clients are authenticated 
with mTLS.
`

func defaultOpts() *Options {
	return &Options{
		LimitBytes:        500 * 1024,
		LimitReceiveBytes: receive.DefaultRequestLimit,
		Ratelimit:         4*time.Minute + 30*time.Second,
	}
}

func main() {
	opt := defaultOpts()

	var listen, listenInternal string
	cmd := &cobra.Command{
		Short:         "Proxy for Prometheus remote_write API with mTLS authentication.",
		Long:          desc,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			listener, err := net.Listen("tcp", listen)
			if err != nil {
				return err
			}
			internalListener, err := net.Listen("tcp", listenInternal)
			if err != nil {
				return err
			}

			return opt.Run(context.Background(), listener, internalListener)
		},
	}

	cmd.Flags().StringVar(&listen, "listen", "0.0.0.0:9003", "A host:port to listen on for upload traffic.")
	cmd.Flags().StringVar(&listenInternal, "listen-internal", "localhost:9004", "A host:port to listen on for health and metrics.")

	cmd.Flags().StringVar(&opt.TLSKeyPath, "tls-key", opt.TLSKeyPath, "Path to a private key to serve TLS for external traffic.")
	cmd.Flags().StringVar(&opt.TLSCertificatePath, "tls-crt", opt.TLSCertificatePath, "Path to a certificate to serve TLS for external traffic.")
	cmd.Flags().StringVar(&opt.TLSCACertificatePath, "tls-ca-crt", opt.TLSCACertificatePath, "Path to the trusted Certificate Authority of the rhelemeter client for mTLS.")

	cmd.Flags().StringVar(&opt.InternalTLSKeyPath, "internal-tls-key", opt.InternalTLSKeyPath, "Path to a private key to serve TLS for internal traffic.")
	cmd.Flags().StringVar(&opt.InternalTLSCertificatePath, "internal-tls-crt", opt.InternalTLSCertificatePath, "Path to a certificate to serve TLS for internal traffic.")

	cmd.Flags().StringSliceVar(&opt.LabelFlag, "label", opt.LabelFlag, "Labels to add to each outgoing metric, in key=value form.")

	cmd.Flags().StringVar(&opt.OIDCIssuer, "oidc-issuer", opt.OIDCIssuer, "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	cmd.Flags().StringVar(&opt.OIDCClientSecret, "client-secret", opt.OIDCClientSecret, "The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	cmd.Flags().StringVar(&opt.OIDCClientID, "client-id", opt.OIDCClientID, "The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	cmd.Flags().StringVar(&opt.OIDCAudienceEndpoint, "oidc-audience", opt.OIDCAudienceEndpoint, "The OIDC audience some providers like Auth0 need.")
	cmd.Flags().StringVar(&opt.TenantID, "tenant-id", opt.TenantID, "Tenant ID to use for the system forwarded to.")

	cmd.Flags().DurationVar(&opt.Ratelimit, "ratelimit", opt.Ratelimit, "The rate limit of metric uploads per client. Uploads happening more often than this limit will be rejected.")
	cmd.Flags().StringVar(&opt.ForwardURL, "forward-url", opt.ForwardURL, "All written metrics will be written to this URL additionally")

	cmd.Flags().BoolVarP(&opt.Verbose, "verbose", "v", opt.Verbose, "Show verbose output.")

	cmd.Flags().StringArrayVar(&opt.Whitelist, "whitelist", opt.Whitelist, "Allowed rules for incoming metrics. If one of these rules is not matched, the metric is dropped.")
	cmd.Flags().StringVar(&opt.WhitelistFile, "whitelist-file", opt.WhitelistFile, "A file of allowed rules for incoming metrics. If one of these rules is not matched, the metric is dropped; one label key per line.")
	cmd.Flags().StringArrayVar(&opt.ElideLabels, "elide-label", opt.ElideLabels, "A list of labels to be elided from incoming metrics.")
	cmd.Flags().Int64Var(&opt.LimitBytes, "limit-bytes", opt.LimitBytes, "The maxiumum acceptable size of a request made to the upload endpoint.")
	cmd.Flags().Int64Var(&opt.LimitReceiveBytes, "limit-receive-bytes", opt.LimitReceiveBytes, "The maxiumum acceptable size of a request made to the receive endpoint.")

	cmd.Flags().StringVar(&opt.LogLevel, "log-level", opt.LogLevel, "Log filtering level. e.g info, debug, warn, error")

	cmd.Flags().StringVar(&opt.TracingServiceName, "internal.tracing.service-name", "rhelemeter-server",
		"The service name to report to the tracing backend.")
	cmd.Flags().StringVar(&opt.TracingEndpoint, "internal.tracing.endpoint", "",
		"The full URL of the trace collector. If it's not set, tracing will be disabled.")
	cmd.Flags().Float64Var(&opt.TracingSamplingFraction, "internal.tracing.sampling-fraction", 0.1,
		"The fraction of traces to sample. Thus, if you set this to .5, half of traces will be sampled.")
	cmd.Flags().StringVar(&opt.TracingEndpointType, "internal.tracing.endpoint-type", string(tracing.EndpointTypeAgent),
		fmt.Sprintf("The tracing endpoint type. Options: '%s', '%s'.", tracing.EndpointTypeAgent, tracing.EndpointTypeCollector))

	l := log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	l = log.WithPrefix(l, "ts", log.DefaultTimestampUTC)
	l = log.WithPrefix(l, "caller", log.DefaultCaller)
	stdlog.SetOutput(log.NewStdlibAdapter(l))
	opt.Logger = l

	level.Info(l).Log("msg", "Rhelemeter server initialized.")
	if err := cmd.Execute(); err != nil {
		level.Error(l).Log("err", err)
		os.Exit(1)
	}
}

type Options struct {
	// External server mTLS configuration
	TLSKeyPath           string
	TLSCertificatePath   string
	TLSCACertificatePath string

	// Internal server TLS configuration
	InternalTLSKeyPath         string
	InternalTLSCertificatePath string

	OIDCIssuer           string
	OIDCClientID         string
	OIDCClientSecret     string
	OIDCAudienceEndpoint string

	TenantID string

	LabelFlag         []string
	Labels            map[string]string
	LimitBytes        int64
	LimitReceiveBytes int64
	RequiredLabelFlag []string
	RequiredLabels    map[string]string
	Whitelist         []string
	ElideLabels       []string
	WhitelistFile     string

	Ratelimit  time.Duration
	ForwardURL string

	LogLevel string
	Logger   log.Logger

	TracingServiceName      string
	TracingEndpoint         string
	TracingEndpointType     string
	TracingSamplingFraction float64

	Verbose bool
}

type Paths struct {
	Paths []string `json:"paths"`
}

func (o *Options) Run(ctx context.Context, externalListener, internalListener net.Listener) error {
	for _, flag := range o.LabelFlag {
		values := strings.SplitN(flag, "=", 2)
		if len(values) != 2 {
			return fmt.Errorf("--label must be of the form key=value: %s", flag)
		}
		if o.Labels == nil {
			o.Labels = make(map[string]string)
		}
		o.Labels[values[0]] = values[1]
	}

	for _, flag := range o.RequiredLabelFlag {
		values := strings.SplitN(flag, "=", 2)
		if len(values) != 2 {
			return fmt.Errorf("--required-label must be of the form key=value: %s", flag)
		}
		if o.RequiredLabels == nil {
			o.RequiredLabels = make(map[string]string)
		}
		o.RequiredLabels[values[0]] = values[1]
	}

	levelledOption := logger.LogLevelFromString(o.LogLevel)
	o.Logger = level.NewFilter(o.Logger, levelledOption)

	tp, err := tracing.InitTracer(
		ctx,
		o.TracingServiceName,
		o.TracingEndpoint,
		o.TracingEndpointType,
		o.TracingSamplingFraction,
	)
	if err != nil {
		return fmt.Errorf("cannot initialize tracer: %v", err)
	}

	otel.SetErrorHandler(tracing.OtelErrorHandler{Logger: o.Logger})

	var transport http.RoundTripper = otelhttp.NewTransport(&http.Transport{
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     30 * time.Second,
	})

	if o.Verbose {
		transport = telemeter_http.NewDebugRoundTripper(o.Logger, transport)
	}

	forwardClient := &http.Client{
		Transport: telemeter_http.NewInstrumentedRoundTripper("forward", transport),
	}

	if o.OIDCIssuer != "" {
		provider, err := oidc.NewProvider(ctx, o.OIDCIssuer)
		if err != nil {
			return fmt.Errorf("OIDC provider initialization failed: %v", err)
		}

		ctx = context.WithValue(ctx, oauth2.HTTPClient,
			&http.Client{
				// Note, that e.g forward timeouts after 5s.
				Timeout:   20 * time.Second,
				Transport: telemeter_http.NewInstrumentedRoundTripper("oauth", transport),
			},
		)

		cfg := clientcredentials.Config{
			ClientID:     o.OIDCClientID,
			ClientSecret: o.OIDCClientSecret,
			TokenURL:     provider.Endpoint().TokenURL,
		}

		if o.OIDCAudienceEndpoint != "" {
			cfg.EndpointParams = url.Values{"audience": []string{o.OIDCAudienceEndpoint}}
		}

		s := cfg.TokenSource(ctx)

		forwardClient.Transport = &oauth2.Transport{
			Base:   forwardClient.Transport,
			Source: s,
		}
	}

	var g run.Group
	{
		internal := http.NewServeMux()

		telemeter_http.DebugRoutes(internal)
		telemeter_http.MetricRoutes(internal)

		r := chi.NewRouter()
		r.Mount("/", internal)

		r.Get("/", func(w http.ResponseWriter, req *http.Request) {
			internalPathJSON, _ := json.MarshalIndent(Paths{Paths: []string{"/", "/metrics", "/debug/pprof", "/healthz", "/healthz/ready"}}, "", "  ")

			w.Header().Add("Content-Type", "application/json")
			if _, err := w.Write(internalPathJSON); err != nil {
				level.Error(o.Logger).Log("msg", "could not write internal paths", "err", err)
			}
		})

		s := &http.Server{
			Handler: otelhttp.NewHandler(r, "internal", otelhttp.WithTracerProvider(tp)),
		}

		// Run the internal server.
		g.Add(func() error {
			if len(o.InternalTLSCertificatePath) > 0 {
				if err := s.ServeTLS(internalListener, o.InternalTLSCertificatePath, o.InternalTLSKeyPath); err != nil && err != http.ErrServerClosed {
					level.Error(o.Logger).Log("msg", "internal HTTPS server exited", "err", err)
					return err
				}
			} else {
				if err := s.Serve(internalListener); err != nil && err != http.ErrServerClosed {
					level.Error(o.Logger).Log("msg", "internal HTTP server exited", "err", err)
					return err
				}
			}
			return nil
		}, func(error) {
			_ = s.Shutdown(context.TODO())
			internalListener.Close()
		})
	}
	{
		external := chi.NewRouter()
		external.Use(middleware.RequestID)

		mux := http.NewServeMux()
		telemeter_http.HealthRoutes(mux)
		external.Mount("/", mux)

		// rhelemeter routes
		{
			receiver, err := receive.NewHandler(o.Logger, o.ForwardURL, forwardClient, prometheus.DefaultRegisterer, o.TenantID, o.Whitelist, o.ElideLabels)
			if err != nil {
				level.Error(o.Logger).Log("msg", "could not initialize receive handler", "err", err)
			}

			external.Handle("/metrics/v1/receive", server.InstrumentedHandler("receive", http.HandlerFunc(receiver.Receive)))
		}

		externalPathJSON, _ := json.MarshalIndent(Paths{Paths: []string{"/", "/healthz", "/healthz/ready", "/metrics/v1/receive"}}, "", "  ")

		external.Get("/", func(w http.ResponseWriter, req *http.Request) {
			w.Header().Add("Content-Type", "application/json")
			if _, err := w.Write(externalPathJSON); err != nil {
				level.Error(o.Logger).Log("msg", "could not write external paths", "err", err)
			}
		})

		s := &http.Server{
			Handler: otelhttp.NewHandler(external, "external", otelhttp.WithTracerProvider(tp)),
			ErrorLog: stdlog.New(
				&filteredHTTP2ErrorWriter{
					out:               os.Stderr,
					toDebugLogFilters: logFilter,
					logger:            o.Logger,
				},
				"",
				0),
		}

		// Run the external server.
		g.Add(func() error {
			if len(o.TLSCertificatePath) > 0 {
				cert, err := tls.LoadX509KeyPair(o.TLSCertificatePath, o.TLSKeyPath)
				if err != nil {
					return err
				}

				caCert, err := ioutil.ReadFile(o.TLSCACertificatePath)
				if err != nil {
					return err
				}
				caCertPool := x509.NewCertPool()
				caCertPool.AppendCertsFromPEM(caCert)

				tlsConfig := &tls.Config{
					Certificates: []tls.Certificate{cert},
					ClientAuth:   tls.RequireAndVerifyClientCert,
					ClientCAs:    caCertPool,
				}

				externalTLSListener := tls.NewListener(externalListener, tlsConfig)
				if err := s.Serve(externalTLSListener); err != nil && err != http.ErrServerClosed {
					level.Error(o.Logger).Log("msg", "external HTTPS server exited", "err", err)
					return err
				}

			} else {
				if err := s.Serve(externalListener); err != nil && err != http.ErrServerClosed {
					level.Error(o.Logger).Log("msg", "external HTTP server exited", "err", err)
					return err
				}
			}
			return nil
		}, func(error) {
			_ = s.Shutdown(context.TODO())
			externalListener.Close()

			// Close clients in order to check for leaks properly.
			forwardClient.CloseIdleConnections()
			if c, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok {
				c.CloseIdleConnections()
			}
		})
	}

	// Kill all when caller requests to.
	gctx, gcancel := context.WithCancel(ctx)
	g.Add(func() error {
		<-gctx.Done()
		return gctx.Err()
	}, func(err error) {
		gcancel()
	})

	level.Info(o.Logger).Log("msg", "starting rhelemeter-server", "external", externalListener.Addr().String(), "internal", internalListener.Addr().String())

	return g.Run()
}

// logFilter is a list of filters
var logFilter = [][]string{
	// filter out TCP probes
	// see https://github.com/golang/go/issues/26918
	{
		"http2: server: error reading preface from client",
		"read: connection reset by peer",
	},
}

type filteredHTTP2ErrorWriter struct {
	out io.Writer
	// toDebugLogFilters is a list of filters.
	// All strings within a filter must match for the filter to match.
	// If any of the filters matches, the log is written to debug level.
	toDebugLogFilters [][]string
	logger            log.Logger
}

func (w *filteredHTTP2ErrorWriter) Write(p []byte) (int, error) {
	logContents := string(p)

	for _, filter := range w.toDebugLogFilters {
		shouldFilter := true
		for _, matches := range filter {
			if !strings.Contains(logContents, matches) {
				shouldFilter = false
				break
			}
		}
		if shouldFilter {
			level.Debug(w.logger).Log("msg", "http server error log has been filtered", "error", logContents)
			return len(p), nil
		}
	}
	return w.out.Write(p)
}
