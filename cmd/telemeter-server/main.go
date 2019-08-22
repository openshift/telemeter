package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"log"
	mathrand "math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	oidc "github.com/coreos/go-oidc"
	"github.com/oklog/run"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/authorize/jwt"
	"github.com/openshift/telemeter/pkg/authorize/stub"
	"github.com/openshift/telemeter/pkg/authorize/tollbooth"
	"github.com/openshift/telemeter/pkg/cluster"
	telemeter_http "github.com/openshift/telemeter/pkg/http"
	httpserver "github.com/openshift/telemeter/pkg/http/server"
	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/store"
	"github.com/openshift/telemeter/pkg/store/forward"
	"github.com/openshift/telemeter/pkg/store/memstore"
	"github.com/openshift/telemeter/pkg/store/ratelimited"
	"github.com/openshift/telemeter/pkg/validate"
)

const desc = `
Receive federated metric push events

This server acts as a federation gateway by receiving Prometheus metrics data from
clients, performing local filtering and sanity checking, and then exposing it for 
scraping by a local Prometheus server. In order to satisfy the Prometheus federation
contract, the servers form a cluster with a consistent hash ring and internally
route requests to a consistent server so that Prometheus always sees the same metrics
for a given remote client for a given member.

A client that connects to the server must perform an authorization check, providing
a Bearer token and a cluster identifier (as ?cluster=<id>) against the /authorize 
endpoint. The authorize endpoint will forward that request to an upstream server that 
may approve or reject the request as well as add additional labels that will be added
to all future metrics from that client. The server will generate a JWT token with a
short lifetime and pass that back to the client, which is expected to use that token
when pushing metrics to /upload.

Clients are considered untrusted and so input data is validated, sorted, and 
normalized before processing continues.

To form a cluster, a --shared-key, a --listen-cluster address, and an optional existing 
cluster member to --join must be provided. The --name of this server is used to
identify the server within the cluster - if it changes client data may be sent to
another cluster member.
`

