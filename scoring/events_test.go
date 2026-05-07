package scoring

import (
	"errors"
	"testing"
	"time"
)

func TestContainsAuthFailureIndicator(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected bool
	}{
		// Standard auth errors
		{
			name:     "authentication failed",
			errMsg:   "authentication failed",
			expected: true,
		},
		{
			name:     "unauthorized",
			errMsg:   "unauthorized request",
			expected: true,
		},
		{
			name:     "invalid credentials",
			errMsg:   "invalid credentials provided",
			expected: true,
		},
		{
			name:     "access denied",
			errMsg:   "access denied to resource",
			expected: true,
		},
		// Bitrefcom-specific error
		{
			name:     "bitrefcom api key not in allow list",
			errMsg:   `HTTP 403: {"error":"The API key was not found in the allow list"}`,
			expected: true,
		},
		// Generic API key errors
		{
			name:     "invalid api key",
			errMsg:   "invalid api key provided",
			expected: true,
		},
		{
			name:     "missing api key",
			errMsg:   "missing api key in request",
			expected: true,
		},
		{
			name:     "api key error",
			errMsg:   "api key authentication failed",
			expected: true,
		},
		// HTTP status codes
		{
			name:     "http 401",
			errMsg:   "HTTP 401: unauthorized",
			expected: true,
		},
		{
			name:     "http 403",
			errMsg:   "HTTP 403: forbidden",
			expected: true,
		},
		{
			name:     "forbidden keyword",
			errMsg:   "forbidden resource",
			expected: true,
		},
		// Non-auth errors (should return false)
		{
			name:     "timeout error",
			errMsg:   "request timed out",
			expected: false,
		},
		{
			name:     "network error",
			errMsg:   "network connection failed",
			expected: false,
		},
		{
			name:     "rate limit",
			errMsg:   "rate limit exceeded",
			expected: false,
		},
		{
			name:     "internal server error",
			errMsg:   "HTTP 500: internal server error",
			expected: false,
		},
		{
			name:     "empty string",
			errMsg:   "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsAuthFailureIndicator(tt.errMsg)
			if result != tt.expected {
				t.Errorf("containsAuthFailureIndicator(%q) = %v, want %v", tt.errMsg, result, tt.expected)
			}
		})
	}
}

func TestClassifyOperationEvent_AuthFailure(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectedType  ScoreEventType
		expectedError error
	}{
		{
			name:          "bitrefcom 403 error",
			err:           errors.New(`HTTP 403: {"error":"The API key was not found in the allow list"}`),
			expectedType:  EventOperationAuthFail,
			expectedError: errors.New(`HTTP 403: {"error":"The API key was not found in the allow list"}`),
		},
		{
			name:          "generic auth failure",
			err:           errors.New("authentication failed"),
			expectedType:  EventOperationAuthFail,
			expectedError: errors.New("authentication failed"),
		},
		{
			name:          "api key error",
			err:           errors.New("invalid api key"),
			expectedType:  EventOperationAuthFail,
			expectedError: errors.New("invalid api key"),
		},
		{
			name:          "http 401",
			err:           errors.New("HTTP 401: unauthorized"),
			expectedType:  EventOperationAuthFail,
			expectedError: errors.New("HTTP 401: unauthorized"),
		},
		{
			name:          "rate limit error",
			err:           errors.New("rate limit exceeded"),
			expectedType:  EventRateLimited,
			expectedError: errors.New("rate limit exceeded"),
		},
		{
			name:          "generic error",
			err:           errors.New("connection timeout"),
			expectedType:  EventOperationFailed,
			expectedError: errors.New("connection timeout"),
		},
		{
			name:          "nil error (success)",
			err:           nil,
			expectedType:  EventOperationSuccess,
			expectedError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := ClassifyOperationEvent("TestProvider", 0, tt.err)

			if event.Type != tt.expectedType {
				t.Errorf("ClassifyOperationEvent() type = %v, want %v", event.Type, tt.expectedType)
			}

			if event.Provider != "TestProvider" {
				t.Errorf("ClassifyOperationEvent() provider = %v, want TestProvider", event.Provider)
			}

			if tt.expectedError == nil && event.Error != nil {
				t.Errorf("ClassifyOperationEvent() error = %v, want nil", event.Error)
			}

			if tt.expectedError != nil && event.Error == nil {
				t.Errorf("ClassifyOperationEvent() error = nil, want %v", tt.expectedError)
			}

			if tt.expectedError != nil && event.Error != nil && event.Error.Error() != tt.expectedError.Error() {
				t.Errorf("ClassifyOperationEvent() error = %v, want %v", event.Error, tt.expectedError)
			}
		})
	}
}

