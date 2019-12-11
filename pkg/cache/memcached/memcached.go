package memcached

import (
	"crypto/sha256"
	"fmt"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/pkg/errors"

	tcache "github.com/openshift/telemeter/pkg/cache"
)

// cache is a Cacher implemented on top of Memcached.
type cache struct {
	*memcache.Client
	expiration int32
}

// New creates a new Cache from a list of Memcached servers
// and key expiration time given in seconds.
func New(expiration int32, servers ...string) tcache.Cacher {
	return &cache{
		memcache.New(servers...),
		expiration,
	}
}

// Get returns a value from Memcached.
func (c *cache) Get(key string) ([]byte, bool, error) {
	key, err := hash(key)
	if err != nil {
		return nil, false, err
	}
	i, err := c.Client.Get(key)
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
	return c.Client.Set(&i)
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
