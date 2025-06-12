package sentry_transport

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

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
}

// DSN represents a parsed Sentry DSN
type DSN struct {
	Scheme    string
	PublicKey string
	SecretKey string
	Host      string
	Port      int
	Path      string
	ProjectID string
	URL       string
}

// NewHTTPTransport creates a new HTTP transport
func NewHTTPTransport(config *TransportConfig, dsnStr string, logger *zap.Logger) (*HTTPTransport, error) {
	dsn, err := ParseDSN(dsnStr)
	if err != nil {
		return nil, fmt.Errorf("invalid DSN: %w", err)
	}

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
		retryMgr:    NewRetryManager(&RetryConfig{}, logger), // Will be set properly later
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
		t.logger.Info("Event sent successfully",
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
	envelope := t.createEnvelope(event)

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

	// Create request
	req, err := http.NewRequest("POST", t.dsn.URL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("User-Agent", "sentry-transport-rr/1.0.0")
	req.Header.Set("X-Sentry-Auth", t.createAuthHeader())

	if contentEncoding != "" {
		req.Header.Set("Content-Encoding", contentEncoding)
	}

	return req, nil
}

// createEnvelope creates a Sentry envelope format
func (t *HTTPTransport) createEnvelope(event *SentryEvent) string {
	// Envelope header
	header := fmt.Sprintf(`{"event_id":"%s","dsn":"%s"}`, event.ID, t.dsn.URL)

	// Item header
	itemHeader := fmt.Sprintf(`{"type":"%s","length":%d}`, event.Type, len(event.Payload))

	// Combine into envelope format
	return fmt.Sprintf("%s\n%s\n%s\n", header, itemHeader, event.Payload)
}

// createAuthHeader creates the X-Sentry-Auth header
func (t *HTTPTransport) createAuthHeader() string {
	timestamp := time.Now().Unix()
	
	auth := fmt.Sprintf("Sentry sentry_version=7,sentry_client=sentry-transport-rr/1.0.0,sentry_timestamp=%d,sentry_key=%s",
		timestamp, t.dsn.PublicKey)
	
	if t.dsn.SecretKey != "" {
		auth += fmt.Sprintf(",sentry_secret=%s", t.dsn.SecretKey)
	}
	
	return auth
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

// ParseDSN parses a Sentry DSN string
func ParseDSN(dsnStr string) (*DSN, error) {
	if dsnStr == "" {
		return nil, fmt.Errorf("DSN is empty")
	}

	u, err := url.Parse(dsnStr)
	if err != nil {
		return nil, fmt.Errorf("invalid DSN format: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid DSN scheme: %s", u.Scheme)
	}

	if u.User == nil {
		return nil, fmt.Errorf("DSN missing credentials")
	}

	publicKey := u.User.Username()
	secretKey, _ := u.User.Password()

	if publicKey == "" {
		return nil, fmt.Errorf("DSN missing public key")
	}

	// Extract project ID from path
	path := strings.TrimPrefix(u.Path, "/")
	projectID := path

	// Build envelope endpoint URL
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	// Create the envelope endpoint URL
	envelopeURL := fmt.Sprintf("%s://%s:%s/api/%s/envelope/", u.Scheme, u.Hostname(), port, projectID)

	return &DSN{
		Scheme:    u.Scheme,
		PublicKey: publicKey,
		SecretKey: secretKey,
		Host:      u.Hostname(),
		Path:      u.Path,
		ProjectID: projectID,
		URL:       envelopeURL,
	}, nil
}
