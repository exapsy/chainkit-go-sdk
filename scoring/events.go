package scoring

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// ScoreEventType represents the type of scoring event that occurred
type ScoreEventType string

const (
	// EventHealthCheckFailed indicates a general health check failure
	EventHealthCheckFailed ScoreEventType = "healthcheck_failed"

	// EventHealthCheck429 indicates a rate limit response (429) during health check
	EventHealthCheck429 ScoreEventType = "healthcheck_429"

	// EventHealthCheckAuthFail indicates authentication/authorization failure (401/403)
	EventHealthCheckAuthFail ScoreEventType = "healthcheck_auth"

	// EventHealthCheckTimeout indicates the health check timed out
	EventHealthCheckTimeout ScoreEventType = "healthcheck_timeout"

	// EventOperationFailed indicates a provider operation failed
	EventOperationFailed ScoreEventType = "operation_failed"

	// EventOperationAuthFail indicates an operation failed due to invalid credentials.
	// Unlike transient failures, this signals a configuration problem and triggers
	// a higher penalty and faster circuit-breaker escalation.
	EventOperationAuthFail ScoreEventType = "operation_auth_fail"

	// EventOperationSuccess indicates a provider operation succeeded
	EventOperationSuccess ScoreEventType = "operation_success"

	// EventSlowResponse indicates response time was significantly slower than peers
	EventSlowResponse ScoreEventType = "slow_response"

	// EventRateLimited indicates the operation was rate limited (429)
	EventRateLimited ScoreEventType = "rate_limited"
)

// String returns the string representation of the event type
func (e ScoreEventType) String() string {
	return string(e)
}

// ScoreEvent represents an event that affects a provider's score
type ScoreEvent struct {
	// Type of the event
	Type ScoreEventType

	// Provider name that this event applies to
	Provider string

	// Timestamp when the event occurred
	Timestamp time.Time

	// ResponseTime for the operation (if applicable)
	ResponseTime time.Duration

	// HTTPStatus code (if applicable, 0 if not HTTP-related)
	HTTPStatus int

	// Error that occurred (if applicable)
	Error error

	// Metadata for additional context (optional)
	Metadata map[string]interface{}
}

// ClassifyHealthCheckEvent classifies a health check result into a ScoreEvent
func ClassifyHealthCheckEvent(providerName string, httpStatus int, responseTime time.Duration, err error) ScoreEvent {
	event := ScoreEvent{
		Provider:     providerName,
		Timestamp:    time.Now(),
		ResponseTime: responseTime,
		HTTPStatus:   httpStatus,
		Error:        err,
	}

	switch {
	case httpStatus == http.StatusTooManyRequests:
		event.Type = EventHealthCheck429
	case httpStatus == http.StatusUnauthorized || httpStatus == http.StatusForbidden:
		event.Type = EventHealthCheckAuthFail
	case err != nil && errors.Is(err, context.DeadlineExceeded):
		event.Type = EventHealthCheckTimeout
	case httpStatus >= 400 || err != nil:
		event.Type = EventHealthCheckFailed
	default:
		event.Type = EventOperationSuccess
	}

	return event
}

// ClassifyOperationEvent classifies a provider operation result into a ScoreEvent
func ClassifyOperationEvent(providerName string, responseTime time.Duration, err error) ScoreEvent {
	event := ScoreEvent{
		Provider:     providerName,
		Timestamp:    time.Now(),
		ResponseTime: responseTime,
		Error:        err,
	}

	if err != nil {
		errMsg := err.Error()
		if containsAuthFailureIndicator(errMsg) {
			event.Type = EventOperationAuthFail
		} else if containsRateLimitIndicator(errMsg) {
			event.Type = EventRateLimited
		} else {
			event.Type = EventOperationFailed
		}
	} else {
		event.Type = EventOperationSuccess
	}

	return event
}

// containsAuthFailureIndicator checks if an error message indicates an auth failure.
// This is a string-matching fallback for providers that don't wrap ErrAuthFailure.
// Providers that use ErrAuthFailure are detected via errors.Is in the manager before
// ClassifyOperationEvent is called, so this catches only third-party or legacy cases.
func containsAuthFailureIndicator(errMsg string) bool {
	indicators := []string{
		"authentication failed",
		"unauthorized",
		"invalid client",
		"invalid credentials",
		"access denied",
	}
	for _, indicator := range indicators {
		if contains(errMsg, indicator) {
			return true
		}
	}
	return false
}

// containsRateLimitIndicator checks if an error message indicates rate limiting
func containsRateLimitIndicator(errMsg string) bool {
	indicators := []string{
		"rate limit",
		"too many requests",
		"429",
		"quota exceeded",
		"throttled",
	}

	for _, indicator := range indicators {
		if contains(errMsg, indicator) {
			return true
		}
	}
	return false
}

// contains is a simple case-insensitive substring check
func contains(s, substr string) bool {
	// Simple implementation - can be replaced with strings.Contains if case-insensitive not needed
	return len(s) >= len(substr) && (s == substr ||
		len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
