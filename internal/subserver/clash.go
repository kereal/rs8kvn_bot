package subserver

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// clashConfig is a minimal Clash/Mihomo YAML config — only the "proxies"
// section is needed for subscription aggregation.
type clashConfig struct {
	Proxies []map[string]any `yaml:"proxies"`
}

// realityOpts mirrors the Clash "reality-opts" object.
type realityOpts struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}

// grpcOpts mirrors the Clash "grpc-opts" object.
type grpcOpts struct {
	GrpcServiceName string `yaml:"grpc-service-name"`
}

// wsOpts mirrors the Clash "ws-opts" object.
type wsOpts struct {
	Path    string            `yaml:"path"`
	Headers map[string]string `yaml:"headers"`
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
			zap.String("body_preview", truncateString(string(body), 200)))
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
	setStr(out, p, "server", "address")
	setStr(out, p, "name", "remark")
	if v, ok := p["port"]; ok {
		out["port"] = portToInt(v)
	}

	switch proxyType {
	case "vless":
		setStr(out, p, "uuid", "uuid")
		setStr(out, p, "flow", "flow")
		setStr(out, p, "encryption", "encryption")
		setStr(out, p, "network", "network")
		setStr(out, p, "servername", "sni")
		setStr(out, p, "client-fingerprint", "fp")
		setStr(out, p, "alpn", "alpn")
		// REALITY: presence of reality-opts means security=reality.
		if ro := getMap(p, "reality-opts"); ro != nil {
			out["security"] = "reality"
			if pk, ok := ro["public-key"].(string); ok {
				out["pbk"] = pk
			}
			if sid, ok := ro["short-id"].(string); ok {
				out["sid"] = sid
			}
		} else if b, _ := p["tls"].(bool); b {
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
			if s, ok := w["path"].(string); ok {
				out["path"] = s
			}
			if h, ok := w["headers"].(map[string]any); ok {
				if host, ok := h["Host"].(string); ok {
					out["host"] = host
				}
			}
		}
	case "vmess":
		setStr(out, p, "uuid", "uuid")
		setStr(out, p, "cipher", "scy")
		setStr(out, p, "network", "network")
		setStr(out, p, "servername", "sni")
		setStr(out, p, "client-fingerprint", "fp")
		setStr(out, p, "alpn", "alpn")
		if aID, ok := p["alterId"]; ok {
			out["aid"] = aID
		}
		if b, _ := p["tls"].(bool); b {
			out["tls"] = "tls"
		}
		if g := getMap(p, "grpc-opts"); g != nil {
			if s, ok := g["grpc-service-name"].(string); ok {
				out["path"] = s
			}
		}
		if w := getMap(p, "ws-opts"); w != nil {
			if s, ok := w["path"].(string); ok {
				out["path"] = s
			}
			if h, ok := w["headers"].(map[string]any); ok {
				if host, ok := h["Host"].(string); ok {
					out["host"] = host
				}
			}
		}
	case "trojan":
		setStr(out, p, "password", "password")
		setStr(out, p, "network", "network")
		setStr(out, p, "sni", "sni")
		setStr(out, p, "client-fingerprint", "fp")
		setStr(out, p, "alpn", "alpn")
		if b, _ := p["skip-cert-verify"].(bool); b {
			out["allowInsecure"] = true
		}
		if g := getMap(p, "grpc-opts"); g != nil {
			if s, ok := g["grpc-service-name"].(string); ok {
				out["path"] = s
			}
		}
		if w := getMap(p, "ws-opts"); w != nil {
			if s, ok := w["path"].(string); ok {
				out["path"] = s
			}
			if h, ok := w["headers"].(map[string]any); ok {
				if host, ok := h["Host"].(string); ok {
					out["host"] = host
				}
			}
		}
	case "shadowsocks", "ss":
		setStr(out, p, "cipher", "method")
		setStr(out, p, "password", "password")
	case "socks5", "socks":
		setStr(out, p, "username", "uuid")
		setStr(out, p, "password", "password")
		if b, _ := p["tls"].(bool); b {
			out["tls"] = "tls"
		}
		if b, _ := p["skip-cert-verify"].(bool); b {
			out["allowInsecure"] = true
		}
	case "hysteria", "hysteria2", "hy2":
		setStr(out, p, "password", "password")
		setStr(out, p, "sni", "sni")
		setStr(out, p, "obfs", "obfs")
		setStr(out, p, "obfs-password", "obfsPassword")
		if b, _ := p["skip-cert-verify"].(bool); b {
			out["allowInsecure"] = true
		}
		setStr(out, p, "client-fingerprint", "fp")
	case "tuic":
		setStr(out, p, "uuid", "uuid")
		setStr(out, p, "password", "password")
		setStr(out, p, "sni", "sni")
		setStr(out, p, "alpn", "alpn")
		if b, _ := p["skip-cert-verify"].(bool); b {
			out["allowInsecure"] = true
		}
		setStr(out, p, "client-fingerprint", "fp")
	default:
		return nil, fmt.Errorf("unsupported clash proxy type: %s", proxyType)
	}

	return out, nil
}

// setStr copies a string value from src[key] to dst[jsonTag] if present.
func setStr(dst, src map[string]any, key, jsonTag string) {
	if v, ok := src[key]; ok {
		switch s := v.(type) {
		case string:
			dst[jsonTag] = s
		case int:
			dst[jsonTag] = strconv.Itoa(s)
		case bool:
			dst[jsonTag] = strconv.FormatBool(s)
		}
	}
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
