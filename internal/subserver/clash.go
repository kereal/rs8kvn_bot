package subserver

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// clashConfig is a minimal Clash/Mihomo YAML config — only the "proxies"
// section is needed for subscription aggregation.
type clashConfig struct {
	Proxies []map[string]any `yaml:"proxies"`
}

// ExtractClashConfigs parses a Clash/Mihomo YAML config body and returns the
// proxy entries as json.RawMessage slice (compatible with ExtractJSONConfigs).
// Each proxy map is normalised to the serverConfig JSON schema and re-marshalled,
// so downstream ConvertSingleJSONToLink can consume it unchanged.
func ExtractClashConfigs(body []byte) ([]json.RawMessage, error) {
	var cfg clashConfig
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		logger.Error("Failed to parse Clash YAML",
			zap.Error(err),
			zap.String("body_preview", utils.TruncateString(string(body), 200)))
		return nil, fmt.Errorf("parse clash yaml: %w", err)
	}

	if len(cfg.Proxies) == 0 {
		return nil, fmt.Errorf("clash config has no proxies")
	}

	items := make([]json.RawMessage, 0, len(cfg.Proxies))
	for i, proxy := range cfg.Proxies {
		normalised, err := normaliseClashProxy(proxy)
		if err != nil {
			logger.Warn("Skipping clash proxy entry",
				zap.Int("index", i),
				zap.Error(err))
			continue
		}
		raw, err := json.Marshal(normalised)
		if err != nil {
			logger.Warn("Failed to marshal normalised clash proxy",
				zap.Int("index", i),
				zap.Error(err))
			continue
		}
		items = append(items, raw)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no valid clash proxy entries")
	}
	return items, nil
}

// normaliseClashProxy converts a Clash proxy map into a map keyed by the
// serverConfig JSON tags, so json.Marshal produces a serverConfig-compatible object.
func normaliseClashProxy(p map[string]any) (map[string]any, error) {
	proxyType, _ := p["type"].(string)
	if proxyType == "" {
		return nil, fmt.Errorf("missing type field")
	}
	proxyType = strings.ToLower(proxyType)

	out := make(map[string]any)

	// Common fields shared across protocols.
	out["type"] = proxyType
	setStrFallback(out, p, []string{"server", "address"}, "address")
	setStrFallback(out, p, []string{"name", "remark"}, "remark")
	if v, ok := p["port"]; ok {
		out["port"] = portToInt(v)
	}

	switch proxyType {
	case "vless":
		setStrFallback(out, p, []string{"uuid", "id", "userId"}, "uuid")
		setStrFallback(out, p, []string{"flow"}, "flow")
		setStrFallback(out, p, []string{"encryption"}, "encryption")
		setStrFallback(out, p, []string{"network", "net"}, "network")
		setStrFallback(out, p, []string{"servername", "sni"}, "sni")
		setStrFallback(out, p, []string{"client-fingerprint", "fp"}, "fp")
		setStrFallback(out, p, []string{"alpn"}, "alpn")
		// REALITY: presence of reality-opts means security=reality.
		if ro := getMap(p, "reality-opts"); ro != nil {
			out["security"] = "reality"
			setStrFallback(out, ro, []string{"public-key", "pbk"}, "pbk")
			setStrFallback(out, ro, []string{"short-id", "sid"}, "sid")
		} else if toBool(p["tls"]) {
			out["security"] = "tls"
		}
		// grpc transport.
		if g := getMap(p, "grpc-opts"); g != nil {
			if s, ok := g["grpc-service-name"].(string); ok {
				out["path"] = s
			}
		}
		// ws transport.
		if w := getMap(p, "ws-opts"); w != nil {
			setWSHost(out, w)
			setWSPath(out, w)
		} else if h := getMap(p, "ws-headers"); h != nil {
			if host := getHostFromHeaders(h); host != "" {
				out["host"] = host
			}
		}
	case "vmess":
		setStrFallback(out, p, []string{"uuid", "id", "userId"}, "uuid")
		setStrFallback(out, p, []string{"cipher", "scy"}, "scy")
		setStrFallback(out, p, []string{"network", "net"}, "network")
		setStrFallback(out, p, []string{"servername", "sni"}, "sni")
		setStrFallback(out, p, []string{"client-fingerprint", "fp"}, "fp")
		setStrFallback(out, p, []string{"alpn"}, "alpn")
		setStrFallback(out, p, []string{"alterId", "aid"}, "aid")
		if toBool(p["tls"]) {
			out["tls"] = "tls"
		}
		if g := getMap(p, "grpc-opts"); g != nil {
			if s, ok := g["grpc-service-name"].(string); ok {
				out["path"] = s
			}
		}
		if w := getMap(p, "ws-opts"); w != nil {
			setWSHost(out, w)
			setWSPath(out, w)
		} else if h := getMap(p, "ws-headers"); h != nil {
			if host := getHostFromHeaders(h); host != "" {
				out["host"] = host
			}
		}
	case "trojan":
		setStrFallback(out, p, []string{"password", "pass"}, "password")
		setStrFallback(out, p, []string{"network", "net"}, "network")
		setStrFallback(out, p, []string{"sni", "servername"}, "sni")
		setStrFallback(out, p, []string{"client-fingerprint", "fp"}, "fp")
		setStrFallback(out, p, []string{"alpn"}, "alpn")
		if toBool(p["skip-cert-verify"]) || toBool(p["allowInsecure"]) {
			out["allowInsecure"] = true
		}
		if toBool(p["tls"]) {
			out["tls"] = "tls"
		}
		if g := getMap(p, "grpc-opts"); g != nil {
			if s, ok := g["grpc-service-name"].(string); ok {
				out["path"] = s
			}
		}
		if w := getMap(p, "ws-opts"); w != nil {
			setWSHost(out, w)
			setWSPath(out, w)
		} else if h := getMap(p, "ws-headers"); h != nil {
			if host := getHostFromHeaders(h); host != "" {
				out["host"] = host
			}
		}
	case "shadowsocks", "ss":
		setStrFallback(out, p, []string{"cipher", "method"}, "method")
		setStrFallback(out, p, []string{"password", "pass"}, "password")
	case "hysteria", "hysteria2", "hy2":
		setStrFallback(out, p, []string{"password", "auth", "auth-str", "auth_str"}, "password")
		setStrFallback(out, p, []string{"sni", "servername"}, "sni")
		setStrFallback(out, p, []string{"obfs"}, "obfs")
		setStrFallback(out, p, []string{"obfs-password", "obfs_password", "obfsPassword"}, "obfsPassword")
		if toBool(p["skip-cert-verify"]) || toBool(p["allowInsecure"]) {
			out["allowInsecure"] = true
		}
		setStrFallback(out, p, []string{"client-fingerprint", "fp"}, "fp")
		setStrFallback(out, p, []string{"alpn"}, "alpn")
	case "tuic":
		setStrFallback(out, p, []string{"uuid", "id", "userId"}, "uuid")
		setStrFallback(out, p, []string{"password", "pass"}, "password")
		setStrFallback(out, p, []string{"sni", "servername"}, "sni")
		setStrFallback(out, p, []string{"alpn"}, "alpn")
		if toBool(p["skip-cert-verify"]) || toBool(p["allowInsecure"]) {
			out["allowInsecure"] = true
		}
		setStrFallback(out, p, []string{"client-fingerprint", "fp"}, "fp")
	default:
		return nil, fmt.Errorf("unsupported clash proxy type: %s", proxyType)
	}

	return out, nil
}