// TestRecordEvent_MetadataPropagated verifies that Metadata set on a ScoreEvent
// flows through RecordEvent into the in-memory PenaltyRecord ring buffer.
func TestRecordEvent_MetadataPropagated(t *testing.T) {
	engine := NewEngine()
	engine.RegisterProvider("Mempool", 1)

	want := map[string]interface{}{
		"operation":  "GetBalance",
		"address":    "tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx",
		"network":    "testnet3",
		"touchpoint": "payment_monitor",
	}

	engine.RecordEvent(ScoreEvent{
		Type:      EventOperationFailed,
		Provider:  "Mempool",
		Timestamp: time.Now(),
		Error:     errors.New("HTTP 400: Address on invalid network"),
		Metadata:  want,
	})

	records, ok := engine.GetPenaltyHistory("Mempool", 10, time.Time{}, "")
	if !ok {
		t.Fatal("GetPenaltyHistory returned false for registered provider")
	}
	if len(records) == 0 {
		t.Fatal("expected at least one penalty record")
	}

	got := records[0].Metadata
	if got == nil {
		t.Fatal("PenaltyRecord.Metadata is nil; expected it to be populated")
	}
	for _, key := range []string{"operation", "address", "network", "touchpoint"} {
		if got[key] != want[key] {
			t.Errorf("Metadata[%q] = %q, want %q", key, got[key], want[key])
		}
	}
}

// TestRecordEvent_NoMetadata verifies that Metadata is nil when the event carries
// no Metadata map — ensuring the omitempty JSON tag omits the field.
func TestRecordEvent_NoMetadata(t *testing.T) {
	engine := NewEngine()
	engine.RegisterProvider("Blockstream", 1)

	engine.RecordEvent(ScoreEvent{
		Type:      EventOperationFailed,
		Provider:  "Blockstream",
		Timestamp: time.Now(),
		Error:     errors.New("connection timeout"),
	})

	records, ok := engine.GetPenaltyHistory("Blockstream", 10, time.Time{}, "")
	if !ok {
		t.Fatal("GetPenaltyHistory returned false for registered provider")
	}
	if len(records) == 0 {
		t.Fatal("expected at least one penalty record")
	}
	if records[0].Metadata != nil {
		t.Errorf("expected Metadata to be nil when no context was provided, got %v", records[0].Metadata)
	}
}

// TestGetPenaltyHistory_FilterByCategory verifies category-based filtering.
func TestGetPenaltyHistory_FilterByCategory(t *testing.T) {
	engine := NewEngine()
	engine.RegisterProvider("Blockcypher", 1)

	engine.RecordEvent(ScoreEvent{
		Type: EventOperationFailed, Provider: "Blockcypher",
		Timestamp: time.Now(), Error: errors.New("network error"),
	})
	engine.RecordEvent(ScoreEvent{
		Type: EventRateLimited, Provider: "Blockcypher",
		Timestamp: time.Now(), Error: errors.New("rate limit"),
	})

	errorOnly, _ := engine.GetPenaltyHistory("Blockcypher", 10, time.Time{}, PenaltyCategoryError)
	for _, r := range errorOnly {
		if r.Category != PenaltyCategoryError {
			t.Errorf("filter by error category returned record with category %q", r.Category)
		}
	}

	all, _ := engine.GetPenaltyHistory("Blockcypher", 10, time.Time{}, "")
	if len(all) < len(errorOnly) {
		t.Errorf("unfiltered result (%d) should be >= filtered result (%d)", len(all), len(errorOnly))
	}
}

// TestGetPenaltyHistory_UnknownProvider verifies that a false bool is returned
// for providers that were never registered.
func TestGetPenaltyHistory_UnknownProvider(t *testing.T) {
	engine := NewEngine()
	_, ok := engine.GetPenaltyHistory("Ghost", 10, time.Time{}, "")
	if ok {
		t.Error("expected false for unregistered provider, got true")
	}
}

// TestGetPenaltyHistory_MostRecentFirst verifies chronological ordering.
func TestGetPenaltyHistory_MostRecentFirst(t *testing.T) {
	engine := NewEngine()
	engine.RegisterProvider("Mempool", 1)

	for i := 0; i < 3; i++ {
		engine.RecordEvent(ScoreEvent{
			Type: EventOperationFailed, Provider: "Mempool",
			Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond),
			Error:     errors.New("err"),
		})
	}

	records, _ := engine.GetPenaltyHistory("Mempool", 10, time.Time{}, "")
	for i := 1; i < len(records); i++ {
		if records[i].Timestamp.After(records[i-1].Timestamp) {
			t.Errorf("records not sorted most-recent-first: record[%d] (%v) is after record[%d] (%v)",
				i, records[i].Timestamp, i-1, records[i-1].Timestamp)
		}
	}
}

// TestStringifyMetadata verifies the conversion helper.
func TestStringifyMetadata(t *testing.T) {
	if got := stringifyMetadata(nil); got != nil {
		t.Errorf("nil map should return nil, got %v", got)
	}
	if got := stringifyMetadata(map[string]interface{}{}); got != nil {
		t.Errorf("empty map should return nil, got %v", got)
	}

	in := map[string]interface{}{"k": "v", "n": 42}
	out := stringifyMetadata(in)
	if out["k"] != "v" {
		t.Errorf("expected out[k] = v, got %q", out["k"])
	}
	if out["n"] != "42" {
		t.Errorf("expected out[n] = 42, got %q", out["n"])
	}
}
