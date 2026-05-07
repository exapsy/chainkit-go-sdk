package scoring

import "time"

// PenaltyCategory classifies which scoring bucket a penalty belongs to.
type PenaltyCategory string

const (
	PenaltyCategoryHealth    PenaltyCategory = "health"
	PenaltyCategoryRateLimit PenaltyCategory = "rate_limit"
	PenaltyCategoryError     PenaltyCategory = "error"
	PenaltyCategoryLatency   PenaltyCategory = "latency"
)

// PenaltyRecord is a single penalty event with a human-readable reason.
// Metadata carries optional call-site context (address, network, touchpoint,
// operation) populated when the caller attached an [OperationMetadata] to the
// request context. It is omitted from JSON when empty.
//
// DecayFactor is the fraction of the original penalty still in effect at the
// time the record was retrieved (1.0 = fresh, 0.0 = fully decayed). It is
// computed by [Engine.GetPenaltyHistory] using the engine's DecayRate and
// DecayInterval; it is never persisted.
type PenaltyRecord struct {
	Timestamp   time.Time         `json:"timestamp"`
	Category    PenaltyCategory   `json:"category"`
	Reason      string            `json:"reason"`
	Amount      float64           `json:"amount"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	DecayFactor float64           `json:"decay_factor"` // 0.0–1.0; populated at query time
}

const defaultPenaltyHistorySize = 50

// penaltyHistory is an in-place ring buffer for recent penalty events.
//
// NOT thread-safe — callers must hold the parent ProviderScore's mutex.
// Zero allocs after initialisation: add() overwrites slots in-place once the
// buffer is full rather than allocating new backing arrays.
type penaltyHistory struct {
	buf  []PenaltyRecord
	head int  // index of the next write slot
	full bool // true once all slots have been written at least once
}

func newPenaltyHistory(size int) *penaltyHistory {
	if size <= 0 {
		size = defaultPenaltyHistorySize
	}
	return &penaltyHistory{buf: make([]PenaltyRecord, size)}
}

// add writes r into the ring, overwriting the oldest entry once the buffer is full.
func (h *penaltyHistory) add(r PenaltyRecord) {
	if len(h.buf) == 0 {
		return
	}
	h.buf[h.head] = r
	h.head = (h.head + 1) % len(h.buf)
	if !h.full && h.head == 0 {
		h.full = true
	}
}

// snapshot returns a copy of all records in chronological order (oldest first).
// Returns nil when the buffer is empty.
func (h *penaltyHistory) snapshot() []PenaltyRecord {
	if len(h.buf) == 0 || (!h.full && h.head == 0) {
		return nil
	}
	if !h.full {
		// Buffer not yet wrapped: valid entries are buf[0:head].
		out := make([]PenaltyRecord, h.head)
		copy(out, h.buf[:h.head])
		return out
	}
	// Buffer is full: head points at the oldest entry.
	out := make([]PenaltyRecord, len(h.buf))
	n := copy(out, h.buf[h.head:])
	copy(out[n:], h.buf[:h.head])
	return out
}
