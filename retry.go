package sentry_transport

import (
	"math"
	"math/rand"
	"time"

	"go.uber.org/zap"
)

// RetryManager handles retry logic for failed events
type RetryManager struct {
	config     *RetryConfig
	logger     *zap.Logger
	metrics    *metricsCollector
	deadLetter chan *QueuedEvent
}

// NewRetryManager creates a new retry manager
func NewRetryManager(config *RetryConfig, logger *zap.Logger, metrics *metricsCollector) *RetryManager {
	rm := &RetryManager{
		config:  config,
		logger:  logger,
		metrics: metrics,
	}

	if config.DeadLetterQueue {
		rm.deadLetter = make(chan *QueuedEvent, 100) // Buffer for dead letter queue
	}

	return rm
}

// ShouldRetry determines if an event should be retried
func (rm *RetryManager) ShouldRetry(event *QueuedEvent, err error) bool {
	if event.Attempts >= rm.config.MaxAttempts {
		rm.logger.Error("Event exceeded max retry attempts",
			zap.String("event_id", event.Event.ID),
			zap.Int("attempts", event.Attempts),
			zap.Int("max_attempts", rm.config.MaxAttempts),
			zap.Error(err))

		// Send to dead letter queue if enabled
		if rm.config.DeadLetterQueue && rm.deadLetter != nil {
			select {
			case rm.deadLetter <- event:
				rm.logger.Debug("Event moved to dead letter queue",
					zap.String("event_id", event.Event.ID))
				
				// Record dead letter queue metric
				rm.metrics.IncDeadLetterEvents()
				
			default:
				rm.logger.Warn("Dead letter queue is full, dropping event",
					zap.String("event_id", event.Event.ID))
				
				// Record dropped event metric
				rm.metrics.IncDroppedEvents()
			}
		} else {
			// No dead letter queue, count as dropped
			rm.metrics.IncDroppedEvents()
		}

		return false
	}

	return true
}

// CalculateBackoff calculates the backoff duration for the next retry
func (rm *RetryManager) CalculateBackoff(attempts int) time.Duration {
	if attempts <= 0 {
		return rm.config.InitialBackoff
	}

	// Exponential backoff with jitter
	backoff := float64(rm.config.InitialBackoff) * math.Pow(rm.config.BackoffMultiplier, float64(attempts-1))
	
	// Add jitter (±25% random variation)
	jitter := backoff * 0.25 * (2*rand.Float64() - 1)
	backoff += jitter

	duration := time.Duration(backoff)

	// Cap at maximum backoff
	if duration > rm.config.MaxBackoff {
		duration = rm.config.MaxBackoff
	}

	return duration
}

// ScheduleRetry prepares an event for retry
func (rm *RetryManager) ScheduleRetry(event *QueuedEvent, err error) {
	event.Attempts++
	event.LastAttempt = time.Now()
	
	backoff := rm.CalculateBackoff(event.Attempts)
	event.NextRetry = event.LastAttempt.Add(backoff)

	rm.logger.Debug("Scheduling event retry",
		zap.String("event_id", event.Event.ID),
		zap.Int("attempt", event.Attempts),
		zap.Duration("backoff", backoff),
		zap.Time("next_retry", event.NextRetry),
		zap.Error(err))
}

// IsRetryTime checks if an event is ready for retry
func (rm *RetryManager) IsRetryTime(event *QueuedEvent) bool {
	return time.Now().After(event.NextRetry)
}

// GetDeadLetterQueue returns the dead letter queue channel
func (rm *RetryManager) GetDeadLetterQueue() <-chan *QueuedEvent {
	return rm.deadLetter
}

// GetRetryStats returns retry statistics
func (rm *RetryManager) GetRetryStats() map[string]interface{} {
	stats := map[string]interface{}{
		"max_attempts":        rm.config.MaxAttempts,
		"initial_backoff":     rm.config.InitialBackoff.String(),
		"backoff_multiplier":  rm.config.BackoffMultiplier,
		"max_backoff":         rm.config.MaxBackoff.String(),
		"dead_letter_enabled": rm.config.DeadLetterQueue,
	}

	if rm.deadLetter != nil {
		stats["dead_letter_queue_length"] = len(rm.deadLetter)
	}

	return stats
}

// Close closes the retry manager and its resources
func (rm *RetryManager) Close() {
	if rm.deadLetter != nil {
		close(rm.deadLetter)
	}
}
