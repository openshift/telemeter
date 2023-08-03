package stub

import (
	"fmt"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/openshift/telemeter/pkg/fnv"
)

func AuthorizeFn(logger log.Logger) func(token, cluster string) (string, error) {
	return func(token, cluster string) (string, error) {
		subject, err := fnv.Hash(token)
		if err != nil {
			return "", fmt.Errorf("hashing token failed: %v", err)
		}
		level.Warn(logger).Log("msg", "performing no-op authentication", "subject", subject, "cluster", cluster)
		return subject, nil
	}
}
