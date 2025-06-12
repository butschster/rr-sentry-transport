package sentry_transport

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
    "regexp"

	"go.uber.org/zap"
)

// HTTPTransport handles HTTP communication with Sentry
type HTTPTransport struct {
	config      *TransportConfig
	dsn         *DSN
	client      *http.Client
	logger      *zap.Logger
	rateLimiter *RateLimiter
	retryMgr    *RetryManager
	metrics     *metricsCollector
}

// NewHTTPTransport creates a new HTTP transport
func NewHTTPTransport(config *TransportConfig, dsnStr string, logger *zap.Logger, metrics *metricsCollector) (*HTTPTransport, error) {
	dsn, err := ParseDSN(dsnStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSN: %w", err)
	}

	// Validate the parsed DSN
	if err := dsn.Validate(); err != nil {
		return nil, fmt.Errorf("invalid DSN: %w", err)
	}

	// Configure HTTP transport
	transport := &http.Transport{
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !config.SSLVerify,
		},
	}

	// Configure proxy if specified
	if config.Proxy != "" {
		proxyURL, err := url.Parse(config.Proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   config.Timeout,
	}

	return &HTTPTransport{
		config:      config,
		dsn:         dsn,
		client:      client,
		logger:      logger,
		rateLimiter: NewRateLimiter(logger),
		metrics:     metrics,
	}, nil
}

// ProcessEvent implements EventProcessor interface
func (t *HTTPTransport) ProcessEvent(event *QueuedEvent) *SendResult {
	// Check rate limiting
	if t.rateLimiter.IsRateLimited(event.Event.Type) {
		disabledUntil := t.rateLimiter.GetDisabledUntil(event.Event.Type)
		t.logger.Warn("Event rate limited",
			zap.String("event_id", event.Event.ID),
			zap.String("type", event.Event.Type),
			zap.Time("disabled_until", disabledUntil))

		return &SendResult{
			Success:   false,
			EventID:   event.Event.ID,
			RateLimit: true,
			Error:     fmt.Sprintf("rate limited until %s", disabledUntil.Format(time.RFC3339)),
		}
	}

	// Send the event
	return t.sendEvent(event.Event)
}

// sendEvent sends a single event to Sentry
func (t *HTTPTransport) sendEvent(event *SentryEvent) *SendResult {
	// Create request
	req, err := t.createRequest(event)

	if err != nil {
		t.logger.Error("Failed to create request",
			zap.String("event_id", event.ID),
			zap.Error(err))
		
		return &SendResult{
			Success: false,
			EventID: event.ID,
			Error:   err.Error(),
		}
	}

	// Send request
	resp, err := t.client.Do(req)
	if err != nil {
		t.logger.Error("HTTP request failed",
			zap.String("event_id", event.ID),
			zap.Error(err))
		
		return &SendResult{
			Success: false,
			EventID: event.ID,
			Error:   err.Error(),
		}
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.logger.Warn("Failed to read response body",
			zap.String("event_id", event.ID),
			zap.Error(err))
	}

	// Handle rate limiting
	if resp.StatusCode == 429 {
		t.rateLimiter.HandleRateLimitHeaders(resp.Header)
		return &SendResult{
			Success:   false,
			EventID:   event.ID,
			RateLimit: true,
			Error:     "rate limited by server",
		}
	}

	// Check for other rate limit headers
	t.rateLimiter.HandleRateLimitHeaders(resp.Header)

	// Handle response
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		t.logger.Debug("Event sent successfully",
			zap.String("event_id", event.ID),
			zap.Int("status_code", resp.StatusCode))
		
		return &SendResult{
			Success: true,
			EventID: event.ID,
		}
	}

	// Handle errors
	errorMsg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
	t.logger.Error("Event send failed",
		zap.String("event_id", event.ID),
		zap.Int("status_code", resp.StatusCode),
		zap.String("response", string(body)))

	return &SendResult{
		Success: false,
		EventID: event.ID,
		Error:   errorMsg,
	}
}

// createRequest creates an HTTP request for the event
func (t *HTTPTransport) createRequest(event *SentryEvent) (*http.Request, error) {
	// Create envelope
    // Replace empty DSN with actual DSN
	envelope := t.processPayload(event.Payload)

	// Prepare request body
	var body io.Reader
	var contentEncoding string

	if t.config.Compression {
		// Compress with gzip
		var buf bytes.Buffer
		gzipWriter := gzip.NewWriter(&buf)
		if _, err := gzipWriter.Write([]byte(envelope)); err != nil {
			return nil, fmt.Errorf("failed to compress payload: %w", err)
		}
		if err := gzipWriter.Close(); err != nil {
			return nil, fmt.Errorf("failed to close gzip writer: %w", err)
		}
		body = &buf
		contentEncoding = "gzip"
	} else {
		body = strings.NewReader(envelope)
	}

	// Create request to the envelope endpoint
	req, err := http.NewRequest("POST", t.dsn.EnvelopeURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers according to Sentry specification
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("User-Agent", "roadrunner/1.0.0")
	req.Header.Set("X-Sentry-Auth", fmt.Sprintf("Sentry sentry_version=7,sentry_client=roadrunner/1.0.0,sentry_key=%s", t.dsn.PublicKey))

	if contentEncoding != "" {
		req.Header.Set("Content-Encoding", contentEncoding)
	}

	return req, nil
}

func (t *HTTPTransport) processPayload(payload string) string {
    // Regex to match "dsn": followed by any quoted string value
    re := regexp.MustCompile(`"dsn"\s*:\s*"[^"]*"`)
    return re.ReplaceAllString(payload, fmt.Sprintf(`"dsn":"%s"`, t.dsn.String))
}

// SetRetryManager sets the retry manager
func (t *HTTPTransport) SetRetryManager(retryMgr *RetryManager) {
	t.retryMgr = retryMgr
}

// GetRateLimiter returns the rate limiter
func (t *HTTPTransport) GetRateLimiter() *RateLimiter {
	return t.rateLimiter
}

// Close closes the transport
func (t *HTTPTransport) Close() error {
	if t.client != nil {
		t.client.CloseIdleConnections()
	}
	return nil
}
