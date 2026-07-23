package joeydb

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeout           = 10 * time.Second
	defaultMaxRequestBytes   = 16 << 20
	defaultMaxResponseBytes  = 8 << 20
	defaultMaxErrorBodyBytes = 64 << 10
	absoluteBodyLimit        = 64 << 20
	defaultUserAgent         = "joeydb-go/v0"
)

// RequestIDGenerator returns one JoeyDB-safe correlation identifier. A
// generator supplied to a Client must be safe for concurrent use.
type RequestIDGenerator func() (string, error)

// Config controls local client behavior. NewClient performs no network I/O.
type Config struct {
	BaseURL string

	// HTTPClient is shallow-cloned before use. Redirects are refused and a
	// finite timeout is applied to the clone, without mutating the caller's
	// client.
	HTTPClient *http.Client
	// Transport is used only when HTTPClient is nil.
	Transport http.RoundTripper

	// Timeout defaults to 10 seconds. When HTTPClient already has a positive
	// timeout, a zero Config.Timeout preserves it.
	Timeout time.Duration

	MaxRequestBytes    int64
	MaxResponseBytes   int64
	MaxErrorBodyBytes  int64
	UserAgent          string
	RequestIDGenerator RequestIDGenerator
}

// Client is safe for concurrent use.
type Client struct {
	baseURL           string
	httpClient        *http.Client
	maxRequestBytes   int64
	maxResponseBytes  int64
	maxErrorBodyBytes int64
	userAgent         string
	requestID         RequestIDGenerator
}

// NewClient validates configuration without contacting JoeyDB.
func NewClient(config Config) (*Client, error) {
	baseURL, err := validateBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	if config.HTTPClient != nil && config.Transport != nil {
		return nil, errors.New("joeydb: HTTPClient and Transport are mutually exclusive")
	}

	timeout := config.Timeout
	if timeout < 0 {
		return nil, errors.New("joeydb: timeout must not be negative")
	}
	var httpClient *http.Client
	if config.HTTPClient != nil {
		clone := *config.HTTPClient
		httpClient = &clone
		if timeout > 0 {
			httpClient.Timeout = timeout
		} else if httpClient.Timeout <= 0 {
			httpClient.Timeout = defaultTimeout
		}
	} else {
		if timeout == 0 {
			timeout = defaultTimeout
		}
		httpClient = &http.Client{Timeout: timeout, Transport: config.Transport}
	}
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	maxRequest, err := boundedConfigValue("MaxRequestBytes", config.MaxRequestBytes, defaultMaxRequestBytes)
	if err != nil {
		return nil, err
	}
	maxResponse, err := boundedConfigValue("MaxResponseBytes", config.MaxResponseBytes, defaultMaxResponseBytes)
	if err != nil {
		return nil, err
	}
	maxError, err := boundedConfigValue("MaxErrorBodyBytes", config.MaxErrorBodyBytes, defaultMaxErrorBodyBytes)
	if err != nil {
		return nil, err
	}
	if maxError > maxResponse {
		maxError = maxResponse
	}

	userAgent := config.UserAgent
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	if strings.ContainsAny(userAgent, "\r\n") {
		return nil, errors.New("joeydb: UserAgent must not contain a newline")
	}
	generator := config.RequestIDGenerator
	if generator == nil {
		generator = randomRequestID
	}

	return &Client{
		baseURL:           baseURL,
		httpClient:        httpClient,
		maxRequestBytes:   maxRequest,
		maxResponseBytes:  maxResponse,
		maxErrorBodyBytes: maxError,
		userAgent:         userAgent,
		requestID:         generator,
	}, nil
}

func validateBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("joeydb: invalid base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("joeydb: base URL scheme must be http or https")
	}
	if parsed.Host == "" || parsed.Opaque != "" {
		return "", errors.New("joeydb: base URL host is required")
	}
	if parsed.User != nil {
		return "", errors.New("joeydb: base URL credentials are not supported")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errors.New("joeydb: base URL query strings and fragments are not supported")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func boundedConfigValue(name string, value, defaultValue int64) (int64, error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 0 || value > absoluteBodyLimit {
		return 0, fmt.Errorf("joeydb: %s must be between 1 and %d", name, absoluteBodyLimit)
	}
	return value, nil
}

func randomRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate request ID: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func validWireToken(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' || c == '-' {
			continue
		}
		return false
	}
	return true
}
