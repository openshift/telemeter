package server

import (
	"context"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/openshift/telemeter/pkg/transform"
)

var (
	metricSamples = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "telemeter_server_samples",
		Help: "Tracks the number of samples processed by this server.",
	}, []string{"phase"})
)

func init() {
	prometheus.MustRegister(metricSamples)
}

// maxSampleAge is the maximum age of a sample that we can report via federation.
const maxSampleAge = 10 * time.Minute

type Store interface {
	ReadMetrics(ctx context.Context, minTimestampMs int64, fn func(partitionKey string, families []*clientmodel.MetricFamily) error) error
	WriteMetrics(ctx context.Context, partitionKey string, families []*clientmodel.MetricFamily) error
}

type UploadValidator interface {
	ValidateUpload(ctx context.Context, req *http.Request) (string, []transform.Interface, error)
}

type Server struct {
	store     Store
	validator UploadValidator
	nowFn     func() time.Time
}

func New(store Store, validator UploadValidator) *Server {
	return &Server{
		store:     store,
		validator: validator,
		nowFn:     time.Now,
	}
}

func NewNonExpiring(store Store, validator UploadValidator) *Server {
	return &Server{
		store:     store,
		validator: validator,
		nowFn:     nil,
	}
}

func (s *Server) Get(w http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	format := expfmt.Negotiate(req.Header)
	encoder := expfmt.NewEncoder(w, format)
	ctx := context.Background()

	// samples older than 10 minutes must be ignored
	var minTimeMs int64
	var filter transform.All
	if s.nowFn != nil {
		minTime := s.nowFn().Add(-maxSampleAge)
		minTimeMs = minTime.UnixNano() / int64(time.Millisecond)
		filter = transform.All{
			transform.NewDropExpiredSamples(minTime),
			transform.PackMetrics,
		}
	}

	err := s.store.ReadMetrics(ctx, minTimeMs, func(partitionKey string, families []*clientmodel.MetricFamily) error {
		for _, family := range families {
			if family == nil {
				continue
			}
			if ok, err := filter.Transform(family); err != nil || !ok {
				continue
			}
			if err := encoder.Encode(family); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (s *Server) Post(w http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	defer req.Body.Close()

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	partitionKey, transforms, err := s.validator.ValidateUpload(ctx, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// read the response into memory
	format := expfmt.ResponseFormat(req.Header)
	var r io.Reader = req.Body
	if req.Header.Get("Content-Encoding") == "snappy" {
		r = snappy.NewReader(r)
	}
	decoder := expfmt.NewDecoder(r, format)

	errCh := make(chan error)
	go func() { errCh <- decodeAndStoreMetrics(ctx, partitionKey, decoder, transforms, s.store) }()

	select {
	case <-ctx.Done():
		http.Error(w, "Timeout while storing metrics", http.StatusInternalServerError)
		log.Printf("timeout processing incoming request")
		return
	case err := <-errCh:
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
}

func decodeAndStoreMetrics(ctx context.Context, partitionKey string, decoder expfmt.Decoder, transforms []transform.Interface, store Store) error {
	families := make([]*clientmodel.MetricFamily, 0, 100)
	for {
		family := &clientmodel.MetricFamily{}
		families = append(families, family)
		if err := decoder.Decode(family); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}

	metricSamples.WithLabelValues("received").Add(float64(transform.Metrics(families)))

	// filter the list
	for _, transform := range transforms {
		for i, family := range families {
			ok, err := transform.Transform(family)
			if err != nil {
				return err
			}
			if !ok {
				families[i] = nil
				continue
			}
		}
	}

	families = transform.Pack(families)

	return store.WriteMetrics(ctx, partitionKey, families)
}
