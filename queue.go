package sentry_transport

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// EventQueue manages asynchronous event processing
type EventQueue struct {
	events      chan *QueuedEvent
	retryEvents chan *QueuedEvent
	config      *QueueConfig
	logger      *zap.Logger
	workers     []context.CancelFunc
	wg          sync.WaitGroup
	metrics     *TransportMetrics
	mu          sync.RWMutex
	closed      bool
}

// NewEventQueue creates a new event queue
func NewEventQueue(config *QueueConfig, logger *zap.Logger) *EventQueue {
	return &EventQueue{
		events:      make(chan *QueuedEvent, config.BufferSize),
		retryEvents: make(chan *QueuedEvent, config.BufferSize/2),
		config:      config,
		logger:      logger,
		metrics:     &TransportMetrics{},
	}
}

// Start starts the queue workers
func (eq *EventQueue) Start(ctx context.Context, processor EventProcessor) error {
	eq.mu.Lock()
	defer eq.mu.Unlock()

	if eq.closed {
		return nil
	}

	// Start worker goroutines
	for i := 0; i < eq.config.Workers; i++ {
		workerCtx, cancel := context.WithCancel(ctx)
		eq.workers = append(eq.workers, cancel)

		eq.wg.Add(1)
		go eq.worker(workerCtx, i, processor)
	}

	// Start retry scheduler
	retryCtx, retryCancel := context.WithCancel(ctx)
	eq.workers = append(eq.workers, retryCancel)
	eq.wg.Add(1)
	go eq.retryScheduler(retryCtx)

	return nil
}

// Stop stops the queue and all workers
func (eq *EventQueue) Stop(ctx context.Context) error {
	eq.mu.Lock()
	if eq.closed {
		eq.mu.Unlock()
		return nil
	}
	eq.closed = true
	eq.mu.Unlock()

	// Cancel all workers
	for _, cancel := range eq.workers {
		cancel()
	}

	// Close channels
	close(eq.events)
	close(eq.retryEvents)

	// Wait for workers to finish with timeout
	done := make(chan struct{})
	go func() {
		eq.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		eq.logger.Debug("Event queue stopped gracefully")
	case <-ctx.Done():
		eq.logger.Warn("Event queue stopped with timeout")
	}

	return nil
}

// Enqueue adds an event to the processing queue
func (eq *EventQueue) Enqueue(event *SentryEvent) error {
	eq.mu.RLock()
	defer eq.mu.RUnlock()

	if eq.closed {
		return ErrQueueClosed
	}

	queuedEvent := &QueuedEvent{
		Event:     event,
		Attempts:  0,
		NextRetry: time.Now(),
	}

	select {
	case eq.events <- queuedEvent:
		return nil
	default:
		eq.logger.Warn("Event queue is full, dropping event",
			zap.String("event_id", event.ID))
		return ErrQueueFull
	}
}

// EnqueueRetry adds an event to the retry queue
func (eq *EventQueue) EnqueueRetry(event *QueuedEvent) error {
	eq.mu.RLock()
	defer eq.mu.RUnlock()

	if eq.closed {
		return ErrQueueClosed
	}

	select {
	case eq.retryEvents <- event:
		return nil
	default:
		eq.logger.Warn("Retry queue is full, dropping event",
			zap.String("event_id", event.Event.ID))
		return ErrQueueFull
	}
}

// worker processes events from the queue
func (eq *EventQueue) worker(ctx context.Context, workerID int, processor EventProcessor) {
	defer eq.wg.Done()

	logger := eq.logger.With(zap.Int("worker_id", workerID))

	batch := make([]*QueuedEvent, 0, eq.config.BatchSize)
	batchTimer := time.NewTimer(eq.config.BatchTimeout)
	defer batchTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			// Process remaining batch
			if len(batch) > 0 {
				eq.processBatch(logger, processor, batch)
			}
			return

		case event, ok := <-eq.events:
			if !ok {
				// Process remaining batch
				if len(batch) > 0 {
					eq.processBatch(logger, processor, batch)
				}
				return
			}

			batch = append(batch, event)

			// Process batch if it's full
			if len(batch) >= eq.config.BatchSize {
				eq.processBatch(logger, processor, batch)
				batch = batch[:0] // Reset batch
				batchTimer.Reset(eq.config.BatchTimeout)
			}

		case <-batchTimer.C:
			// Process batch on timeout
			if len(batch) > 0 {
				eq.processBatch(logger, processor, batch)
				batch = batch[:0] // Reset batch
			}
			batchTimer.Reset(eq.config.BatchTimeout)
		}
	}
}

