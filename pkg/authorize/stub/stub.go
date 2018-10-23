package stub

import (
	"fmt"
	"log"

	"github.com/openshift/telemeter/pkg/fnv"
)

func Authorize(token, cluster string) (string, error) {
	subject, err := fnv.Hash(token)
	if err != nil {
		return "", fmt.Errorf("hashing token failed: %v", err)
	}
	log.Printf("warning: Performing no-op authentication, subject will be %s with cluster %s", subject, cluster)
	return subject, nil
}