func main() {
	opt := &Options{
		Listen:         "0.0.0.0:9003",
		ListenInternal: "localhost:9004",

		LimitBytes:         500 * 1024,
		TokenExpireSeconds: 24 * 60 * 60,
		PartitionKey:       "_id",
		Ratelimit:          4*time.Minute + 30*time.Second,
		TTL:                10 * time.Minute,
	}
	cmd := &cobra.Command{
		Short:        "Aggregate federated metrics pushes",
		Long:         desc,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opt.Run()
		},
	}

	cmd.Flags().StringVar(&opt.Listen, "listen", opt.Listen, "A host:port to listen on for upload traffic.")
	cmd.Flags().StringVar(&opt.ListenInternal, "listen-internal", opt.ListenInternal, "A host:port to listen on for health and metrics.")
	cmd.Flags().StringVar(&opt.ListenCluster, "listen-cluster", opt.ListenCluster, "A host:port for cluster gossip.")

	cmd.Flags().StringVar(&opt.TLSKeyPath, "tls-key", opt.TLSKeyPath, "Path to a private key to serve TLS for external traffic.")
	cmd.Flags().StringVar(&opt.TLSCertificatePath, "tls-crt", opt.TLSCertificatePath, "Path to a certificate to serve TLS for external traffic.")

	cmd.Flags().StringVar(&opt.InternalTLSKeyPath, "internal-tls-key", opt.InternalTLSKeyPath, "Path to a private key to serve TLS for internal traffic.")
	cmd.Flags().StringVar(&opt.InternalTLSCertificatePath, "internal-tls-crt", opt.InternalTLSCertificatePath, "Path to a certificate to serve TLS for internal traffic.")

	cmd.Flags().StringSliceVar(&opt.LabelFlag, "label", opt.LabelFlag, "Labels to add to each outgoing metric, in key=value form.")
	cmd.Flags().StringVar(&opt.PartitionKey, "partition-label", opt.PartitionKey, "The label to separate incoming data on. This label will be required for callers to include.")

	cmd.Flags().StringSliceVar(&opt.Members, "join", opt.Members, "One or more host:ports to contact to find other peers.")
	cmd.Flags().StringVar(&opt.Name, "name", opt.Name, "The name to identify this node in the cluster. If not specified will be the hostname and a random suffix.")

	cmd.Flags().StringVar(&opt.SharedKey, "shared-key", opt.SharedKey, "The path to a private key file that will be used to sign authentication requests and secure the cluster protocol.")
	cmd.Flags().Int64Var(&opt.TokenExpireSeconds, "token-expire-seconds", opt.TokenExpireSeconds, "The expiration of auth tokens in seconds.")

	cmd.Flags().StringVar(&opt.AuthorizeEndpoint, "authorize", opt.AuthorizeEndpoint, "A URL against which to authorize client requests.")

	cmd.Flags().StringVar(&opt.OIDCIssuer, "oidc-issuer", opt.OIDCIssuer, "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	cmd.Flags().StringVar(&opt.ClientSecret, "client-secret", opt.ClientSecret, "The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	cmd.Flags().StringVar(&opt.ClientID, "client-id", opt.ClientID, "The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.")

	cmd.Flags().DurationVar(&opt.Ratelimit, "ratelimit", opt.Ratelimit, "The rate limit of metric uploads per cluster ID. Uploads happening more often than this limit will be rejected.")
	cmd.Flags().DurationVar(&opt.TTL, "ttl", opt.TTL, "The TTL for metrics to be held in memory.")
	cmd.Flags().StringVar(&opt.ForwardURL, "forward-url", opt.ForwardURL, "All written metrics will be written to this URL additionally")

	cmd.Flags().BoolVarP(&opt.Verbose, "verbose", "v", opt.Verbose, "Show verbose output.")

	cmd.Flags().StringSliceVar(&opt.RequiredLabelFlag, "required-label", opt.RequiredLabelFlag, "Labels that must be present on each incoming metric, in key=value form.")
	cmd.Flags().StringArrayVar(&opt.Whitelist, "whitelist", opt.Whitelist, "Allowed rules for incoming metrics. If one of these rules is not matched, the metric is dropped.")
	cmd.Flags().StringVar(&opt.WhitelistFile, "whitelist-file", opt.WhitelistFile, "A file of allowed rules for incoming metrics. If one of these rules is not matched, the metric is dropped; one label key per line.")
	cmd.Flags().StringArrayVar(&opt.ElideLabels, "elide-label", opt.ElideLabels, "A list of labels to be elided from incoming metrics.")

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

type Options struct {
	Listen         string
	ListenInternal string
	ListenCluster  string

	TLSKeyPath         string
	TLSCertificatePath string

	InternalTLSKeyPath         string
	InternalTLSCertificatePath string

	Members []string

	Name               string
	SharedKey          string
	TokenExpireSeconds int64

	AuthorizeEndpoint string

	OIDCIssuer   string
	ClientID     string
	ClientSecret string

	PartitionKey      string
	LabelFlag         []string
	Labels            map[string]string
	LimitBytes        int64
	RequiredLabelFlag []string
	RequiredLabels    map[string]string
	Whitelist         []string
	ElideLabels       []string
	WhitelistFile     string

	TTL        time.Duration
	Ratelimit  time.Duration
	ForwardURL string

	Verbose bool
}

type Paths struct {
	Paths []string `json:"paths"`
}

func (o *Options) Run() error {
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

	if len(o.Name) == 0 {
		hostname, err := os.Hostname()
		if err != nil {
			return err
		}
		o.Name = fmt.Sprintf("%s-%s", hostname, strconv.FormatUint(uint64(mathrand.Int63()), 32))
	}

	// set up the upstream authorization
	var authorizeURL *url.URL
	var authorizeClient *http.Client
	ctx := context.Background()
	if len(o.AuthorizeEndpoint) > 0 {
		u, err := url.Parse(o.AuthorizeEndpoint)
		if err != nil {
			return fmt.Errorf("--authorize must be a valid URL: %v", err)
		}
		authorizeURL = u

		var transport http.RoundTripper = &http.Transport{
			Dial:                (&net.Dialer{Timeout: 10 * time.Second}).Dial,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     30 * time.Second,
		}

		if o.Verbose {
			transport = telemeter_http.NewDebugRoundTripper(transport)
		}

		authorizeClient = &http.Client{
			Timeout:   20 * time.Second,
			Transport: telemeter_http.NewInstrumentedRoundTripper("authorize", transport),
		}

		if o.OIDCIssuer != "" {
			provider, err := oidc.NewProvider(ctx, o.OIDCIssuer)
			if err != nil {
				return fmt.Errorf("OIDC provider initialization failed: %v", err)
			}

			ctx = context.WithValue(ctx, oauth2.HTTPClient,
				&http.Client{
					Timeout:   20 * time.Second,
					Transport: telemeter_http.NewInstrumentedRoundTripper("oauth", transport),
				},
			)

			cfg := clientcredentials.Config{
				ClientID:     o.ClientID,
				ClientSecret: o.ClientSecret,
				TokenURL:     provider.Endpoint().TokenURL,
			}

			authorizeClient.Transport = &oauth2.Transport{
				Base:   authorizeClient.Transport,
				Source: cfg.TokenSource(ctx),
			}
		}
	}

	switch {
	case (len(o.TLSCertificatePath) == 0) != (len(o.TLSKeyPath) == 0):
		return fmt.Errorf("both --tls-key and --tls-crt must be provided")
	case (len(o.InternalTLSCertificatePath) == 0) != (len(o.InternalTLSKeyPath) == 0):
		return fmt.Errorf("both --internal-tls-key and --internal-tls-crt must be provided")
	}
	useTLS := len(o.TLSCertificatePath) > 0
	useInternalTLS := len(o.InternalTLSCertificatePath) > 0

	var (
		publicKey  crypto.PublicKey
		privateKey crypto.PrivateKey
		keyBytes   []byte
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
			keyBytes, _ = x509.MarshalECPrivateKey(t)
			privateKey, publicKey = t, t.Public()
		case *rsa.PrivateKey:
			keyBytes = x509.MarshalPKCS1PrivateKey(t)
			privateKey, publicKey = t, t.Public()
		default:
			return fmt.Errorf("unknown key type in --shared-key")
		}
	} else {
		if len(o.Members) > 0 {
			return fmt.Errorf("--shared-key must be specified when specifying a cluster to join")
		}

		log.Printf("warning: Using a generated shared-key")

		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return fmt.Errorf("key generation failed: %v", err)
		}

		keyBytes, err = x509.MarshalECPrivateKey(key)
		if err != nil {
			return fmt.Errorf("unable to marshal private key")
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

	issuer := "telemeter.selfsigned"
	audience := "federate"

	jwtAuthorizer := jwt.NewClientAuthorizer(
		issuer,
		[]crypto.PublicKey{publicKey},
		jwt.NewValidator([]string{audience}),
	)
	signer := jwt.NewSigner(issuer, privateKey)

	// create a secret for the JWT key
	h := sha256.New()
	if _, err := h.Write(keyBytes); err != nil {
		return fmt.Errorf("JWT secret generation failed: %v", err)
	}
	secret := h.Sum(nil)[:32]

	external := http.NewServeMux()
	externalProtected := http.NewServeMux()
	internal := http.NewServeMux()
	internalProtected := http.NewServeMux()

	internalPaths := []string{"/", "/federate", "/metrics", "/debug/pprof", "/healthz", "/healthz/ready"}

	// configure the authenticator and incoming data validator
	var clusterAuth authorize.ClusterAuthorizer = authorize.ClusterAuthorizerFunc(stub.Authorize)
	if authorizeURL != nil {
		clusterAuth = tollbooth.NewAuthorizer(authorizeClient, authorizeURL)
	}

	auth := jwt.NewAuthorizeClusterHandler(o.PartitionKey, o.TokenExpireSeconds, signer, o.RequiredLabels, clusterAuth)
	validator := validate.New(o.PartitionKey, o.LimitBytes, 24*time.Hour)

	var store store.Store

	ms := memstore.New(o.TTL)
	ms.StartCleaner(ctx, time.Minute)
	store = ms

	// If specified all written metrics will be written to the remote forward URL
	if o.ForwardURL != "" {
		u, err := url.Parse(o.ForwardURL)
		if err != nil {
			return fmt.Errorf("--forward-url must be a valid URL: %v", err)
		}
		store = forward.New(u, store)
	}

	// Create a rate-limited store with a memory-store as its backend.
	store = ratelimited.New(o.Ratelimit, store)

	if len(o.ListenCluster) > 0 {
		c := cluster.NewDynamic(o.Name, store)
		ml, err := cluster.NewMemberlist(o.Name, o.ListenCluster, secret, o.Verbose, c)
		if err != nil {
			return fmt.Errorf("unable to configure cluster: %v", err)
		}

		c.Start(ml, context.Background())

		if len(o.Members) > 0 {
			go func() {
				for {
					if err := c.Join(o.Members); err != nil {
						log.Printf("error: Could not join any of %v: %v", o.Members, err)
						time.Sleep(5 * time.Second)
						continue
					}
					return
				}
			}()
		}
		internalPaths = append(internalPaths, "/debug/cluster")
		internalProtected.Handle("/debug/cluster", c)
		store = c
		// Wrap the cluster store within a rate-limited store.
		// This guarantees an upper-bound on the total inter-node requests that
		// hit the target node of `l*n`, where l is the rate limit and n is
		// the cluster size. Without this, if a DOS attack with IDs that hash
		// to node A's bucket enter the cluster on different node, node B,
		// then node B will dutifully pass along the requests to the node A
		// and can DOS the target and congest the internal network.
		if o.Ratelimit != 0 {
			store = ratelimited.New(o.Ratelimit, store)
		}
	}

	transforms := metricfamily.MultiTransformer{}
	transforms.With(whitelister)
	if len(o.Labels) > 0 {
		transforms.With(metricfamily.NewLabel(o.Labels, nil))
	}
	transforms.With(metricfamily.NewElide(o.ElideLabels...))

	server := httpserver.New(store, validator, transforms, o.TTL)

	internalPathJSON, _ := json.MarshalIndent(Paths{Paths: internalPaths}, "", "  ")
	externalPathJSON, _ := json.MarshalIndent(Paths{Paths: []string{"/", "/authorize", "/upload", "/healthz", "/healthz/ready"}}, "", "  ")

	// TODO: add internal authorization
	telemeter_http.DebugRoutes(internalProtected)
	internalProtected.Handle("/federate", http.HandlerFunc(server.Get))

	internal.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/" && req.Method == "GET" {
			w.Header().Add("Content-Type", "application/json")
			if _, err := w.Write(internalPathJSON); err != nil {
				log.Printf("error writing internal paths: %v", err)
			}
			return
		}
		internalProtected.ServeHTTP(w, req)
	}))
	telemeter_http.MetricRoutes(internal)
	telemeter_http.HealthRoutes(internal)

	externalProtected.Handle("/upload", telemeter_http.NewInstrumentedHandler("upload", http.HandlerFunc(server.Post)))
	externalProtectedHandler := authorize.NewAuthorizeClientHandler(jwtAuthorizer, externalProtected)

	external.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/" && req.Method == "GET" {
			w.Header().Add("Content-Type", "application/json")
			if _, err := w.Write(externalPathJSON); err != nil {
				log.Printf("error writing external paths: %v", err)
			}
			return
		}
		externalProtectedHandler.ServeHTTP(w, req)
	}))
	telemeter_http.HealthRoutes(external)
	external.Handle("/authorize", telemeter_http.NewInstrumentedHandler("authorize", auth))

	log.Printf("Starting telemeter-server %s on %s (internal=%s, cluster=%s)", o.Name, o.Listen, o.ListenInternal, o.ListenCluster)

	internalListener, err := net.Listen("tcp", o.ListenInternal)
	if err != nil {
		return err
	}
	externalListener, err := net.Listen("tcp", o.Listen)
	if err != nil {
		return err
	}

	var g run.Group
	{
		// Run the internal server.
		g.Add(func() error {
			s := &http.Server{
				Handler: internal,
			}
			if useInternalTLS {
				if err := s.ServeTLS(internalListener, o.InternalTLSCertificatePath, o.InternalTLSKeyPath); err != nil && err != http.ErrServerClosed {
					log.Printf("error: internal HTTPS server exited: %v", err)
					return err
				}
			} else {
				if err := s.Serve(internalListener); err != nil && err != http.ErrServerClosed {
					log.Printf("error: internal HTTP server exited: %v", err)
					return err
				}
			}
			return nil
		}, func(error) {
			internalListener.Close()
		})
	}

	{
		// Run the external server.
		g.Add(func() error {
			s := &http.Server{
				Handler: external,
			}
			if useTLS {
				if err := s.ServeTLS(externalListener, o.TLSCertificatePath, o.TLSKeyPath); err != nil && err != http.ErrServerClosed {
					log.Printf("error: external HTTPS server exited: %v", err)
					return err
				}
			} else {
				if err := s.Serve(externalListener); err != nil && err != http.ErrServerClosed {
					log.Printf("error: external HTTP server exited: %v", err)
					return err
				}
			}
			return nil
		}, func(error) {
			externalListener.Close()
		})
	}

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
