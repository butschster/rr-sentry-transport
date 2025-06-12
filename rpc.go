package sentry_transport

import (
	"strings"

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

	r.logger.Info("Received batch of events via RPC",
		zap.Int("count", len(events)))

	results := make([]*SendResult, len(events))

	// Check if plugin is enabled and configured
	if !r.plugin.IsEnabled() {
		for i, event := range events {
			results[i] = &SendResult{
				Success: false,
				EventID: event.ID,
				Error:   "plugin is disabled or not configured",
			}
		}
		*result = results
		return nil
	}

	// Enqueue each event
	for i, event := range events {
		// Validate event
		if err := r.validateEvent(event); err != nil {
			results[i] = &SendResult{
				Success: false,
				EventID: event.ID,
				Error:   err.Error(),
			}
			continue
		}

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

	r.logger.Info("Received single event via RPC",
		zap.String("event_id", event.ID),
		zap.String("type", event.Type))

	// Check if plugin is enabled and configured
	if !r.plugin.IsEnabled() {
		*result = SendResult{
			Success: false,
			EventID: event.ID,
			Error:   "plugin is disabled or not configured",
		}
		return nil
	}

	// Validate event
	if err := r.validateEvent(event); err != nil {
		*result = SendResult{
			Success: false,
			EventID: event.ID,
			Error:   err.Error(),
		}
		return nil
	}

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

	r.logger.Debug("Event queued for processing",
		zap.String("event_id", event.ID),
		zap.String("type", event.Type))

	return nil
}

// GetStatus returns the current plugin status
func (r *RPC) GetStatus(input bool, result *map[string]interface{}) error {
	status := map[string]interface{}{
		"enabled":    r.plugin.IsEnabled(),
		"dsn_set":    r.plugin.config != nil && r.plugin.config.DSN != "",
		"queue":      r.plugin.queue.GetStatus(),
		"metrics":    r.plugin.queue.GetMetrics(),
	}

	if r.plugin.transport != nil {
		status["rate_limits"] = r.plugin.transport.GetRateLimiter().GetStatus()
	}

	*result = status
	return nil
}

// GetMetrics returns detailed metrics
func (r *RPC) GetMetrics(input bool, result *TransportMetrics) error {
	if r.plugin.queue != nil {
		*result = *r.plugin.queue.GetMetrics()
	} else {
		*result = TransportMetrics{}
	}
	return nil
}

// validateEvent validates a Sentry event
func (r *RPC) validateEvent(event *SentryEvent) error {
	if event == nil {
		return &PluginError{
			Op:      "validate_event",
			Code:    "nil_event",
			Message: "event is nil",
		}
	}

	if event.ID == "" {
		return &PluginError{
			Op:      "validate_event",
			Code:    "missing_event_id",
			Message: "event ID is required",
		}
	}

	if event.Type == "" {
		return &PluginError{
			Op:      "validate_event",
			Code:    "missing_event_type",
			Message: "event type is required",
		}
	}

	if event.Payload == "" {
		return &PluginError{
			Op:      "validate_event",
			Code:    "missing_payload",
			Message: "event payload is required",
		}
	}

	// Validate JSON payload
	if !isValidJSON(event.Payload) {
		return &PluginError{
			Op:      "validate_event",
			Code:    "invalid_json",
			Message: "event payload is not valid JSON",
		}
	}

	return nil
}

// isValidJSON checks if a string is valid JSON
func isValidJSON(str string) bool {
	// Simple JSON validation - check if it starts with { or [
	if len(str) == 0 {
		return false
	}
	
	trimmed := strings.TrimSpace(str)
	return (trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}') ||
		   (trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']')
}
