package sentry_transport

import (
	"go.uber.org/zap"
)

// RPC provides RPC methods for PHP communication
type RPC struct {
	plugin *Plugin
	logger *zap.Logger
}

// NewRPC creates a new RPC instance
func NewRPC(plugin *Plugin, logger *zap.Logger) *RPC {
	return &RPC{
		plugin: plugin,
		logger: logger,
	}
}

// SendBatch sends a batch of Sentry events
func (r *RPC) SendBatch(events []*SentryEvent, result *[]*SendResult) error {
	const op = "sentry_transport_rpc_send_batch"

	if len(events) == 0 {
		*result = []*SendResult{}
		return nil
	}

	r.logger.Debug("Received batch of events via RPC",
		zap.Int("count", len(events)))

	results := make([]*SendResult, len(events))

	// Enqueue each event
	for i, event := range events {
		// Enqueue for processing
		if err := r.plugin.queue.Enqueue(event); err != nil {
			results[i] = &SendResult{
				Success: false,
				EventID: event.ID,
				Error:   err.Error(),
			}
			r.logger.Error("Failed to enqueue event",
				zap.String("event_id", event.ID),
				zap.Error(err))
			continue
		}

		// Success - event is queued for processing
		results[i] = &SendResult{
			Success: true,
			EventID: event.ID,
		}

		r.logger.Debug("Event queued for processing",
			zap.String("event_id", event.ID),
			zap.String("type", event.Type))
	}

	*result = results
	return nil
}

// SendEvent sends a single Sentry event
func (r *RPC) SendEvent(event *SentryEvent, result *SendResult) error {
	const op = "sentry_transport_rpc_send_event"

	r.logger.Debug("Received single event via RPC",
		zap.String("event_id", event.ID),
		zap.String("type", event.Type))

	// Enqueue for processing
	if err := r.plugin.queue.Enqueue(event); err != nil {
		*result = SendResult{
			Success: false,
			EventID: event.ID,
			Error:   err.Error(),
		}
		r.logger.Error("Failed to enqueue event",
			zap.String("event_id", event.ID),
			zap.Error(err))
		return nil
	}

	// Success - event is queued for processing
	*result = SendResult{
		Success: true,
		EventID: event.ID,
	}

	return nil
}