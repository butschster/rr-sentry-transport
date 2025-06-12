package sentry_transport

import (
	"time"
)

// SentryEvent represents a single Sentry event to be transmitted
type SentryEvent struct {
	ID      string `json:"event_id"`
	Type    string `json:"type"`
	Payload string `json:"payload"` // JSON payload generated on PHP side
}

// SendResult represents the result of a send operation
type SendResult struct {
	Success   bool   `json:"success"`
	EventID   string `json:"event_id"`
	Error     string `json:"error,omitempty"`
	RateLimit bool   `json:"rate_limit,omitempty"`
}

// QueuedEvent represents an event in the processing queue
type QueuedEvent struct {
	Event      *SentryEvent
	Attempts   int
	LastAttempt time.Time
	NextRetry   time.Time
}

// RateLimitInfo represents rate limiting information for a category
type RateLimitInfo struct {
	Category     string
	DisabledUntil time.Time
}

// TransportMetrics represents plugin metrics
type TransportMetrics struct {
	EventsSent      int64
	EventsFailed    int64
	EventsRateLimit int64
	QueueLength     int
	TotalRetries    int64
}
