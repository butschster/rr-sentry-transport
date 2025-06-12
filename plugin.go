package sentry_transport

import (
	"context"
	"time"

	"github.com/roadrunner-server/endure/v2/dep"
	"github.com/roadrunner-server/errors"
	"go.uber.org/zap"
)

const PluginName = "sentry_transport"

// Plugin represents the main plugin structure
type Plugin struct {
	config    *Config
	logger    *zap.Logger
	queue     *EventQueue
	transport *HTTPTransport
	retryMgr  *RetryManager
	
	// Lifecycle
	stopCh chan struct{}
	doneCh chan struct{}
}

// Configurer interface for config plugin
type Configurer interface {
	UnmarshalKey(name string, out interface{}) error
	Has(name string) bool
}

// Logger interface for logger plugin
type Logger interface {
	NamedLogger(name string) *zap.Logger
}

// Init initializes the plugin
func (p *Plugin) Init(cfg Configurer, log Logger) error {
	const op = errors.Op("sentry_transport_init")

	// Check if configuration section exists
	if !cfg.Has(PluginName) {
		return errors.E(op, errors.Disabled)
	}

	// Unmarshal configuration
	config := &Config{}
	if err := cfg.UnmarshalKey(PluginName, config); err != nil {
		return errors.E(op, err)
	}

	// Initialize defaults and validate
	config.InitDefaults()
	if err := config.Validate(); err != nil {
		return errors.E(op, err)
	}

	// Check if plugin is enabled
	if !config.Enabled {
		return errors.E(op, errors.Disabled)
	}

	// Store configuration
	p.config = config

	// Initialize logger
	p.logger = log.NamedLogger(PluginName)

	// Initialize event queue
	p.queue = NewEventQueue(&config.Queue, p.logger)

	// Initialize HTTP transport if DSN is provided
	if config.DSN != "" {
		transport, err := NewHTTPTransport(&config.Transport, config.DSN, p.logger)
		if err != nil {
			return errors.E(op, err)
		}
		p.transport = transport

		// Initialize retry manager
		p.retryMgr = NewRetryManager(&config.Retry, p.logger)
		transport.SetRetryManager(p.retryMgr)
	} else {
		p.logger.Warn("No DSN configured, events will be queued but not transmitted")
	}

	// Initialize lifecycle channels
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})

	p.logger.Info("Sentry transport plugin initialized", 
		zap.Bool("enabled", config.Enabled),
		zap.Bool("dsn_configured", config.DSN != ""),
		zap.Int("queue_buffer_size", config.Queue.BufferSize),
		zap.Int("workers", config.Queue.Workers))

	return nil
}

// Serve starts the plugin
func (p *Plugin) Serve() chan error {
	errCh := make(chan error, 1)

	if p.config == nil {
		errCh <- errors.E("sentry_transport_serve", "plugin not initialized")
		return errCh
	}

	go func() {
		defer close(p.doneCh)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start event queue with transport as processor
		if p.transport != nil {
			if err := p.queue.Start(ctx, p.transport); err != nil {
				errCh <- errors.E("sentry_transport_serve", err)
				return
			}
		} else {
			// Start queue without transport (dry-run mode)
			if err := p.queue.Start(ctx, &NoOpProcessor{p.logger}); err != nil {
				errCh <- errors.E("sentry_transport_serve", err)
				return
			}
		}

		// Start cleanup routine
		go p.cleanupRoutine(ctx)

		p.logger.Info("Sentry transport plugin started")

		// Wait for stop signal
		select {
		case <-p.stopCh:
			p.logger.Info("Sentry transport plugin stopping")
		case <-ctx.Done():
			p.logger.Info("Sentry transport plugin context cancelled")
		}

		// Stop components
		if err := p.queue.Stop(ctx); err != nil {
			p.logger.Error("Error stopping event queue", zap.Error(err))
		}

		if p.transport != nil {
			if err := p.transport.Close(); err != nil {
				p.logger.Error("Error closing transport", zap.Error(err))
			}
		}

		if p.retryMgr != nil {
			p.retryMgr.Close()
		}

		p.logger.Info("Sentry transport plugin stopped")
	}()

	return errCh
}

// Stop stops the plugin
func (p *Plugin) Stop(ctx context.Context) error {
	if p.stopCh != nil {
		close(p.stopCh)
	}

	// Wait for graceful shutdown with timeout
	select {
	case <-p.doneCh:
		return nil
	case <-ctx.Done():
		p.logger.Warn("Plugin stop timed out")
		return ctx.Err()
	}
}

// Name returns the plugin name
func (p *Plugin) Name() string {
	return PluginName
}

// RPC returns the RPC interface
func (p *Plugin) RPC() interface{} {
	return NewRPC(p, p.logger)
}

// Provides returns the dependencies this plugin provides
func (p *Plugin) Provides() []*dep.Out {
	return []*dep.Out{
		dep.Bind((*SentryTransporter)(nil), p.Transport),
	}
}

// IsEnabled returns true if the plugin is enabled and configured
func (p *Plugin) IsEnabled() bool {
	return p.config != nil && p.config.Enabled
}

// Transport returns the transport interface
func (p *Plugin) Transport() SentryTransporter {
	return p
}

// SendEvent implements SentryTransporter interface
func (p *Plugin) SendEvent(event *SentryEvent) error {
	if p.queue == nil {
		return errors.E("sentry_transport_send", "plugin not initialized")
	}

	return p.queue.Enqueue(event)
}

// SendBatch implements SentryTransporter interface
func (p *Plugin) SendBatch(events []*SentryEvent) error {
	if p.queue == nil {
		return errors.E("sentry_transport_send_batch", "plugin not initialized")
	}

	for _, event := range events {
		if err := p.queue.Enqueue(event); err != nil {
			return err
		}
	}

	return nil
}

// GetMetrics implements SentryTransporter interface
func (p *Plugin) GetMetrics() *TransportMetrics {
	if p.queue == nil {
		return &TransportMetrics{}
	}

	return p.queue.GetMetrics()
}

// cleanupRoutine performs periodic cleanup tasks
func (p *Plugin) cleanupRoutine(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Clean up expired rate limits
			if p.transport != nil {
				p.transport.GetRateLimiter().CleanupExpired()
			}
		}
	}
}

// SentryTransporter interface for other plugins to use
type SentryTransporter interface {
	SendEvent(event *SentryEvent) error
	SendBatch(events []*SentryEvent) error
	GetMetrics() *TransportMetrics
}

// NoOpProcessor is a no-op event processor for when no transport is configured
type NoOpProcessor struct {
	logger *zap.Logger
}

// ProcessEvent implements EventProcessor interface
func (n *NoOpProcessor) ProcessEvent(event *QueuedEvent) *SendResult {
	n.logger.Info("Dry-run: would send event",
		zap.String("event_id", event.Event.ID),
		zap.String("type", event.Event.Type),
		zap.Int("payload_size", len(event.Event.Payload)))

	return &SendResult{
		Success: true,
		EventID: event.Event.ID,
	}
}
