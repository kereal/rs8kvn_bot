package subserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// ConvertJSONConfigsToClash renders normalized server configs into Clash/Mihomo
// proxy entries, one per usable node. Unsupported protocols are skipped (not
// fatal) so a partial subscription still yields usable nodes.
func ConvertJSONConfigsToClash(items []json.RawMessage) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(items))
	for i, raw := range items {
		cfg, err := toServerConfig(raw)
		if err != nil {
			logger.Warn("Skipping config in Clash conversion",
				zap.Int("index", i),
				zap.Error(err))

			continue
		}
		proxy, err := buildClashProxy(cfg)
		if err != nil {
			logger.Warn("Skipping config in Clash conversion",
				zap.Int("index", i),
				zap.Error(err))

			continue
		}
		if cfg.Address == "" || cfg.Port == 0 {
			continue
		}
		out = append(out, proxy)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no valid clash configs")
	}

	return out, nil
}

// buildClashProxy assembles a single Clash/Mihomo proxy entry from a normalized
// serverConfig. It is the exact reverse of normaliseClashProxy, so the same node
// data round-trips between the Clash parser and the Clash renderer.
func buildClashProxy(cfg *serverConfig) (map[string]any, error) {
	name := cfg.Remark
	if name == "" {
		name = fmt.Sprintf("proxy-%s", cfg.Address)
	}

	switch strings.ToLower(cfg.Type) {
	case "vless":
		return buildVLESSClash(cfg, name), nil
	case "vmess":
		return buildVMessClash(cfg, name), nil
	case "trojan":
		return buildTrojanClash(cfg, name), nil
	case "shadowsocks", "ss":
		return buildShadowsocksClash(cfg, name), nil
	case "hysteria", "hysteria2", "hy2":
		return buildHysteria2Clash(cfg, name), nil
	case "tuic":
		return buildTUICClash(cfg, name), nil
	default:
		return nil, fmt.Errorf("unsupported protocol for clash: %s", cfg.Type)
	}
}

func baseClashNode(cfg *serverConfig, proxyType string, name string) map[string]any {
	return map[string]any{
		"name":   name,
		"type":   proxyType,
		"server": cfg.Address,
		"port":   cfg.Port,
		"udp":    true,
	}
}

func applyClashNetwork(p map[string]any, cfg *serverConfig) {
	switch cfg.Network {
	case "ws":
		ws := map[string]any{}
		if cfg.Path != "" {
			ws["path"] = cfg.Path
		}
		if cfg.Host != "" {
			ws["headers"] = map[string]any{"Host": cfg.Host}
		}
		p["network"] = "ws"
		p["ws-opts"] = ws
	case "grpc":
		g := map[string]any{}
		if cfg.Path != "" {
			g["grpc-service-name"] = cfg.Path
		}
		p["network"] = "grpc"
		p["grpc-opts"] = g
	case "xhttp", "h2", "http", "splithttp", "tcp", "raw":
		p["network"] = cfg.Network
	}
}

func applyClashTLS(p map[string]any, cfg *serverConfig) {
	security := strings.ToLower(cfg.Security)
	tlsEnabled := strings.EqualFold(cfg.TLS, "true") || strings.EqualFold(cfg.TLS, "1") ||
		security == "tls" || security == "reality"
	if tlsEnabled {
		p["tls"] = true
	}
	if cfg.SNI != "" {
		p["servername"] = cfg.SNI
	}
	if cfg.Fingerprint != "" {
		p["client-fingerprint"] = cfg.Fingerprint
	}
	if cfg.Alpn != "" {
		p["alpn"] = splitComma(cfg.Alpn)
	}
	if security == "reality" {
		r := map[string]any{}
		if cfg.PublicKey != "" {
			r["public-key"] = cfg.PublicKey
		}
		if cfg.ShortID != "" {
			r["short-id"] = cfg.ShortID
		}
		p["reality-opts"] = r
	}
	if cfg.AllowInsecure {
		p["skip-cert-verify"] = true
	}
}

func buildVLESSClash(cfg *serverConfig, name string) map[string]any {
	p := baseClashNode(cfg, "vless", name)
	if cfg.UUID != "" {
		p["uuid"] = cfg.UUID
	}
	if cfg.Flow != "" {
		p["flow"] = cfg.Flow
	}
	applyClashNetwork(p, cfg)
	applyClashTLS(p, cfg)
	return p
}

func buildVMessClash(cfg *serverConfig, name string) map[string]any {
	p := baseClashNode(cfg, "vmess", name)
	if cfg.UUID != "" {
		p["uuid"] = cfg.UUID
	}
	if cfg.Aid != "" {
		p["alterId"] = cfg.Aid
	}
	cipher := cfg.Scy
	if cipher == "" {
		cipher = "auto"
	}
	p["cipher"] = cipher
	applyClashNetwork(p, cfg)
	applyClashTLS(p, cfg)
	return p
}

func buildTrojanClash(cfg *serverConfig, name string) map[string]any {
	p := baseClashNode(cfg, "trojan", name)
	if cfg.Password != "" {
		p["password"] = cfg.Password
	}
	applyClashNetwork(p, cfg)
	applyClashTLS(p, cfg)
	if cfg.Flow != "" {
		p["flow"] = cfg.Flow
	}
	return p
}

func buildShadowsocksClash(cfg *serverConfig, name string) map[string]any {
	p := baseClashNode(cfg, "ss", name)
	if cfg.Method != "" {
		p["cipher"] = cfg.Method
	}
	if cfg.Password != "" {
		p["password"] = cfg.Password
	}
	if cfg.Plugin != "" {
		p["plugin"] = cfg.Plugin
	}
	if cfg.PluginOpts != "" {
		p["plugin-opts"] = cfg.PluginOpts
	}
	return p
}

func buildHysteria2Clash(cfg *serverConfig, name string) map[string]any {
	p := baseClashNode(cfg, "hysteria2", name)
	password := cfg.Password
	if password == "" {
		password = cfg.UUID
	}
	p["password"] = password
	if cfg.Obfs != "" {
		p["obfs"] = cfg.Obfs
	}
	if cfg.ObfsPassword != "" {
		p["obfs-password"] = cfg.ObfsPassword
	}
	if cfg.SNI != "" {
		p["sni"] = cfg.SNI
	}
	if cfg.AllowInsecure {
		p["skip-cert-verify"] = true
	}
	return p
}

func buildTUICClash(cfg *serverConfig, name string) map[string]any {
	p := baseClashNode(cfg, "tuic", name)
	if cfg.UUID != "" {
		p["uuid"] = cfg.UUID
	}
	if cfg.Password != "" {
		p["password"] = cfg.Password
	}
	if cfg.Alpn != "" {
		p["alpn"] = splitComma(cfg.Alpn)
	}
	return p
}

// tagClashProxies guarantees every proxy entry has a unique non-empty "name".
// The upstream remark is kept when unique; otherwise a free positional
// "proxy-N" name is used. It mutates the supplied entries in place.
func tagClashProxies(proxies []map[string]any) {
	used := make(map[string]struct{}, len(proxies))
	for i := range proxies {
		name, _ := proxies[i]["name"].(string)
		if name == "" {
			name = freeProxyTag(1, used)
		}
		for {
			if _, dup := used[name]; !dup {
				break
			}
			name = freeProxyTag(1, used)
		}
		used[name] = struct{}{}
		proxies[i]["name"] = name
	}
}
