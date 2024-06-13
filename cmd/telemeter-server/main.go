package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
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

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/oklog/run"
	"github.com/openshift/telemeter/pkg/runutil"
	"github.com/openshift/telemeter/pkg/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/authorize/jwt"
	"github.com/openshift/telemeter/pkg/authorize/stub"
	"github.com/openshift/telemeter/pkg/authorize/tollbooth"
	"github.com/openshift/telemeter/pkg/cache"
	"github.com/openshift/telemeter/pkg/cache/memcached"
	telemeter_http "github.com/openshift/telemeter/pkg/http"
	"github.com/openshift/telemeter/pkg/logger"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/receive"
	"github.com/openshift/telemeter/pkg/server"
)

const desc = `
Receive federated metric push events

This server acts as an auth proxy for ingesting Prometheus metrics.
The original API implements its own protocol for receiving metrics.
The new API receives metrics via the Prometheus remote write API.
This server authenticates requests, performs local filtering and sanity
checking and then forwards the requests via remote write to another endpoint.

A client that connects to the server must perform an authorization check, providing
a token and a cluster identifier.
The original API expects a bearer token in the Authorization header and a cluster ID
as a query parameter when making a request against the /authorize endpoint.
The authorize endpoint will forward that request to an upstream server that
may approve or reject the request as well as add additional labels that will be added
to all future metrics from that client. The server will generate a JWT token with a
short lifetime and pass that back to the client, which is expected to use that token
when pushing metrics to /upload.

The new API expects a bearer token in the Authorization header when making requests
against the /metrics/v1/receive endpoints. This token should consist of a
base64-encoded JSON object containing "authorization_token" and "cluster_id" fields.

Clients are considered untrusted and so input data is validated, sorted, and
normalized before processing continues.
`

func defaultOpts() *Options {
	return &Options{
		LimitBytes:         500 * 1024,
		LimitReceiveBytes:  receive.DefaultRequestLimit,
		TokenExpireSeconds: 24 * 60 * 60,
		clusterIDKey:       "_id",
		Ratelimit:          4*time.Minute + 30*time.Second,
		MemcachedExpire:    24 * 60 * 60,
		MemcachedInterval:  10,
		TenantID:           "FB870BF3-9F3A-44FF-9BF7-D7A047A52F43",
	}
}

