package sentry_transport

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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
	metrics   *metricsCollector
	
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

	// Store configuration
	p.config = config

	// Initialize logger
	p.logger = log.NamedLogger(PluginName)

	// Initialize metrics collector
	p.metrics = newMetricsCollector()

	// Initialize event queue with metrics
	p.queue = NewEventQueue(&config.Queue, p.logger, p.metrics)

	// Initialize HTTP transport if DSN is provided
	if config.DSN != "" {
		transport, err := NewHTTPTransport(&config.Transport, config.DSN, p.logger, p.metrics)
		if err != nil {
			return errors.E(op, err)
		}
		p.transport = transport

		// Initialize retry manager with metrics
		p.retryMgr = NewRetryManager(&config.Retry, p.logger, p.metrics)
		transport.SetRetryManager(p.retryMgr)
	} else {
		p.logger.Warn("No DSN configured, events will be queued but not transmitted")
	}

	// Initialize lifecycle channels
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})

	p.logger.Debug("Sentry transport plugin initialized")

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
			if err := p.queue.Start(ctx, &NoOpProcessor{p.logger, p.metrics}); err != nil {
				errCh <- errors.E("sentry_transport_serve", err)
				return
			}
		}

		// Start cleanup routine
		go p.cleanupRoutine(ctx)

		p.logger.Debug("Sentry transport plugin started")

		// Wait for stop signal
		select {
		case <-p.stopCh:
			p.logger.Debug("Sentry transport plugin stopping")
		case <-ctx.Done():
			p.logger.Debug("Sentry transport plugin context cancelled")
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

		p.logger.Debug("Sentry transport plugin stopped")
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

// Transport returns the transport interface
func (p *Plugin) Transport() SentryTransporter {
	return p
}

// SendEvent implements SentryTransporter interface
func (p *Plugin) SendEvent(event *SentryEvent) error {
	if p.queue == nil {
		return errors.E("sentry_transport_send", "plugin not initialized")
	}

	// Record event by type metric
	p.metrics.IncEventsByType(event.Type)

	return p.queue.Enqueue(event)
}

// SendBatch implements SentryTransporter interface
func (p *Plugin) SendBatch(events []*SentryEvent) error {
	if p.queue == nil {
		return errors.E("sentry_transport_send_batch", "plugin not initialized")
	}

	for _, event := range events {
		// Record event by type metric for each event
		p.metrics.IncEventsByType(event.Type)
		
		if err := p.queue.Enqueue(event); err != nil {
			return err
		}
	}

	return nil
}

// MetricsCollector implements StatProvider interface for prometheus integration
func (p *Plugin) MetricsCollector() []prometheus.Collector {
	return []prometheus.Collector{p.metrics}
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
}

// NoOpProcessor is a no-op event processor for when no transport is configured
type NoOpProcessor struct {
	logger  *zap.Logger
	metrics *metricsCollector
}

// ProcessEvent implements EventProcessor interface
func (n *NoOpProcessor) ProcessEvent(event *QueuedEvent) *SendResult {
	n.logger.Debug("Dry-run: would send event",
		zap.String("event_id", event.Event.ID),
		zap.String("type", event.Event.Type),
		zap.Int("payload_size", len(event.Event.Payload)))

	// Record as successful in dry-run mode
	n.metrics.IncSuccessfulEvents()

	return &SendResult{
		Success: true,
		EventID: event.Event.ID,
	}
}
