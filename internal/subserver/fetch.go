package subserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// fetchHTTPClient is a shared HTTP client for fetching subscription responses
// from upstream nodes (3x-ui JSON, Clash YAML, base64, plain links). It limits
// idle connections (2 per host) and keeps transparent gzip decompression
// enabled (DisableCompression is false), so resp.Body is the decompressed
// content that DetectFormat and the parsers consume directly.
var fetchHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  false,
	},
}

// NodeResponse holds the body and headers returned by an upstream node's
// subscription endpoint (3x-ui JSON, Clash YAML, base64, plain links).
type NodeResponse struct {
	Body    []byte
	Headers map[string]string
}

// FetchFromNode sends an HTTP GET to url with a custom User-Agent and returns
// the response body (up to 10 MB) together with all response headers stored
// under lowercased keys. Header values are taken from the first value for each key.
// Non-2xx responses are treated as fetch errors so upstream failures are never
// aggregated into a subscription.
func FetchFromNode(ctx context.Context, url string) (*NodeResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		logger.Error("Failed to create HTTP request for source fetch",
			zap.String("url", url),
			zap.Error(err))
		return nil, err
	}

	req.Header.Set("User-Agent", "RS8 KVN Subserver")

	resp, err := fetchHTTPClient.Do(req)
	if err != nil {
		logger.Error("Source fetch request failed",
			zap.String("url", url),
			zap.Error(err))
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		logger.Error("Source fetch returned no response body",
			zap.String("url", url))
		return nil, fmt.Errorf("source fetch returned no body: %s", url)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		logger.Error("Source fetch returned non-2xx status",
			zap.String("url", url),
			zap.Int("status", resp.StatusCode))
		return nil, fmt.Errorf("source fetch %s returned status %d", url, resp.StatusCode)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Error("Failed to close source response body",
				zap.String("url", url),
				zap.Error(closeErr))
		}
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		logger.Error("Failed to read source response body",
			zap.String("url", url),
			zap.Error(err))
		return nil, err
	}

	headers := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}

	return &NodeResponse{
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
	// FormatClash means the body is a Clash/Mihomo YAML config with a proxies section.
	FormatClash
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
	case FormatClash:
		return "clash"
	default:
		return "unknown"
	}
}

// DetectFormat examines body and returns its format: JSON, Clash, Base64, Plain, or Unknown.
func DetectFormat(body []byte) Format {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return FormatUnknown
	}

	if json.Valid([]byte(trimmed)) {
		return FormatJSON
	}

	if isClashYAML(body) {
		return FormatClash
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

// isClashYAML checks whether body is a Clash/Mihomo YAML config by looking
// for a top-level "proxies" key. YAML is a superset of JSON, so this check
// must run after json.Valid.
func isClashYAML(body []byte) bool {
	var root map[string]yaml.Node
	if err := yaml.Unmarshal(body, &root); err != nil {
		return false
	}
	_, ok := root["proxies"]
	return ok
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
