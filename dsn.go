package sentry_transport

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// DSN represents a parsed Sentry DSN
type DSN struct {
    String    string
	Scheme    string
	PublicKey string
	Host      string
	Port      int
	Path      string
	ProjectID string
	OrgID     *int // Organization ID (optional, for SaaS)
	
	// Computed URLs
	EnvelopeURL string
	CSPURL      string
}

// Regex to match the organization ID in the host (for Sentry SaaS)
var sentryOrgIDRegex = regexp.MustCompile(`^o(\d+)\.`)

// ParseDSN parses a Sentry DSN string according to PHP implementation
func ParseDSN(dsnStr string) (*DSN, error) {
	if dsnStr == "" {
		return nil, fmt.Errorf("DSN is empty")
	}

	parsedURL, err := url.Parse(dsnStr)
	if err != nil {
		return nil, fmt.Errorf("the \"%s\" DSN is invalid: %w", dsnStr, err)
	}

	// Validate required components (matching PHP validation)
	if parsedURL.Scheme == "" {
		return nil, fmt.Errorf("the \"%s\" DSN must contain a scheme, a host, a user and a path component", dsnStr)
	}
	if parsedURL.Host == "" {
		return nil, fmt.Errorf("the \"%s\" DSN must contain a scheme, a host, a user and a path component", dsnStr)
	}
	if parsedURL.Path == "" {
		return nil, fmt.Errorf("the \"%s\" DSN must contain a scheme, a host, a user and a path component", dsnStr)
	}
	if parsedURL.User == nil || parsedURL.User.Username() == "" {
		return nil, fmt.Errorf("the \"%s\" DSN must contain a scheme, a host, a user and a path component", dsnStr)
	}

	// Validate scheme
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("the scheme of the \"%s\" DSN must be either \"http\" or \"https\"", dsnStr)
	}

	// Extract public key (user part)
	publicKey := parsedURL.User.Username()

	// Extract port with defaults
	port := 80
	if parsedURL.Scheme == "https" {
		port = 443
	}
	if parsedURL.Port() != "" {
		if portNum, err := strconv.Atoi(parsedURL.Port()); err == nil {
			port = portNum
		}
	}

	// Extract project ID and path (matching PHP logic)
	pathSegments := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	if len(pathSegments) == 0 {
		return nil, fmt.Errorf("the \"%s\" DSN path must contain a project ID", dsnStr)
	}
	
	projectID := pathSegments[len(pathSegments)-1]
	if projectID == "" {
		return nil, fmt.Errorf("the \"%s\" DSN path must contain a project ID", dsnStr)
	}

	// Calculate path without project ID
	path := "/"
	if len(pathSegments) > 1 {
		path = "/" + strings.Join(pathSegments[:len(pathSegments)-1], "/")
	}
	
	// Handle case where original path ends with /
	if strings.HasSuffix(parsedURL.Path, "/") && !strings.HasSuffix(path, "/") {
		path += "/"
	}

	// Extract organization ID if present (for Sentry SaaS)
	var orgID *int
	if matches := sentryOrgIDRegex.FindStringSubmatch(parsedURL.Hostname()); len(matches) > 1 {
		if id, err := strconv.Atoi(matches[1]); err == nil {
			orgID = &id
		}
	}

	dsn := &DSN{
	    String:    dsnStr,
		Scheme:    parsedURL.Scheme,
		PublicKey: publicKey,
		Host:      parsedURL.Hostname(),
		Port:      port,
		Path:      path,
		ProjectID: projectID,
		OrgID:     orgID,
	}

	// Generate computed URLs
	dsn.EnvelopeURL = dsn.GetEnvelopeEndpointURL()
	dsn.CSPURL = dsn.GetCSPReportEndpointURL()

	return dsn, nil
}

// GetBaseEndpointURL returns the base API endpoint URL
func (d *DSN) GetBaseEndpointURL() string {
	url := fmt.Sprintf("%s://%s", d.Scheme, d.Host)

	// Add port if non-standard
	if (d.Scheme == "http" && d.Port != 80) || (d.Scheme == "https" && d.Port != 443) {
		url += fmt.Sprintf(":%d", d.Port)
	}

	// Add path if present and not just "/"
	if d.Path != "" && d.Path != "/" {
		url += strings.TrimSuffix(d.Path, "/")
	}

	url += fmt.Sprintf("/api/%s", d.ProjectID)

	return url
}

// GetEnvelopeEndpointURL returns the envelope API endpoint URL
func (d *DSN) GetEnvelopeEndpointURL() string {
	return d.GetBaseEndpointURL() + "/envelope/"
}

// GetCSPReportEndpointURL returns the CSP report endpoint URL
func (d *DSN) GetCSPReportEndpointURL() string {
	return d.GetBaseEndpointURL() + "/security/?sentry_key=" + d.PublicKey
}

// Validate performs additional validation on the parsed DSN
func (d *DSN) Validate() error {
	if d.PublicKey == "" {
		return fmt.Errorf("DSN missing public key")
	}
	if d.ProjectID == "" {
		return fmt.Errorf("DSN missing project ID")
	}
	if d.Host == "" {
		return fmt.Errorf("DSN missing host")
	}
	if d.Scheme != "http" && d.Scheme != "https" {
		return fmt.Errorf("DSN scheme must be http or https")
	}
	return nil
}
