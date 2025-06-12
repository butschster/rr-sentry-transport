package sentry_transport

import (
	"time"
)

const PluginName = "sentry_transport"

// Config represents the plugin configuration
type Config struct {
	// Enable/disable the plugin
	Enabled bool `mapstructure:"enabled"`

	// Sentry DSN
	DSN string `mapstructure:"dsn"`

	// HTTP transport settings
	Transport TransportConfig `mapstructure:"transport"`

	// Retry configuration
	Retry RetryConfig `mapstructure:"retry"`

	// Queue configuration
	Queue QueueConfig `mapstructure:"queue"`

	// Logging configuration
	Logging LoggingConfig `mapstructure:"logging"`
}

// TransportConfig contains HTTP transport settings
type TransportConfig struct {
	// Request timeout
	Timeout time.Duration `mapstructure:"timeout"`
	// Connection timeout
	ConnectTimeout time.Duration `mapstructure:"connect_timeout"`
	// Enable gzip compression
	Compression bool `mapstructure:"compression"`
	// SSL verification
	SSLVerify bool `mapstructure:"ssl_verify"`
	// Proxy settings
	Proxy     string `mapstructure:"proxy"`
	ProxyAuth string `mapstructure:"proxy_auth"`
}

// RetryConfig contains retry mechanism settings
type RetryConfig struct {
	// Maximum retry attempts
	MaxAttempts int `mapstructure:"max_attempts"`
	// Initial backoff duration
	InitialBackoff time.Duration `mapstructure:"initial_backoff"`
	// Backoff multiplier
	BackoffMultiplier float64 `mapstructure:"backoff_multiplier"`
	// Maximum backoff duration
	MaxBackoff time.Duration `mapstructure:"max_backoff"`
	// Enable dead letter queue
	DeadLetterQueue bool `mapstructure:"dead_letter_queue"`
}

// QueueConfig contains queue settings
type QueueConfig struct {
	// Buffer size for the event queue
	BufferSize int `mapstructure:"buffer_size"`
	// Number of worker goroutines
	Workers int `mapstructure:"workers"`
	// Batch size for processing events
	BatchSize int `mapstructure:"batch_size"`
	// Batch timeout
	BatchTimeout time.Duration `mapstructure:"batch_timeout"`
}

// LoggingConfig contains logging settings
type LoggingConfig struct {
	// Log level for plugin operations
	Level string `mapstructure:"level"`
}

// InitDefaults initializes default configuration values
func (cfg *Config) InitDefaults() {
	if cfg.Transport.Timeout == 0 {
		cfg.Transport.Timeout = 30 * time.Second
	}
	if cfg.Transport.ConnectTimeout == 0 {
		cfg.Transport.ConnectTimeout = 10 * time.Second
	}
	if cfg.Transport.Compression == false {
		cfg.Transport.Compression = true
	}
	if cfg.Transport.SSLVerify == false {
		cfg.Transport.SSLVerify = true
	}

	if cfg.Retry.MaxAttempts == 0 {
		cfg.Retry.MaxAttempts = 3
	}
	if cfg.Retry.InitialBackoff == 0 {
		cfg.Retry.InitialBackoff = 1 * time.Second
	}
	if cfg.Retry.BackoffMultiplier == 0 {
		cfg.Retry.BackoffMultiplier = 2.0
	}
	if cfg.Retry.MaxBackoff == 0 {
		cfg.Retry.MaxBackoff = 300 * time.Second
	}

	if cfg.Queue.BufferSize == 0 {
		cfg.Queue.BufferSize = 1000
	}
	if cfg.Queue.Workers == 0 {
		cfg.Queue.Workers = 4
	}
	if cfg.Queue.BatchSize == 0 {
		cfg.Queue.BatchSize = 10
	}
	if cfg.Queue.BatchTimeout == 0 {
		cfg.Queue.BatchTimeout = 5 * time.Second
	}

	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
}

// Validate validates the configuration
func (cfg *Config) Validate() error {
	if cfg.DSN == "" {
		return nil // DSN can be empty to disable transmission
	}

	if cfg.Queue.BufferSize <= 0 {
		cfg.Queue.BufferSize = 1000
	}

	if cfg.Queue.Workers <= 0 {
		cfg.Queue.Workers = 1
	}

	if cfg.Retry.MaxAttempts < 0 {
		cfg.Retry.MaxAttempts = 0
	}

	return nil
}