func main() {
	opt := defaultOpts()

	var listen, listenInternal string
	cmd := &cobra.Command{
		Short:         "Aggregate federated metrics pushes",
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

	cmd.Flags().StringVar(&opt.InternalTLSKeyPath, "internal-tls-key", opt.InternalTLSKeyPath, "Path to a private key to serve TLS for internal traffic.")
	cmd.Flags().StringVar(&opt.InternalTLSCertificatePath, "internal-tls-crt", opt.InternalTLSCertificatePath, "Path to a certificate to serve TLS for internal traffic.")

	cmd.Flags().StringSliceVar(&opt.LabelFlag, "label", opt.LabelFlag, "Labels to add to each outgoing metric, in key=value form.")
	cmd.Flags().StringVar(&opt.clusterIDKey, "partition-label", opt.clusterIDKey, "The label to separate incoming data on. This label will be required for callers to include.")

	cmd.Flags().StringVar(&opt.SharedKey, "shared-key", opt.SharedKey, "The path to a private key file that will be used to sign authentication requests.")
	cmd.Flags().Int64Var(&opt.TokenExpireSeconds, "token-expire-seconds", opt.TokenExpireSeconds, "The expiration of auth tokens in seconds.")

	cmd.Flags().StringVar(&opt.AuthorizeEndpoint, "authorize", opt.AuthorizeEndpoint, "A URL against which to authorize client requests.")

	cmd.Flags().StringVar(&opt.OIDCIssuer, "oidc-issuer", opt.OIDCIssuer, "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	cmd.Flags().StringVar(&opt.OIDCClientSecret, "client-secret", opt.OIDCClientSecret, "The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	cmd.Flags().StringVar(&opt.OIDCClientID, "client-id", opt.OIDCClientID, "The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	cmd.Flags().StringVar(&opt.OIDCAudienceEndpoint, "oidc-audience", opt.OIDCAudienceEndpoint, "The OIDC audience some providers like Auth0 need.")
	cmd.Flags().StringVar(&opt.TenantKey, "tenant-key", opt.TenantKey, "The JSON key in the bearer token whose value to use as the tenant ID.")
	cmd.Flags().StringSliceVar(&opt.Memcacheds, "memcached", opt.Memcacheds, "One or more Memcached server addresses.")
	cmd.Flags().Int32Var(&opt.MemcachedExpire, "memcached-expire", opt.MemcachedExpire, "Time after which keys stored in Memcached should expire, given in seconds.")
	cmd.Flags().Int32Var(&opt.MemcachedInterval, "memcached-interval", opt.MemcachedInterval, "The interval at which to update the Memcached DNS, given in seconds; use 0 to disable.")
	cmd.Flags().StringVar(&opt.TenantID, "tenant-id", opt.TenantID, "Tenant ID to use for the system forwarded to.")

	cmd.Flags().DurationVar(&opt.Ratelimit, "ratelimit", opt.Ratelimit, "The rate limit of metric uploads per cluster ID. Uploads happening more often than this limit will be rejected.")
	cmd.Flags().StringVar(&opt.ForwardURL, "forward-url", opt.ForwardURL, "All written metrics will be written to this URL additionally")

	cmd.Flags().BoolVarP(&opt.Verbose, "verbose", "v", opt.Verbose, "Show verbose output.")

	cmd.Flags().StringSliceVar(&opt.RequiredLabelFlag, "required-label", opt.RequiredLabelFlag, "Labels that must be present on each incoming metric, in key=value form.")
	cmd.Flags().StringArrayVar(&opt.Whitelist, "whitelist", opt.Whitelist, "Allowed rules for incoming metrics. If one of these rules is not matched, the metric is dropped.")
	cmd.Flags().StringVar(&opt.WhitelistFile, "whitelist-file", opt.WhitelistFile, "A file of allowed rules for incoming metrics. If one of these rules is not matched, the metric is dropped; one label key per line.")
	cmd.Flags().StringArrayVar(&opt.ElideLabels, "elide-label", opt.ElideLabels, "A list of labels to be elided from incoming metrics.")
	cmd.Flags().Int64Var(&opt.LimitBytes, "limit-bytes", opt.LimitBytes, "The maxiumum acceptable size of a request made to the upload endpoint.")
	cmd.Flags().Int64Var(&opt.LimitReceiveBytes, "limit-receive-bytes", opt.LimitReceiveBytes, "The maxiumum acceptable size of a request made to the receive endpoint.")

	cmd.Flags().StringVar(&opt.LogLevel, "log-level", opt.LogLevel, "Log filtering level. e.g info, debug, warn, error")

	cmd.Flags().StringVar(&opt.TracingServiceName, "internal.tracing.service-name", "telemeter-server",
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

	level.Info(l).Log("msg", "Telemeter server initialized.")
	if err := cmd.Execute(); err != nil {
		level.Error(l).Log("err", err)
		os.Exit(1)
	}
}

type Options struct {
	TLSKeyPath         string
	TLSCertificatePath string

	InternalTLSKeyPath         string
	InternalTLSCertificatePath string

	SharedKey          string
	TokenExpireSeconds int64

	AuthorizeEndpoint string

	OIDCIssuer           string
	OIDCClientID         string
	OIDCClientSecret     string
	OIDCAudienceEndpoint string

	TenantKey         string
	TenantID          string
	Memcacheds        []string
	MemcachedExpire   int32
	MemcachedInterval int32

	clusterIDKey      string
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

	// Set up the upstream authorization.
	var authorizeURL *url.URL
	var authorizeClient http.Client
	if len(o.AuthorizeEndpoint) > 0 {
		u, err := url.Parse(o.AuthorizeEndpoint)
		if err != nil {
			return fmt.Errorf("--authorize must be a valid URL: %v", err)
		}
		authorizeURL = u

		authorizeClient = http.Client{
			Timeout:   20 * time.Second,
			Transport: telemeter_http.NewInstrumentedRoundTripper("authorize", transport),
		}
	} else {
		level.Warn(o.Logger).Log("msg", "no AuthorizeEndpoint specified, server /authorize will be exposed without any auth")
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

		// Wrap authorise and forward clients in order to retrieve and inject OIDC Token to the request.
		authorizeClient.Transport = &oauth2.Transport{
			Base:   authorizeClient.Transport,
			Source: s,
		}
		forwardClient.Transport = &oauth2.Transport{
			Base:   forwardClient.Transport,
			Source: s,
		}
	}

	switch {
	case (len(o.TLSCertificatePath) == 0) != (len(o.TLSKeyPath) == 0):
		return fmt.Errorf("both --tls-key and --tls-crt must be provided")
	case (len(o.InternalTLSCertificatePath) == 0) != (len(o.InternalTLSKeyPath) == 0):
		return fmt.Errorf("both --internal-tls-key and --internal-tls-crt must be provided")
	}

	var g run.Group
	{
		internal := http.NewServeMux()

		// TODO: Refactor to not take *http.Mux
		telemeter_http.DebugRoutes(internal)
		telemeter_http.MetricRoutes(internal)
		telemeter_http.HealthRoutes(internal)

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

		// TODO: Refactor HealthRoutes to not take *http.Mux
		mux := http.NewServeMux()
		telemeter_http.HealthRoutes(mux)
		external.Mount("/", mux)

		// v1 routes
		{
			var (
				publicKey  crypto.PublicKey
				privateKey crypto.PrivateKey
			)

			if len(o.SharedKey) > 0 {
				data, err := ioutil.ReadFile(o.SharedKey)
				if err != nil {
					return fmt.Errorf("unable to read --shared-key: %v", err)
				}

				key, err := loadPrivateKey(data)
				if err != nil {
					return err
				}

				switch t := key.(type) {
				case *ecdsa.PrivateKey:
					privateKey, publicKey = t, t.Public()
				case *rsa.PrivateKey:
					privateKey, publicKey = t, t.Public()
				default:
					return fmt.Errorf("unknown key type in --shared-key")
				}
			} else {
				level.Warn(o.Logger).Log("msg", "Using a generated shared-key")

				key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					return fmt.Errorf("key generation failed: %v", err)
				}

				privateKey, publicKey = key, key.Public()
			}

			// Configure the whitelist.
			if len(o.WhitelistFile) > 0 {
				data, err := ioutil.ReadFile(o.WhitelistFile)
				if err != nil {
					return fmt.Errorf("unable to read --whitelist-file: %v", err)
				}
				o.Whitelist = append(o.Whitelist, strings.Split(string(data), "\n")...)
			}
			for i := 0; i < len(o.Whitelist); {
				s := strings.TrimSpace(o.Whitelist[i])
				if len(s) == 0 {
					o.Whitelist = append(o.Whitelist[:i], o.Whitelist[i+1:]...)
					continue
				}
				o.Whitelist[i] = s
				i++
			}
			whitelister, err := metricfamily.NewWhitelist(o.Whitelist)
			if err != nil {
				return err
			}

			const issuer = "telemeter.selfsigned"
			const audience = "telemeter-client"

			jwtAuthorizer := jwt.NewClientAuthorizer(
				issuer,
				[]crypto.PublicKey{publicKey},
				jwt.NewValidator(o.Logger, []string{audience}),
			)
			signer := jwt.NewSigner(issuer, privateKey)

			// configure the authenticator and incoming data validator
			var clusterAuth authorize.ClusterAuthorizer = authorize.ClusterAuthorizerFunc(stub.AuthorizeFn(o.Logger))
			if authorizeURL != nil {
				clusterAuth = tollbooth.NewAuthorizer(o.Logger, &authorizeClient, authorizeURL)
			} else {
				level.Warn(o.Logger).Log("msg", "no cluster authorizer specified. /authenticate endpoint is without any auth")
			}

			auth := jwt.NewAuthorizeClusterHandler(o.Logger, o.clusterIDKey, o.TokenExpireSeconds, signer, o.RequiredLabels, clusterAuth)

			forwardURL, err := url.Parse(o.ForwardURL)
			if err != nil {
				return fmt.Errorf("--forward-url must be a valid URL: %v", err)
			}

			transforms := metricfamily.MultiTransformer{}
			transforms.With(whitelister)
			if len(o.Labels) > 0 {
				transforms.With(metricfamily.NewLabel(o.Labels, nil))
			}
			transforms.With(metricfamily.NewElide(o.ElideLabels...))

			external.Post("/authorize",
				runutil.ExhaustCloseRequestBodyHandler(o.Logger,
					server.InstrumentedHandler("authorize",
						auth,
					),
				).ServeHTTP)

			external.Post("/upload",
				runutil.ExhaustCloseRequestBodyHandler(o.Logger,
					server.InstrumentedHandler("upload",
						authorize.NewAuthorizeClientHandler(jwtAuthorizer,
							server.ClusterID(o.Logger, o.clusterIDKey,
								server.Ratelimit(o.Logger, o.Ratelimit, time.Now,
									server.Snappy(
										server.Validate(o.Logger, transforms, 24*time.Hour, o.LimitBytes, time.Now,
											server.ForwardHandler(o.Logger, forwardURL, o.TenantID, forwardClient),
										),
									),
								),
							),
						),
					),
				).ServeHTTP,
			)
		}

		// v2 routes
		{
			v2AuthorizeClient := authorizeClient
			v2ForwardClient := forwardClient

			if len(o.Memcacheds) > 0 {
				mc := memcached.New(context.Background(), o.MemcachedInterval, o.MemcachedExpire, o.Memcacheds...)
				l := log.With(o.Logger, "component", "cache")
				v2AuthorizeClient.Transport = cache.NewRoundTripper(mc, tollbooth.ExtractToken, v2AuthorizeClient.Transport, l, prometheus.DefaultRegisterer)
			}

			receiver, err := receive.NewHandler(o.Logger, o.ForwardURL, v2ForwardClient, prometheus.DefaultRegisterer, o.TenantID, o.Whitelist, o.ElideLabels)
			if err != nil {
				level.Error(o.Logger).Log("msg", "could not initialize receive handler", "err", err)
			}

			external.Handle("/metrics/v1/receive",
				runutil.ExhaustCloseRequestBodyHandler(o.Logger,
					server.InstrumentedHandler("receive",
						authorize.NewHandler(o.Logger, &v2AuthorizeClient, authorizeURL, o.TenantKey,
							receiver.LimitBodySize(o.LimitReceiveBytes,
								receiver.TransformAndValidateWriteRequest(
									http.HandlerFunc(receiver.Receive),
									o.clusterIDKey,
								),
							),
						),
					),
				),
			)
		}

		externalPathJSON, _ := json.MarshalIndent(Paths{Paths: []string{"/", "/authorize", "/upload", "/healthz", "/healthz/ready", "/metrics/v1/receive"}}, "", "  ")

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
				if err := s.ServeTLS(externalListener, o.TLSCertificatePath, o.TLSKeyPath); err != nil && err != http.ErrServerClosed {
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
			authorizeClient.CloseIdleConnections()
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

	level.Info(o.Logger).Log("msg", "starting telemeter-server", "external", externalListener.Addr().String(), "internal", internalListener.Addr().String())

	return g.Run()
}

// loadPrivateKey loads a private key from PEM/DER-encoded data.
func loadPrivateKey(data []byte) (crypto.PrivateKey, error) {
	input := data

	block, _ := pem.Decode(data)
	if block != nil {
		input = block.Bytes
	}

	var priv interface{}
	priv, err0 := x509.ParsePKCS1PrivateKey(input)
	if err0 == nil {
		return priv, nil
	}

	priv, err1 := x509.ParsePKCS8PrivateKey(input)
	if err1 == nil {
		return priv, nil
	}

	priv, err2 := x509.ParseECPrivateKey(input)
	if err2 == nil {
		return priv, nil
	}

	return nil, fmt.Errorf("unable to parse private key data: '%s', '%s' and '%s'", err0, err1, err2)
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
