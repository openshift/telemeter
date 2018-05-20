package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	mathrand "math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/cmux"
	"github.com/spf13/cobra"

	"github.com/smarterclayton/telemeter/pkg/authorizer/jwt"
	"github.com/smarterclayton/telemeter/pkg/authorizer/remoteauthserver"
	"github.com/smarterclayton/telemeter/pkg/cluster"
	telemeterhttp "github.com/smarterclayton/telemeter/pkg/http"
	httpauthorizer "github.com/smarterclayton/telemeter/pkg/http/authorizer"
	"github.com/smarterclayton/telemeter/pkg/http/server"
	"github.com/smarterclayton/telemeter/pkg/untrusted"
)

func main() {
	opt := &Options{
		Listen:             "0.0.0.0:9003",
		ListenInternal:     "localhost:9004",
		LimitBytes:         500 * 1024,
		TokenExpireSeconds: 24 * 60 * 60,
		PartitionKey:       "cluster",
	}
	cmd := &cobra.Command{
		Short: "Aggregate federated metrics pushes",

		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opt.Run()
		},
	}

	cmd.Flags().Int64Var(&opt.TokenExpireSeconds, "token-expire-seconds", opt.TokenExpireSeconds, "The expiration of auth tokens in seconds.")
	cmd.Flags().StringVar(&opt.Listen, "listen", opt.Listen, "A host:port to listen on for upload traffic.")
	cmd.Flags().StringVar(&opt.ListenInternal, "listen-internal", opt.ListenInternal, "A host:port to listen on for health and metrics.")
	cmd.Flags().StringVar(&opt.ListenCluster, "listen-cluster", opt.ListenCluster, "A host:port for cluster gossip.")
	cmd.Flags().StringArrayVar(&opt.LabelFlag, "label", opt.LabelFlag, "Labels to add to each outgoing metric, in key=value form.")
	cmd.Flags().StringVar(&opt.PartitionKey, "partition-label", opt.PartitionKey, "The label to separate incoming data on. This label will be required for callers to include.")
	cmd.Flags().StringVar(&opt.StorageDir, "storage-dir", opt.StorageDir, "The directory to persist incoming metrics. If not specified metrics will only live in memory.")
	cmd.Flags().StringArrayVar(&opt.Members, "join", opt.Members, "One or more host:ports to contact to find other peers.")

	cmd.Flags().StringVar(&opt.Name, "name", opt.Name, "The name to identify this node in the cluster. If not specified will be the hostname and a random suffix.")
	cmd.Flags().StringVar(&opt.SharedKey, "shared-key", opt.SharedKey, "The path to a private key file that will be used to sign authentication requests and secure the cluster protocol.")

	cmd.Flags().BoolVarP(&opt.Verbose, "verbose", "v", opt.Verbose, "Show verbose output.")

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

