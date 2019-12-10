package cache

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httputil"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
)

// Cacher is able to get and set key value pairs.
type Cacher interface {
	Get(string) ([]byte, bool, error)
	Set(string, []byte) error
}

// KeyFunc generates a cache key from a http.Request.
type KeyFunc func(*http.Request) (string, error)

// RoundTripper is a http.RoundTripper than can get and set responses from a cache.
type RoundTripper struct {
	c    Cacher
	key  KeyFunc
	next http.RoundTripper

	l log.Logger

	// Metrics.
	cacheReadsTotal  *prometheus.CounterVec
	cacheWritesTotal *prometheus.CounterVec
}

// RoundTrip implements the RoundTripper interface.
func (r *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	key, err := r.key(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate key from request")
	}

	raw, ok, err := r.c.Get(key)
	if err != nil {
		r.cacheReadsTotal.WithLabelValues("error").Inc()
		return nil, errors.Wrap(err, "failed to retrieve value from cache")
	}

	if ok {
		r.cacheReadsTotal.WithLabelValues("hit").Inc()
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewBuffer(raw)), req)
		return resp, errors.Wrap(err, "failed to read response")
	}

	r.cacheReadsTotal.WithLabelValues("miss").Inc()
	resp, err := r.next.RoundTrip(req)
	if err == nil && resp.StatusCode/200 == 1 {
		// Try to cache the response but don't block.
		defer func() {
			raw, err := httputil.DumpResponse(resp, true)
			if err != nil {
				level.Error(r.l).Log("msg", "failed to dump response", "err", err)
				return
			}
			if err := r.c.Set(key, raw); err != nil {
				r.cacheWritesTotal.WithLabelValues("error").Inc()
				level.Error(r.l).Log("msg", "failed to set value in cache", "err", err)
				return
			}
			r.cacheWritesTotal.WithLabelValues("success").Inc()
		}()
	}
	return resp, err
}

// NewRoundTripper creates a new http.RoundTripper that returns http.Responses
// from a cache.
func NewRoundTripper(c Cacher, key KeyFunc, next http.RoundTripper, l log.Logger, reg prometheus.Registerer) http.RoundTripper {
	rt := &RoundTripper{
		c:    c,
		key:  key,
		next: next,
		l:    l,
		cacheReadsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cache_reads_total",
				Help: "The number of read requests made to the cache.",
			}, []string{"result"},
		),
		cacheWritesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cache_writes_total",
				Help: "The number of write requests made to the cache.",
			}, []string{"result"},
		),
	}

	if reg != nil {
		reg.MustRegister(rt.cacheReadsTotal, rt.cacheWritesTotal)
	}

	return rt
}
