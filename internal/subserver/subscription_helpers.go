package subserver

import (
	"net/http"
	"strconv"
	"strings"
)

// FilterHeaders extracts request headers into a lowercased map, excluding
// X-Forwarded-Proto, X-Forwarded-For, and X-Real-Ip. Values are also lowercased.
func FilterHeaders(h http.Header) map[string]string {
	result := make(map[string]string)
	excluded := map[string]bool{
		"x-forwarded-proto": true,
		"x-forwarded-for":   true,
		"x-real-ip":         true,
	}

	for key, values := range h {
		lowerKey := strings.ToLower(key)
		if excluded[lowerKey] {
			continue
		}
		if len(values) > 0 {
			result[lowerKey] = strings.ToLower(values[0])
		}
	}
	return result
}

// ParseUserInfoValue extracts a numeric value (upload/download/total) from a
// subscription-userinfo header string (format: "key=N; key2=N2").
func ParseUserInfoValue(headers map[string]string, key string) int64 {
	if headers == nil {
		return 0
	}
	userInfo, ok := headers["subscription-userinfo"]
	if !ok {
		return 0
	}
	prefix := key + "="
	parts := strings.Split(userInfo, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, prefix) {
			val := strings.TrimPrefix(part, prefix)
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

// ParseExpireFromUserInfo extracts the "expire=" value from a subscription-userinfo header string.
func ParseExpireFromUserInfo(userInfo string) string {
	parts := strings.Split(userInfo, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "expire=") {
			return strings.TrimPrefix(part, "expire=")
		}
	}
	return ""
}

// BuildUserInfoHeader constructs a subscription-userinfo header value from
// aggregated upload/download/total bytes and an optional expire timestamp.
func BuildUserInfoHeader(upload, download, total int64, expire string) string {
	parts := []string{
		"upload=" + strconv.FormatInt(upload, 10),
		"download=" + strconv.FormatInt(download, 10),
		"total=" + strconv.FormatInt(total, 10),
	}
	if expire != "" {
		parts = append(parts, "expire="+expire)
	}
	return strings.Join(parts, "; ")
}

// SkipTransportHeader returns true for headers that should NOT be forwarded
// from the upstream (3x-ui) response to the subscription client.
func SkipTransportHeader(key string) bool {
	switch strings.ToLower(key) {
	case "content-length", "content-type", "content-encoding",
		"transfer-encoding", "connection", "date", "server",
		"alt-svc", "trailer", "subscription-userinfo":
		return true
	default:
		return false
	}
}

// ApplySourceHeaders copies non-transport headers from the first source's
// response into the target http.Header. Our Content-Type and Subscription-UserInfo
// are set separately afterwards to overwrite any upstream values.
func ApplySourceHeaders(target http.Header, source map[string]string) {
	if source == nil {
		return
	}
	for k, v := range source {
		if !SkipTransportHeader(k) {
			target.Set(k, v)
		}
	}
}

// ResponseHeaders builds the full set of response headers to cache alongside the body.
// It collects forwarded source headers (profile-title, routing-*, etc.) via
// applySourceHeaders and adds the Content-Type and Subscription-UserInfo headers
// that must be present on every cached response.
func ResponseHeaders(sourceHeaders map[string]string, contentType, userInfo string) map[string]string {
	h := http.Header{}
	ApplySourceHeaders(h, sourceHeaders)
	out := make(map[string]string, len(h)+2)
	for k, v := range h {
		out[k] = v[0]
	}
	out["content-type"] = contentType
	out["subscription-userinfo"] = userInfo
	return out
}
