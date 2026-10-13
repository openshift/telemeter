package fnv

import (
	"fmt"
	"hash"
	"hash/fnv"
	"strconv"
)

// Hash hashes the given text using a 64-bit FNV-1a hash.Hash.
func Hash(text string) (string, error) {
	return hashText(fnv.New64a(), text)
}

func hashText(h hash.Hash64, text string) (string, error) {
	if _, err := h.Write([]byte(text)); err != nil {
		return "", fmt.Errorf("hashing failed: %v", err)
	}
	return strconv.FormatUint(h.Sum64(), 32), nil
}
