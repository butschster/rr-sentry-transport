package sentry_transport

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// RateLimiter handles Sentry rate limiting based on response headers
type RateLimiter struct {
	mu         sync.RWMutex
	rateLimits map[string]time.Time // category -> disabled until time
	logger     *zap.Logger
}

// NewRateLimiter creates a new rate limiter instance
func NewRateLimiter(logger *zap.Logger) *RateLimiter {
	return &RateLimiter{
		rateLimits: make(map[string]time.Time),
		logger:     logger,
	}
}

// IsRateLimited checks if the given event type is currently rate limited
func (rl *RateLimiter) IsRateLimited(eventType string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	now := time.Now()

	// Check specific category rate limit
	if disabledUntil, exists := rl.rateLimits[eventType]; exists && disabledUntil.After(now) {
		return true
	}

	// Check global rate limit
	if disabledUntil, exists := rl.rateLimits["all"]; exists && disabledUntil.After(now) {
		return true
	}

	return false
}

// GetDisabledUntil returns the time until which the event type is disabled
func (rl *RateLimiter) GetDisabledUntil(eventType string) time.Time {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	now := time.Now()
	var maxDisabledUntil time.Time

	// Check specific category
	if disabledUntil, exists := rl.rateLimits[eventType]; exists && disabledUntil.After(now) {
		maxDisabledUntil = disabledUntil
	}

	// Check global rate limit
	if disabledUntil, exists := rl.rateLimits["all"]; exists && disabledUntil.After(now) {
		if disabledUntil.After(maxDisabledUntil) {
			maxDisabledUntil = disabledUntil
		}
	}

	return maxDisabledUntil
}

// HandleRateLimitHeaders processes Sentry rate limit headers
func (rl *RateLimiter) HandleRateLimitHeaders(headers map[string][]string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Handle X-Sentry-Rate-Limits header
	if rateLimits, exists := headers["X-Sentry-Rate-Limits"]; exists && len(rateLimits) > 0 {
		rl.parseRateLimitHeader(rateLimits[0], now)
		return
	}

	// Handle Retry-After header as fallback
	if retryAfter, exists := headers["Retry-After"]; exists && len(retryAfter) > 0 {
		rl.parseRetryAfterHeader(retryAfter[0], now)
	}
}

// parseRateLimitHeader parses the X-Sentry-Rate-Limits header
// Format: "retry_after:categories:scope:reason_code:namespaces"
func (rl *RateLimiter) parseRateLimitHeader(header string, now time.Time) {
	for _, limit := range strings.Split(header, ",") {
		limit = strings.TrimSpace(limit)
		parts := strings.Split(limit, ":")

		if len(parts) < 2 {
			continue
		}

		// Parse retry_after (first part)
		retryAfterSeconds, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			rl.logger.Warn("Failed to parse retry_after from rate limit header", zap.String("value", parts[0]))
			retryAfterSeconds = 60 // Default fallback
		}

		retryAfter := now.Add(time.Duration(retryAfterSeconds) * time.Second)

		// Parse categories (second part)
		categoriesStr := strings.TrimSpace(parts[1])
		if categoriesStr == "" {
			categoriesStr = "all"
		}

		categories := strings.Split(categoriesStr, ";")
		for _, category := range categories {
			category = strings.TrimSpace(category)
			if category == "" {
				category = "all"
			}

			// Convert event type to data category
			category = rl.normalizeCategory(category)

			rl.rateLimits[category] = retryAfter
			rl.logger.Warn("Rate limit applied",
				zap.String("category", category),
				zap.Time("disabled_until", retryAfter),
				zap.Int("retry_after_seconds", retryAfterSeconds))
		}
	}
}

// parseRetryAfterHeader parses the Retry-After header
func (rl *RateLimiter) parseRetryAfterHeader(header string, now time.Time) {
	header = strings.TrimSpace(header)

	// Try to parse as seconds
	if seconds, err := strconv.Atoi(header); err == nil {
		retryAfter := now.Add(time.Duration(seconds) * time.Second)
		rl.rateLimits["all"] = retryAfter
		rl.logger.Warn("Global rate limit applied via Retry-After header",
			zap.Time("disabled_until", retryAfter),
			zap.Int("retry_after_seconds", seconds))
		return
	}

	// Try to parse as HTTP date
	if retryTime, err := time.Parse(time.RFC1123, header); err == nil && retryTime.After(now) {
		rl.rateLimits["all"] = retryTime
		rl.logger.Warn("Global rate limit applied via Retry-After header",
			zap.Time("disabled_until", retryTime))
		return
	}

	// Default fallback
	retryAfter := now.Add(60 * time.Second)
	rl.rateLimits["all"] = retryAfter
	rl.logger.Warn("Failed to parse Retry-After header, using default",
		zap.String("header", header),
		zap.Time("disabled_until", retryAfter))
}

// normalizeCategory converts event types to Sentry data categories
func (rl *RateLimiter) normalizeCategory(category string) string {
	switch category {
	case "event":
		return "error"
	case "log":
		return "log_item"
	case "transaction":
		return "transaction"
	case "session":
		return "session"
	case "attachment":
		return "attachment"
	case "profile":
		return "profile"
	case "replay":
		return "replay"
	default:
		return category
	}
}

// CleanupExpired removes expired rate limits
func (rl *RateLimiter) CleanupExpired() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for category, disabledUntil := range rl.rateLimits {
		if !disabledUntil.After(now) {
			delete(rl.rateLimits, category)
		}
	}
}

// GetStatus returns current rate limit status
func (rl *RateLimiter) GetStatus() map[string]time.Time {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	status := make(map[string]time.Time)
	for category, disabledUntil := range rl.rateLimits {
		status[category] = disabledUntil
	}

	return status
}
