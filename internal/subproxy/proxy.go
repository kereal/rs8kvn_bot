package subproxy

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"time"

	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

var proxyHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        2,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  false,
	},
}

type XUIResponse struct {
	Body    []byte
	Headers map[string]string
}

// FetchFromXUI sends an HTTP GET to the provided URL and returns the response body and first-value headers.
// 
// FetchFromXUI sets the `User-Agent` to "v2rayN/6.31", executes the request via the package HTTP client,
// and reads up to 10<<20 bytes from the response body. The returned XUIResponse contains the full read
// payload and a map of response headers where each key maps to the first header value for that key.
// 
// It returns an error if request creation, execution, or reading the response body fails.
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
			headers[key] = values[0]
		}
	}

	return &XUIResponse{
		Body:    body,
		Headers: headers,
	}, nil
}

type Format int

const (
	FormatBase64 Format = iota
	FormatPlain
)

func DetectFormat(body []byte) Format {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if err == nil && len(decoded) > 0 && isValidSubscription(string(decoded)) {
		return FormatBase64
	}
	return FormatPlain
}

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

func MergeSubscriptions(originalBody []byte, extraServers []string, format Format) []byte {
	if len(extraServers) == 0 {
		return originalBody
	}

	extra := strings.Join(extraServers, "\n")

	if format == FormatBase64 {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(originalBody)))
		if err != nil {
			return originalBody
		}

		original := strings.TrimSpace(string(decoded))
		if original == "" {
			return []byte(base64.StdEncoding.EncodeToString([]byte(extra)))
		}
		merged := original + "\n" + extra

		return []byte(base64.StdEncoding.EncodeToString([]byte(merged)))
	}

	original := strings.TrimSpace(string(originalBody))
	if original == "" {
		return []byte(extra)
	}
	merged := original + "\n" + extra

	return []byte(merged)
}