// retryScheduler handles retry event scheduling
func (eq *EventQueue) retryScheduler(ctx context.Context) {
	defer eq.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	pendingRetries := make([]*QueuedEvent, 0)

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-eq.retryEvents:
			if !ok {
				return
			}
			pendingRetries = append(pendingRetries, event)

		case <-ticker.C:
			// Check pending retries and move ready ones back to main queue
			now := time.Now()
			ready := 0

			for i, event := range pendingRetries {
				if now.After(event.NextRetry) {
					// Move to main queue
					select {
					case eq.events <- event:
						ready++
					default:
						eq.logger.Warn("Main queue full, keeping event in retry queue",
							zap.String("event_id", event.Event.ID))
						// Keep in pending retries
						pendingRetries[i-ready] = event
					}
				} else {
					// Not ready yet, keep in pending
					pendingRetries[i-ready] = event
				}
			}

			// Remove processed retries
			pendingRetries = pendingRetries[:len(pendingRetries)-ready]

			if ready > 0 {
				eq.logger.Debug("Moved events from retry queue to main queue",
					zap.Int("count", ready))
			}
		}
	}
}

// processBatch processes a batch of events
func (eq *EventQueue) processBatch(logger *zap.Logger, processor EventProcessor, batch []*QueuedEvent) {
	if len(batch) == 0 {
		return
	}

	logger.Debug("Processing event batch", zap.Int("size", len(batch)))

	for _, event := range batch {
		result := processor.ProcessEvent(event)
		eq.updateMetrics(result)

		if !result.Success {
			logger.Error("Failed to process event",
				zap.String("event_id", event.Event.ID),
				zap.String("error", result.Error),
				zap.Bool("rate_limit", result.RateLimit))
		}
	}
}

// updateMetrics updates queue metrics
func (eq *EventQueue) updateMetrics(result *SendResult) {
	eq.mu.Lock()
	defer eq.mu.Unlock()

	if result.Success {
		eq.metrics.EventsSent++
	} else if result.RateLimit {
		eq.metrics.EventsRateLimit++
	} else {
		eq.metrics.EventsFailed++
	}

	eq.metrics.QueueLength = len(eq.events)
}

// GetMetrics returns current queue metrics
func (eq *EventQueue) GetMetrics() *TransportMetrics {
	eq.mu.RLock()
	defer eq.mu.RUnlock()

	// Return a copy
	return &TransportMetrics{
		EventsSent:      eq.metrics.EventsSent,
		EventsFailed:    eq.metrics.EventsFailed,
		EventsRateLimit: eq.metrics.EventsRateLimit,
		QueueLength:     len(eq.events),
		TotalRetries:    eq.metrics.TotalRetries,
	}
}

// GetStatus returns current queue status
func (eq *EventQueue) GetStatus() map[string]interface{} {
	eq.mu.RLock()
	defer eq.mu.RUnlock()

	return map[string]interface{}{
		"queue_length":       len(eq.events),
		"retry_queue_length": len(eq.retryEvents),
		"workers":            len(eq.workers),
		"closed":             eq.closed,
		"metrics":            eq.metrics,
	}
}

// EventProcessor interface for processing events
type EventProcessor interface {
	ProcessEvent(event *QueuedEvent) *SendResult
}

// Custom errors
var (
	ErrQueueClosed = &PluginError{Op: "queue_enqueue", Code: "queue_closed", Message: "queue is closed"}
	ErrQueueFull   = &PluginError{Op: "queue_enqueue", Code: "queue_full", Message: "queue is full"}
)

// PluginError represents a plugin-specific error
type PluginError struct {
	Op      string
	Code    string
	Message string
}

func (e *PluginError) Error() string {
	return e.Message
}
