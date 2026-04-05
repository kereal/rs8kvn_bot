package subproxy

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"time"
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
	defer resp.Body.Close()

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
