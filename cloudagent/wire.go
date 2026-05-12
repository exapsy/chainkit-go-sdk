package cloudagent

import (
	"encoding/json"
	"time"
)

// Wire-format types. These mirror chainkit-cloud-srv's services/ingest/types.go
// exactly — keep them in sync. The schema is versioned via the schema field;
// any breaking change bumps the major and gets a new constant here.
//
// We could share a single types package across SDK and cloud-srv but that adds
// a runtime coupling that doesn't pay off for ~30 lines of struct literals.
// Documented contract in chainkit-go-sdk/docs/cloud-ingest-schema.md is the
// source of truth; both sides hand-implement against it.

const schemaV1 = "chainkit.ingest.v1"

type wireBatch struct {
	Schema  string          `json:"schema"`
	Agent   string          `json:"agent"`
	BatchID string          `json:"batch_id"`
	SentAt  time.Time       `json:"sent_at"`
	Events  []wireEnvelope  `json:"events"`
}

type wireEnvelope struct {
	ID    string         `json:"id"`
	T     time.Time      `json:"t"`
	Kind  string         `json:"kind"` // "req" | "score"
	Req   *wireRequest   `json:"req,omitempty"`
	Score *wireScore     `json:"score,omitempty"`
}

type wireRequest struct {
	Provider  string `json:"provider"`
	Operation string `json:"operation"`
	Chain     string `json:"chain"`
	Network   string `json:"network"`
	OK        bool   `json:"ok"`
	DurMS     int32  `json:"dur_ms"`
	Attempts  int16  `json:"attempts"`
	Err       string `json:"err"`
}

type wireScore struct {
	Type           string         `json:"type"`
	Provider       string         `json:"provider"`
	EventType      string         `json:"event_type,omitempty"`
	ScoreType      string         `json:"score_type,omitempty"`
	Operation      string         `json:"operation,omitempty"`
	Store          string         `json:"store,omitempty"`
	OldValue       float64        `json:"old_value,omitempty"`
	NewValue       float64        `json:"new_value,omitempty"`
	Score          float64        `json:"score_after,omitempty"`
	RTMS           int32          `json:"rt_ms,omitempty"`
	Success        bool           `json:"success,omitempty"`
	CacheHit       bool           `json:"cache_hit,omitempty"`
	Rank           int            `json:"rank,omitempty"`
	TotalProviders int            `json:"total_providers,omitempty"`
	StoreErr       string         `json:"store_err,omitempty"`
}

// buildBatch turns a slice of in-memory Events into the JSON-encoded wire
// payload. The agent's deployment-level Chain/Network labels from opts stamp
// every req event; score events get Provider, type, and any numeric fields
// the underlying scoring/metrics.Recorder method populated.
//
// idGen is called once per event to mint a wire-level event id. In production
// this is newEventID (ULID); tests can inject a deterministic generator.
func buildBatch(events []Event, opts Options, batchID string, sentAt time.Time, idGen func() string) ([]byte, error) {
	wb := wireBatch{
		Schema:  schemaV1,
		Agent:   opts.AgentName,
		BatchID: batchID,
		SentAt:  sentAt,
		Events:  make([]wireEnvelope, 0, len(events)),
	}
	for _, ev := range events {
		switch {
		case ev.Request != nil:
			r := ev.Request
			wb.Events = append(wb.Events, wireEnvelope{
				ID:   idGen(),
				T:    ev.CapturedAt.UTC(),
				Kind: "req",
				Req: &wireRequest{
					Provider:  r.Provider,
					Operation: r.Operation,
					Chain:     opts.Chain,
					Network:   opts.Network,
					OK:        r.Success,
					DurMS:     int32(r.Duration.Milliseconds()),
					Attempts:  int16(r.AttemptCount),
					Err:       r.ErrorClass,
				},
			})
		case ev.Score != nil:
			s := ev.Score
			ws := &wireScore{
				Type:           string(s.Kind),
				Provider:       s.Provider,
				EventType:      s.EventType,
				ScoreType:      s.ScoreType,
				Operation:      s.Operation,
				Store:          s.Store,
				OldValue:       s.OldValue,
				NewValue:       s.NewValue,
				Score:          s.Score,
				Success:        s.Success,
				CacheHit:       s.CacheHit,
				Rank:           s.Rank,
				TotalProviders: s.TotalProviders,
				StoreErr:       s.StoreErrText,
			}
			if s.Latency > 0 {
				ws.RTMS = int32(s.Latency.Milliseconds())
			}
			wb.Events = append(wb.Events, wireEnvelope{
				ID:    idGen(),
				T:     ev.CapturedAt.UTC(),
				Kind:  "score",
				Score: ws,
			})
		}
	}
	return json.Marshal(wb)
}
