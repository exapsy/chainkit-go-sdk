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

	h := fnv.New32a()
	_, err = h.Write([]byte(data))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to write to hash: %w", err)
	}

	hash := h.Sum32()

	changeIndex := hash / 0x80000000  // First 31 bits for change
	addressIndex := hash % 0x80000000 // Last 31 bits for address index

	return changeIndex, addressIndex, nil
}
