package cloudagent

import (
	"crypto/rand"
	"encoding/base32"
	"sync"
	"time"
)

// We mint event ids and batch ids inline rather than pulling in github.com/oklog/ulid
// to keep the SDK's transitive dependencies small. The output is a 26-char
// Crockford-base32-style id with a millisecond timestamp prefix and 80 bits of
// random suffix — the same shape ULID provides, without the package weight.

var (
	idMu      sync.Mutex
	lastMS    uint64
	lastEntropy [10]byte
)

const idAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var idEncoding = base32.NewEncoding(idAlphabet).WithPadding(base32.NoPadding)

// newEventID returns a ULID-shaped 26-char id. The implementation is monotonic
// within a single millisecond: if called twice in the same ms, the entropy
// component increments rather than randomising, so generated ids sort in
// emission order even at high rates.
func newEventID() string {
	idMu.Lock()
	defer idMu.Unlock()

	ms := uint64(time.Now().UnixMilli())
	if ms == lastMS {
		// Increment entropy as a big-endian 80-bit number.
		for i := len(lastEntropy) - 1; i >= 0; i-- {
			lastEntropy[i]++
			if lastEntropy[i] != 0 {
				break
			}
		}
	} else {
		_, _ = rand.Read(lastEntropy[:])
		lastMS = ms
	}

	var raw [16]byte
	// 48-bit timestamp big-endian
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	copy(raw[6:], lastEntropy[:])
	return idEncoding.EncodeToString(raw[:])
}
