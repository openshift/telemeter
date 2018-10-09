package stub

import (
	"hash/fnv"
	"log"
	"strconv"
)

func Authorize(token, cluster string) (string, error) {
	subject := fnvHash(token)
	log.Printf("warning: Performing no-op authentication, subject will be %s with cluster %s", subject, cluster)
	return subject, nil
}

func fnvHash(text string) string {
	h := fnv.New64a()
	h.Write([]byte(text))
	return strconv.FormatUint(h.Sum64(), 32)
}
