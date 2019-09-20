package server

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/golang/snappy"
	clientmodel "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"github.com/openshift/telemeter/pkg/metricfamily"
	"github.com/openshift/telemeter/pkg/store"
	"github.com/openshift/telemeter/pkg/store/ratelimited"
	"github.com/openshift/telemeter/pkg/validate"
)

type Server struct {
	maxSampleAge time.Duration
	store        store.Store
	transformer  metricfamily.Transformer
	validator    validate.Validator
	nowFn        func() time.Time
	logger       log.Logger
}

func New(logger log.Logger, store store.Store, validator validate.Validator, transformer metricfamily.Transformer, maxSampleAge time.Duration) *Server {
	return &Server{
		maxSampleAge: maxSampleAge,
		store:        store,
		transformer:  transformer,
		validator:    validator,
		nowFn:        time.Now,
		logger:       log.With(logger, "component", "http/server"),
	}
}

func NewNonExpiring(logger log.Logger, store store.Store, validator validate.Validator, transformer metricfamily.Transformer, maxSampleAge time.Duration) *Server {
	return &Server{
		maxSampleAge: maxSampleAge,
		store:        store,
		transformer:  transformer,
		validator:    validator,
		nowFn:        nil,
		logger:       log.With(logger, "component", "http/server"),
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
	var filter metricfamily.MultiTransformer
	if s.nowFn != nil {
		filter.With(metricfamily.NewDropExpiredSamples(s.nowFn().Add(-s.maxSampleAge)))
		filter.With(metricfamily.TransformerFunc(metricfamily.PackMetrics))
	}

	ps, err := s.store.ReadMetrics(ctx, minTimeMs)
	if err != nil {
		level.Error(s.logger).Log("msg", "error reading metrics", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, p := range ps {
		for _, family := range p.Families {
			if family == nil {
				continue
			}
			if ok, err := filter.Transform(family); err != nil || !ok {
				continue
			}
			if err := encoder.Encode(family); err != nil {
				level.Error(s.logger).Log("msg", "error encoding metrics family", "err", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				continue
			}
		}
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

	partitionKey, transforms, err := s.validator.Validate(ctx, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var t metricfamily.MultiTransformer
	t.With(transforms)
	t.With(s.transformer)

	// read the response into memory
	format := expfmt.ResponseFormat(req.Header)
	var r io.Reader = req.Body
	if req.Header.Get("Content-Encoding") == "snappy" {
		r = snappy.NewReader(r)
	}
	decoder := expfmt.NewDecoder(r, format)

	errCh := make(chan error)
	go func() { errCh <- s.decodeAndStoreMetrics(ctx, partitionKey, decoder, t) }()

	select {
	case <-ctx.Done():
		http.Error(w, "Timeout while storing metrics", http.StatusInternalServerError)
		level.Error(s.logger).Log("msg", "timeout processing incoming request")
		return
	case err := <-errCh:
		switch err {
		case nil:
			break
		case ratelimited.ErrWriteLimitReached(partitionKey):
			http.Error(w, err.Error(), http.StatusTooManyRequests)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
}

func (s *Server) decodeAndStoreMetrics(ctx context.Context, partitionKey string, decoder expfmt.Decoder, transformer metricfamily.Transformer) error {
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

	if err := metricfamily.Filter(families, transformer); err != nil {
		return err
	}
	families = metricfamily.Pack(families)

	return s.store.WriteMetrics(ctx, &store.PartitionedMetrics{
		PartitionKey: partitionKey,
		Families:     families,
	})
}