// setWSHost extracts the WS Host header (case-insensitive) into out["host"].
func setWSHost(out, wsOpts map[string]any) {
	if host := getWSHeaderHost(wsOpts); host != "" {
		out["host"] = host
	}
}

// setWSPath extracts the WS path into out["path"].
func setWSPath(out, wsOpts map[string]any) {
	if s, ok := wsOpts["path"].(string); ok {
		out["path"] = s
	}
}

// setStrFallback copies the first found string value from src[keys] to dst[jsonTag] if present.
func setStrFallback(dst, src map[string]any, keys []string, jsonTag string) {
	for _, key := range keys {
		if v, ok := src[key]; ok {
			switch s := v.(type) {
			case string:
				dst[jsonTag] = s
				return
			case int:
				dst[jsonTag] = strconv.Itoa(s)
				return
			case bool:
				dst[jsonTag] = strconv.FormatBool(s)
				return
			}
		}
	}
}

// toBool converts any value (bool, string, int) to bool.
func toBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		lower := strings.ToLower(b)
		return lower == "true" || lower == "1"
	case int:
		return b != 0
	}
	return false
}

// getHostFromHeaders extracts case-insensitive Host header value.
func getHostFromHeaders(headers map[string]any) string {
	return getWSHeaderHost(headers)
}

// getWSHeaderHost returns the case-insensitive "host" header value from a map
// that may be a ws-opts (with nested "headers") or a raw headers map.
func getWSHeaderHost(wsOpts map[string]any) string {
	headers := getMap(wsOpts, "headers")
	src := wsOpts
	if headers != nil {
		src = headers
	}
	for k, v := range src {
		if strings.EqualFold(k, "host") {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

// getMap returns a nested map field, or nil if absent/not a map.
func getMap(src map[string]any, key string) map[string]any {
	if v, ok := src[key]; ok {
		if m, ok := v.(map[string]any); ok {
			return m
		}
	}
	return nil
}

// portToInt converts a port value (string or int) to int.
func portToInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case string:
		p, _ := strconv.Atoi(n)
		return p
	}
	return 0
}
