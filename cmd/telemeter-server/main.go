package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/smarterclayton/telemeter/pkg/authorizer/jwt"
	"github.com/smarterclayton/telemeter/pkg/authorizer/remoteauthserver"
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
	cmd.Flags().StringSliceVar(&opt.LabelFlag, "label", opt.LabelFlag, "Labels to add to each outgoing metric, in key=value form.")
	cmd.Flags().StringVar(&opt.PartitionKey, "partition-label", opt.PartitionKey, "The label to separate incoming data on. This label will be required for callers to include.")
	cmd.Flags().StringVar(&opt.StorageDir, "storage-dir", opt.StorageDir, "The directory to persist incoming metrics. If not specified metrics will only live in memory.")

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

type Options struct {
	Listen         string
	ListenInternal string
	LimitBytes     int64

	PartitionKey string
	LabelFlag    []string
	Labels       map[string]string

	StorageDir string

	TokenExpireSeconds int64
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

	signer, authorizer, err := jwt.New("federate")
	if err != nil {
		return fmt.Errorf("unable to create signer: %v", err)
	}

	auth := remoteauthserver.New(o.PartitionKey, nil, nil, o.TokenExpireSeconds, signer, o.Labels)
	validator := untrusted.NewValidator(o.PartitionKey, o.Labels, o.LimitBytes, 24*time.Hour)
	var store server.Store
	if len(o.StorageDir) > 0 {
		store = server.NewDiskStore(o.StorageDir)
	} else {
		store = server.NewMemoryStore()
	}
	server := server.New(store, validator)

	internalPathJSON, _ := json.MarshalIndent(Paths{Paths: []string{"/", "/federate", "/metrics", "/debug/pprof", "/healthz", "/healthz/ready"}}, "", "  ")
	externalPathJSON, _ := json.MarshalIndent(Paths{Paths: []string{"/", "/authorize", "/upload", "/healthz", "/healthz/ready"}}, "", "  ")

	internalProtected := http.NewServeMux()
	telemeterhttp.AddDebug(internalProtected)
	internalProtected.Handle("/federate", http.HandlerFunc(server.Get))

	internal := http.NewServeMux()
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

	externalProtected := http.NewServeMux()
	externalProtected.Handle("/upload", http.HandlerFunc(server.Post))
	externalProtectedHandler := httpauthorizer.New(externalProtected, authorizer)

	external := http.NewServeMux()
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

	go func() {
		if err := http.ListenAndServe(o.ListenInternal, internal); err != nil && err != http.ErrServerClosed {
			log.Printf("error: server exited: %v", err)
			os.Exit(1)
		}
	}()

	go func() {
		if err := http.ListenAndServe(o.Listen, external); err != nil && err != http.ErrServerClosed {
			log.Printf("error: server exited: %v", err)
			os.Exit(1)
		}
	}()

	select {}

	return nil
}