type Options struct {
	Listen         string
	ListenInternal string
	ListenCluster  string
	LimitBytes     int64

	Members []string

	Name      string
	SharedKey string

	PartitionKey string
	LabelFlag    []string
	Labels       map[string]string

	StorageDir string

	TokenExpireSeconds int64

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

	var (
		signer     *jwt.Signer
		authorizer *jwt.Authorizer
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
			privateKey = t
			keyBytes, _ = x509.MarshalECPrivateKey(t)
			publicKey = t.Public()
		case *rsa.PrivateKey:
			privateKey = t
			keyBytes = x509.MarshalPKCS1PrivateKey(t)
			publicKey = t.Public()
		default:
			return fmt.Errorf("unknown key type in --shared-key")
		}

		signer, authorizer, err = jwt.NewForKey("federate", privateKey, publicKey)
		if err != nil {
			return fmt.Errorf("unable to create signer: %v", err)
		}

	} else {
		var err error
		var key *ecdsa.PrivateKey
		signer, authorizer, publicKey, key, err = jwt.New("federate")
		if err != nil {
			return fmt.Errorf("unable to create signer: %v", err)
		}

		keyBytes, err = x509.MarshalECPrivateKey(key)
		if err != nil {
			return fmt.Errorf("unable to marshal private key")
		}

		privateKey = key
	}

	h := sha256.New()
	h.Write(keyBytes)
	secret := h.Sum(nil)[:32]

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		log.Fatalf("failed to generate serial number: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Issuer:       pkix.Name{CommonName: "telemeter-server-self-signed"},
		Subject: pkix.Name{
			Organization: []string{"telemeter-server"},
		},
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA: true,
	}
	serverData, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		return fmt.Errorf("unable to create server certificate for private key: %v", err)
	}
	serverCert, err := x509.ParseCertificate(serverData)
	if err != nil {
		return fmt.Errorf("unable to parse server certificate for private key: %v", err)
	}
	externalTLS := &tls.Config{}
	externalTLS.Certificates = append(externalTLS.Certificates, tls.Certificate{
		Certificate: [][]byte{serverData},
		PrivateKey:  privateKey,
		Leaf:        serverCert,
	})

	var internalTLS *tls.Config

	external := http.NewServeMux()
	externalProtected := http.NewServeMux()
	internal := http.NewServeMux()
	internalProtected := http.NewServeMux()

	internalPaths := []string{"/", "/federate", "/metrics", "/debug/pprof", "/healthz", "/healthz/ready"}

	auth := remoteauthserver.New(o.PartitionKey, nil, nil, o.TokenExpireSeconds, signer, o.Labels)
	validator := untrusted.NewValidator(o.PartitionKey, o.Labels, o.LimitBytes, 24*time.Hour)
	var store server.Store
	if len(o.StorageDir) > 0 {
		log.Printf("Storing metrics on disk at %s", o.StorageDir)
		store = server.NewDiskStore(o.StorageDir)
	} else {
		store = server.NewMemoryStore()
	}
	if len(o.ListenCluster) > 0 {
		cluster, err := cluster.NewDynamic(o.Name, o.ListenCluster, secret, store, o.Verbose)
		if err != nil {
			return fmt.Errorf("unable to configure cluster: %v", err)
		}
		if len(o.Members) > 0 {
			go func() {
				for {
					if err := cluster.Join(o.Members); err != nil {
						log.Printf("error: Could not join any of %v: %v", o.Members, err)
						time.Sleep(5 * time.Second)
					}
					return
				}
			}()
		}
		store = cluster
		internalPaths = append(internalPaths, "/debug/cluster")
		internalProtected.Handle("/debug/cluster", cluster)
	}
	server := server.New(store, validator)

	internalPathJSON, _ := json.MarshalIndent(Paths{Paths: internalPaths}, "", "  ")
	externalPathJSON, _ := json.MarshalIndent(Paths{Paths: []string{"/", "/authorize", "/upload", "/healthz", "/healthz/ready"}}, "", "  ")

	telemeterhttp.AddDebug(internalProtected)
	internalProtected.Handle("/federate", http.HandlerFunc(server.Get))

	// TODO: add internal authorization
	internal.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/" && req.Method == "GET" {
			w.Header().Add("Content-Type", "application/json")
			w.Write(internalPathJSON)
			return
		}
		internalProtected.ServeHTTP(w, req)
	}))
	telemeterhttp.AddMetrics(internal)
	telemeterhttp.AddHealth(internal)

	externalProtected.Handle("/upload", http.HandlerFunc(server.Post))
	externalProtectedHandler := httpauthorizer.New(externalProtected, authorizer)

	external.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/" && req.Method == "GET" {
			w.Header().Add("Content-Type", "application/json")
			w.Write(externalPathJSON)
			return
		}
		externalProtectedHandler.ServeHTTP(w, req)
	}))
	telemeterhttp.AddHealth(external)
	external.Handle("/authorize", http.HandlerFunc(auth.AuthorizeHTTP))

	internalListener, err := net.Listen("tcp", o.ListenInternal)
	if err != nil {
		return err
	}
	externalListener, err := net.Listen("tcp", o.Listen)
	if err != nil {
		return err
	}

	internalMux := cmux.New(internalListener)

	internalHTTPListener := internalMux.Match(cmux.HTTP1())
	go func() {
		if err := http.Serve(internalHTTPListener, internal); err != nil && err != http.ErrServerClosed {
			log.Printf("error: HTTP server exited: %v", err)
			os.Exit(1)
		}
	}()
	if internalTLS != nil {
		internalHTTPSListener := internalMux.Match(cmux.Any())
		go func() {
			s := &http.Server{
				TLSConfig: internalTLS,
				Handler:   internal,
			}
			if err := s.Serve(internalHTTPSListener); err != nil && err != http.ErrServerClosed {
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

	externalHTTPListener := externalMux.Match(cmux.HTTP1())
	go func() {
		if err := http.Serve(externalHTTPListener, external); err != nil && err != http.ErrServerClosed {
			log.Printf("error: HTTP server exited: %v", err)
			os.Exit(1)
		}
	}()
	externalHTTPSListener := externalMux.Match(cmux.Any())
	go func() {
		s := &http.Server{
			TLSConfig: externalTLS,
			Handler:   external,
		}
		if err := s.Serve(externalHTTPSListener); err != nil && err != http.ErrServerClosed {
			log.Printf("error: HTTP server exited: %v", err)
			os.Exit(1)
		}
	}()
	go func() {
		if err := externalMux.Serve(); err != nil && err != http.ErrServerClosed {
			log.Printf("error: external server exited: %v", err)
			os.Exit(1)
		}
	}()

	select {}

	return nil
}

// loadPrivateKey loads a private key from PEM/DER-encoded data.
func loadPrivateKey(data []byte) (interface{}, error) {
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
