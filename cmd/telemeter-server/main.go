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

	"github.com/cockroachdb/cmux"
	oidc "github.com/coreos/go-oidc"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/openshift/telemeter/pkg/authorize"
	"github.com/openshift/telemeter/pkg/authorize/jwt"
	"github.com/openshift/telemeter/pkg/authorize/stub"
	"github.com/openshift/telemeter/pkg/authorize/tollbooth"
	"github.com/openshift/telemeter/pkg/cluster"
	telemeter_http "github.com/openshift/telemeter/pkg/http"
	httpserver "github.com/openshift/telemeter/pkg/http/server"
	telemeter_oauth2 "github.com/openshift/telemeter/pkg/oauth2"
	"github.com/openshift/telemeter/pkg/store"
	"github.com/openshift/telemeter/pkg/store/instrumented"
	"github.com/openshift/telemeter/pkg/store/memstore"
	"github.com/openshift/telemeter/pkg/validator"
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

	cmd.Flags().StringArrayVar(&opt.LabelFlag, "label", opt.LabelFlag, "Labels to add to each outgoing metric, in key=value form.")
	cmd.Flags().StringVar(&opt.PartitionKey, "partition-label", opt.PartitionKey, "The label to separate incoming data on. This label will be required for callers to include.")

	cmd.Flags().StringArrayVar(&opt.Members, "join", opt.Members, "One or more host:ports to contact to find other peers.")
	cmd.Flags().StringVar(&opt.Name, "name", opt.Name, "The name to identify this node in the cluster. If not specified will be the hostname and a random suffix.")

	cmd.Flags().StringVar(&opt.SharedKey, "shared-key", opt.SharedKey, "The path to a private key file that will be used to sign authentication requests and secure the cluster protocol.")
	cmd.Flags().Int64Var(&opt.TokenExpireSeconds, "token-expire-seconds", opt.TokenExpireSeconds, "The expiration of auth tokens in seconds.")

	cmd.Flags().StringVar(&opt.AuthorizeEndpoint, "authorize", opt.AuthorizeEndpoint, "A endpoint URL to authorize against when a client requests a token.")

	cmd.Flags().StringVar(&opt.AuthorizeIssuerURL, "authorize-issuer-url", opt.AuthorizeIssuerURL, "The authorize OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	cmd.Flags().StringVar(&opt.AuthorizeUsername, "authorize-username", opt.AuthorizeUsername, "The authorize OIDC username, see rfc6749#section-4.3.")
	cmd.Flags().StringVar(&opt.AuthorizePassword, "authorize-password", opt.AuthorizePassword, "The authorize OIDC password, see rfc6749#section-4.3.")
	cmd.Flags().StringVar(&opt.AuthorizeClientID, "authorize-client-id", opt.AuthorizeClientID, "The authorize OIDC client ID, see rfc6749#section-4.3.")

	cmd.Flags().BoolVarP(&opt.Verbose, "verbose", "v", opt.Verbose, "Show verbose output.")

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

	Members []string

	Name               string
	SharedKey          string
	TokenExpireSeconds int64

	AuthorizeEndpoint string

	AuthorizeIssuerURL string
	AuthorizeClientID  string
	AuthorizeUsername  string
	AuthorizePassword  string

	PartitionKey string
	LabelFlag    []string
	Labels       map[string]string
	LimitBytes   int64

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
			Transport: transport,
		}

		if o.AuthorizeIssuerURL != "" {
			provider, err := oidc.NewProvider(ctx, o.AuthorizeIssuerURL)
			if err != nil {
				return fmt.Errorf("OIDC provider initialization failed: %v", err)
			}

			ctx = context.WithValue(ctx, oauth2.HTTPClient,
				&http.Client{
					Timeout:   20 * time.Second,
					Transport: transport,
				},
			)

			cfg := oauth2.Config{
				ClientID: o.AuthorizeClientID,
				Endpoint: provider.Endpoint(),
			}

			src := telemeter_oauth2.NewPasswordCredentialsTokenSource(
				ctx, &cfg,
				o.AuthorizeUsername, o.AuthorizePassword,
			)

			authorizeClient.Transport = &oauth2.Transport{
				Base:   authorizeClient.Transport,
				Source: src,
			}
		}
	}

	switch {
	case len(o.TLSCertificatePath) == 0 && len(o.TLSKeyPath) > 0,
		len(o.TLSCertificatePath) > 0 && len(o.TLSKeyPath) == 0:
		return fmt.Errorf("both --tls-key and --tls-crt must be provided")
	}
	useTLS := len(o.TLSCertificatePath) > 0
	useInternalTLS := false

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

	auth := jwt.NewAuthorizeClusterHandler(o.PartitionKey, o.TokenExpireSeconds, signer, o.Labels, clusterAuth)
	validator := validator.New(o.PartitionKey, o.Labels, o.LimitBytes, 24*time.Hour)

	ttl := 10 * time.Minute

	// register a store
	var store store.Store = instrumented.New(memstore.New(ttl), "memory")

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
		store = c
		internalPaths = append(internalPaths, "/debug/cluster")
		internalProtected.Handle("/debug/cluster", c)
	}

	server := httpserver.New(store, validator, ttl)

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

	internalMux := cmux.New(internalListener)
	if useInternalTLS {
		internalHTTPSListener := internalMux.Match(cmux.Any())
		go func() {
			s := &http.Server{
				Handler: internal,
			}
			if err := s.ServeTLS(internalHTTPSListener, o.TLSCertificatePath, o.TLSKeyPath); err != nil && err != http.ErrServerClosed {
				log.Printf("error: HTTPS server exited: %v", err)
				os.Exit(1)
			}
		}()
	} else {
		internalHTTPListener := internalMux.Match(cmux.HTTP1())
		go func() {
			if err := http.Serve(internalHTTPListener, internal); err != nil && err != http.ErrServerClosed {
				log.Printf("error: HTTP server exited: %v", err)
				os.Exit(1)
			}
		}()
	}
	go func() {
		if err := internalMux.Serve(); err != nil && err != http.ErrServerClosed {
			log.Printf("error: internal server exited: %v", err)
			os.Exit(1)
		}
	}()

	externalMux := cmux.New(externalListener)
	if useTLS {
		externalHTTPSListener := externalMux.Match(cmux.Any())
		go func() {
			s := &http.Server{
				Handler: external,
			}
			if err := s.ServeTLS(externalHTTPSListener, o.TLSCertificatePath, o.TLSKeyPath); err != nil && err != http.ErrServerClosed {
				log.Printf("error: HTTPS server exited: %v", err)
				os.Exit(1)
			}
		}()
	} else {
		externalHTTPListener := externalMux.Match(cmux.HTTP1())
		go func() {
			if err := http.Serve(externalHTTPListener, external); err != nil && err != http.ErrServerClosed {
				log.Printf("error: HTTP server exited: %v", err)
				os.Exit(1)
			}
		}()
	}
	go func() {
		if err := externalMux.Serve(); err != nil && err != http.ErrServerClosed {
			log.Printf("error: external server exited: %v", err)
			os.Exit(1)
		}
	}()

	select {}
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
