package bitcoin

import (
	"errors"
	"fmt"
	"hash/fnv"
)

// DeriveHDIndices derives BIP32 HD wallet indices from an arbitrary data string.
// The caller is responsible for concatenating any domain-specific keys before
// calling this function (e.g. sessionID + fileID).
//
// Returns (index, childIndex, error).
func DeriveHDIndices(data string) (index uint32, childIndex uint32, err error) {
	if data == "" {
		return 0, 0, errors.New("data cannot be empty")
	}

	h := fnv.New64a()
	_, err = h.Write([]byte(data))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to write to hash: %w", err)
	}

	hash := h.Sum64()

	// Split 64-bit hash into two independent 31-bit indices (BIP32 requires < 0x80000000).
	hi := uint32(hash >> 32)
	lo := uint32(hash)
	index = hi & 0x7FFFFFFF
	childIndex = lo & 0x7FFFFFFF

	return index, childIndex, nil
}
