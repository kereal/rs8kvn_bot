package subserver

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FilterHeaders extracts request headers into a lowercased map, excluding following:
func FilterHeaders(h http.Header) map[string]string {
	result := make(map[string]string)
	excluded := map[string]bool{
		"x-forwarded-proto": true,
		"x-forwarded-for":   true,
		"x-real-ip":         true,
		"accept":            true,
		"authorization":     true,
		"cookie":            true,
		"accept-encoding":   true,
		"connection":        true,
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

	parts := strings.SplitSeq(userInfo, ";")
	for part := range parts {
		part = strings.TrimSpace(part)
		if after, ok0 := strings.CutPrefix(part, prefix); ok0 {
			val := after

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
	parts := strings.SplitSeq(userInfo, ";")
	for part := range parts {
		part = strings.TrimSpace(part)
		if after, ok := strings.CutPrefix(part, "expire="); ok {
			return after
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

// AppendProfileTitleSuffix appends suffix to a profile-title header value and
// re-encodes the result in the canonical form used by 3x-ui panels and sing-box
// clients: "base64:<b64>". The incoming value may arrive as
//
//   - "base64:<b64>" (3x-ui / hiddify convention): the payload is decoded,
//     suffixed, and re-encoded;
//   - raw base64 (no prefix): decoded, suffixed, re-encoded with the prefix so
//     clients that only understand the prefixed form still decode it;
//   - plain text: treated as the title itself, encoded with the prefix.
//
// An empty value or empty suffix is returned unchanged.
func AppendProfileTitleSuffix(value, suffix string) string {
	value = strings.TrimSpace(value)
	if value == "" || suffix == "" {
		return value
	}

	return "base64:" + base64.StdEncoding.EncodeToString([]byte(decodeProfileTitle(value)+suffix))
}

// decodeProfileTitle extracts the plain title from a profile-title header value
// regardless of whether it uses the "base64:" prefix, raw base64, or plain text.
// Non-decodable payloads are returned as-is.
func decodeProfileTitle(value string) string {
	payload := value

	if strings.HasPrefix(strings.ToLower(value), "base64:") {
		payload = value[len("base64:"):]
	}

	payload = strings.TrimSpace(payload)
	if payload == "" {
		return ""
	}

	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		decoded, err := enc.DecodeString(payload)
		decodedString := string(decoded)
		if err == nil && isPrintableProfileTitle(decodedString) {
			return decodedString
		}
	}

	return payload
}

func isPrintableProfileTitle(value string) bool {
	if !utf8.ValidString(value) || value == "" {
		return false
	}

	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}

	return true
}

// ApplyProfileTitleSuffix appends suffix to the profile-title entry of a header
// map in place and returns the map. It is a no-op when the header is absent or
// the suffix is empty. The key lookup is case-insensitive because upstream
// headers arrive lowercased (FetchFromNode) while ResponseHeaders canonicalizes
// keys to Title-Case via http.Header.
func ApplyProfileTitleSuffix(headers map[string]string, suffix string) map[string]string {
	if headers == nil || suffix == "" {
		return headers
	}

	for k, v := range headers {
		if strings.EqualFold(k, "profile-title") {
			headers[k] = AppendProfileTitleSuffix(v, suffix)

			break
		}
	}

	return headers
}
