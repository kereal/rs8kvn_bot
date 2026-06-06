package subserver

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// proxyHTTPClient is a shared HTTP client for fetching subscriptions from 3x-ui.
// It limits idle connections (2 per host) and disables transparent compression
// passthrough to preserve the original response body.
var proxyHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  false,
	},
}

// XUIResponse holds the body and headers returned by a 3x-ui subscription endpoint.
type XUIResponse struct {
	Body    []byte
	Headers map[string]string
}

// FetchFromXUI sends an HTTP GET to url with a v2rayN User-Agent and returns
// the response body (up to 10 MB) together with all response headers stored
// under lowercased keys. Header values are taken from the first value for each key.
func FetchFromXUI(url string) (*XUIResponse, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "v2rayN/6.31")

	resp, err := proxyHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Debug("Failed to close response body", zap.Error(closeErr))
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}

	headers := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}

	return &XUIResponse{
		Body:    body,
		Headers: headers,
	}, nil
}

// Format represents the detected encoding format of a subscription response body.
type Format int

const (
	// FormatUnknown means the body is empty or unparseable.
	FormatUnknown Format = iota
	// FormatJSON means the body is valid JSON (object or array).
	FormatJSON
	// FormatBase64 means the body is valid base64-encoded share links.
	FormatBase64
	// FormatPlain means the body contains plain-text share links.
	FormatPlain
)

// String returns the human-readable name of the format.
func (f Format) String() string {
	switch f {
	case FormatJSON:
		return "json"
	case FormatBase64:
		return "base64"
	case FormatPlain:
		return "plain"
	default:
		return "unknown"
	}
}

// DetectFormat examines body and returns its format: JSON, Base64, Plain, or Unknown.
func DetectFormat(body []byte) Format {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return FormatUnknown
	}

	if json.Valid([]byte(trimmed)) {
		return FormatJSON
	}

	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err == nil && len(decoded) > 0 && isValidSubscription(string(decoded)) {
		return FormatBase64
	}

	decoded, err = base64.RawStdEncoding.DecodeString(trimmed)
	if err == nil && len(decoded) > 0 && isValidSubscription(string(decoded)) {
		return FormatBase64
	}

	if isValidSubscription(trimmed) {
		return FormatPlain
	}

	return FormatUnknown
}

// isValidSubscription returns true if at least one line in data is a recognised share link.
func isValidSubscription(data string) bool {
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isValidServer(line) {
			return true
		}
	}
	return false
}

// base64StdEncode is a short-hand for standard base64 encoding.
func base64StdEncode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
