package memcached

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/pkg/errors"

	tcache "github.com/openshift/telemeter/pkg/cache"
)

// cache is a Cacher implemented on top of Memcached.
type cache struct {
	client     *memcache.Client
	expiration int32
	mu         sync.RWMutex
}

// New creates a new Cacher from a list of Memcached servers,
// a key expiration time given in seconds, a DNS refresh interval,
// and a context. The Cacher will continue to update the DNS entries
// for the Memcached servers every interval as long as the context is valid.
func New(ctx context.Context, interval, expiration int32, servers ...string) tcache.Cacher {
	c := &cache{
		client:     memcache.New(servers...),
		expiration: expiration,
	}

	if interval > 0 {
		go func() {
			t := time.NewTicker(time.Duration(interval) * time.Second)
			for {
				select {
				case <-t.C:
					c.mu.Lock()
					c.client = memcache.New(servers...)
					c.mu.Unlock()
				case <-ctx.Done():
					t.Stop()
					return
				}
			}
		}()
	}
	return c
}

// Get returns a value from Memcached.
func (c *cache) Get(key string) ([]byte, bool, error) {
	key, err := hash(key)
	if err != nil {
		return nil, false, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	i, err := c.client.Get(key)
	if err != nil {
		if err == memcache.ErrCacheMiss {
			return nil, false, nil
		}
		return nil, false, err
	}

	return i.Value, true, nil
}

// Set sets a value in Memcached.
func (c *cache) Set(key string, value []byte) error {
	key, err := hash(key)
	if err != nil {
		return err
	}
	i := memcache.Item{
		Key:        key,
		Value:      value,
		Expiration: c.expiration,
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client.Set(&i)
}

// hashKey hashes the given key to ensure that it is less than 250 bytes,
// as Memcached cannot handle longer keys.
func hash(key string) (string, error) {
	h := sha256.New()
	if _, err := h.Write([]byte(key)); err != nil {
		return "", errors.Wrap(err, "failed to hash key")
	}
	return fmt.Sprintf("%x", (h.Sum(nil))), nil
}
