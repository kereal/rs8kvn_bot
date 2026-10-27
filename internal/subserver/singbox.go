package subserver

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// ConvertJSONConfigsToSingbox renders normalized server configs (the same shape
// produced by ExtractClashConfigs / ExtractJSONConfigs) into sing-box outbound
// objects, one per usable node. Unsupported protocols are skipped (not fatal) so
// a partial subscription still yields usable nodes.
func ConvertJSONConfigsToSingbox(items []json.RawMessage) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(items))
	for i, raw := range items {
		cfg, err := toServerConfig(raw)
		if err != nil {
			logger.Warn("Skipping config in sing-box conversion",
				zap.Int("index", i),
				zap.Error(err))

			continue
		}

		ob, err := buildSingboxOutbound(cfg)
		if err != nil {
			logger.Warn("Skipping config in sing-box conversion",
				zap.Int("index", i),
				zap.Error(err))

			continue
		}

		out = append(out, ob)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no valid sing-box configs")
	}

	return out, nil
}

// buildSingboxOutbound assembles a single sing-box outbound for the given config.
func buildSingboxOutbound(cfg *serverConfig) (map[string]any, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("missing address")
	}

	if cfg.Port == 0 {
		return nil, fmt.Errorf("missing port")
	}

	switch strings.ToLower(cfg.Type) {
	case "vless":
		return buildVLESSSingbox(cfg), nil
	case "vmess":
		return buildVMessSingbox(cfg), nil
	case "trojan":
		return buildTrojanSingbox(cfg), nil
	case "shadowsocks", "ss":
		if cfg.Method == "" {
			return nil, fmt.Errorf("shadowsocks config has no method")
		}
		return buildShadowsocksSingbox(cfg), nil
	case "hysteria", "hysteria2", "hy2":
		return buildHysteria2Singbox(cfg), nil
	case "tuic":
		return buildTUICSingbox(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported protocol for sing-box: %s", cfg.Type)
	}
}

func buildVLESSSingbox(cfg *serverConfig) map[string]any {
	out := map[string]any{
		"type":        "vless",
		"server":      cfg.Address,
		"server_port": cfg.Port,
		"uuid":        cfg.UUID,
	}
	if cfg.Flow != "" {
		out["flow"] = cfg.Flow
	}
	applySingboxTLS(out, cfg)
	applySingboxTransport(out, cfg)
	return out
}

func buildVMessSingbox(cfg *serverConfig) map[string]any {
	aid := 0
	if cfg.Aid != "" {
		n, convErr := strconv.Atoi(cfg.Aid)
		if convErr == nil {
			aid = n
		}
	}
	security := cfg.Scy
	if security == "" {
		security = "auto"
	}
	out := map[string]any{
		"type":        "vmess",
		"server":      cfg.Address,
		"server_port": cfg.Port,
		"uuid":        cfg.UUID,
		"alter_id":    aid,
		"security":    security,
	}
	applySingboxTLS(out, cfg)
	applySingboxTransport(out, cfg)
	return out
}

func buildTrojanSingbox(cfg *serverConfig) map[string]any {
	password := cfg.Password
	if password == "" {
		password = cfg.UUID
	}
	out := map[string]any{
		"type":        "trojan",
		"server":      cfg.Address,
		"server_port": cfg.Port,
		"password":    password,
	}
	if cfg.Flow != "" {
		out["flow"] = cfg.Flow
	}
	applySingboxTLS(out, cfg)
	applySingboxTransport(out, cfg)
	return out
}

func buildShadowsocksSingbox(cfg *serverConfig) map[string]any {
	out := map[string]any{
		"type":        "shadowsocks",
		"server":      cfg.Address,
		"server_port": cfg.Port,
		"method":      cfg.Method,
		"password":    cfg.Password,
	}
	if cfg.Plugin != "" {
		out["plugin"] = cfg.Plugin
		out["plugin_opts"] = cfg.PluginOpts
	}
	return out
}

func buildHysteria2Singbox(cfg *serverConfig) map[string]any {
	password := cfg.Password
	if password == "" {
		password = cfg.UUID
	}
	out := map[string]any{
		"type":        "hysteria2",
		"server":      cfg.Address,
		"server_port": cfg.Port,
		"password":    password,
	}
	if cfg.Obfs != "" {
		obfs := map[string]any{"type": cfg.Obfs}
		if cfg.ObfsPassword != "" {
			obfs["password"] = cfg.ObfsPassword
		}
		out["obfs"] = obfs
	}
	if cfg.AllowInsecure || cfg.SNI != "" || cfg.Fingerprint != "" || splitComma(cfg.Alpn) != nil {
		t := map[string]any{"enabled": true}
		if cfg.SNI != "" {
			t["server_name"] = cfg.SNI
		}
		if cfg.AllowInsecure {
			t["insecure"] = true
		}
		if fp := cfg.Fingerprint; fp != "" {
			t["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		if alpn := splitComma(cfg.Alpn); len(alpn) > 0 {
			t["alpn"] = alpn
		}
		out["tls"] = t
	}
	return out
}

func buildTUICSingbox(cfg *serverConfig) map[string]any {
	password := cfg.Password
	if password == "" {
		password = cfg.UUID
	}
	out := map[string]any{
		"type":               "tuic",
		"server":             cfg.Address,
		"server_port":        cfg.Port,
		"uuid":               cfg.UUID,
		"password":           password,
		"congestion_control": "bbr",
	}
	if cfg.Alpn != "" {
		out["alpn"] = splitComma(cfg.Alpn)
	}
	return out
}

// applySingboxTLS attaches the "tls" field for VLESS/VMess/Trojan. sing-box
// places TLS at the outbound's top level and uses field names that differ from
// Xray (server_name, insecure, utls, reality).
func applySingboxTLS(out map[string]any, cfg *serverConfig) {
	enabled := strings.EqualFold(cfg.TLS, "true") || strings.EqualFold(cfg.TLS, "1") ||
		cfg.Security == "tls" || cfg.Security == "reality" || strings.EqualFold(cfg.Security, "reality")
	if !enabled {
		return
	}

	t := map[string]any{"enabled": true}
	if cfg.AllowInsecure {
		t["insecure"] = true
	}
	if cfg.SNI != "" {
		t["server_name"] = cfg.SNI
	}
	if cfg.Security == "reality" || strings.EqualFold(cfg.Security, "reality") {
		r := map[string]any{}
		if cfg.PublicKey != "" {
			r["public_key"] = cfg.PublicKey
		}
		if cfg.ShortID != "" {
			r["short_id"] = cfg.ShortID
		}
		t["reality"] = r
	} else if fp := cfg.Fingerprint; fp != "" {
		t["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	if alpn := splitComma(cfg.Alpn); len(alpn) > 0 {
		t["alpn"] = alpn
	}

	out["tls"] = t
}

// applySingboxTransport attaches sing-box's "transport" field for non-tcp
// transports. Network aliases are normalized so Clash configs map correctly.
func applySingboxTransport(out map[string]any, cfg *serverConfig) {
	network := cfg.Network
	if network == "" || network == "tcp" || network == "raw" {
		return
	}

	switch network {
	case "ws":
		t := map[string]any{"type": "ws"}
		if cfg.Path != "" {
			t["path"] = cfg.Path
		}
		if cfg.Host != "" {
			t["headers"] = map[string]any{"Host": cfg.Host}
		}
		out["transport"] = t
	case "grpc":
		t := map[string]any{"type": "grpc"}
		if cfg.Path != "" {
			t["service_name"] = cfg.Path
		}
		out["transport"] = t
	case "xhttp", "splithttp":
		t := map[string]any{"type": "xhttp"}
		if cfg.Path != "" {
			t["path"] = cfg.Path
		}
		if cfg.Host != "" {
			t["host"] = cfg.Host
		}
		if cfg.Mode != "" {
			t["mode"] = cfg.Mode
		}
		out["transport"] = t
	case "h2", "http":
		t := map[string]any{"type": "http"}
		if cfg.Path != "" {
			t["path"] = cfg.Path
		}
		if cfg.Host != "" {
			t["host"] = cfg.Host
		}
		out["transport"] = t
	}
}

// BuildSingboxConfig assembles the full sing-box subscription document: server
// outbounds for every usable node plus a "proxy" selector group listing them,
// so clients can switch nodes the same way they switch Xray profiles.
func BuildSingboxConfig(outbounds []map[string]any) map[string]any {
	tags := make([]string, 0, len(outbounds))
	for _, ob := range outbounds {
		if tag, _ := ob["tag"].(string); tag != "" {
			tags = append(tags, tag)
		}
	}

	return map[string]any{
		"outbounds": append(append([]map[string]any{}, outbounds...), map[string]any{
			"type":      "selector",
			"tag":       "proxy",
			"outbounds": tags,
		}),
	}
}

// tagSingboxOutbounds guarantees every server outbound has a unique non-empty
// tag that does not collide with the reserved "proxy" selector tag. The upstream
// remark is kept when unique; otherwise a free positional "proxy-N" tag is used.
// It mutates the supplied outbounds in place.
func tagSingboxOutbounds(outbounds []map[string]any) {
	used := map[string]struct{}{"proxy": {}}
	for i := range outbounds {
		tag, _ := outbounds[i]["tag"].(string)
		if tag == "" {
			tag = freeProxyTag(1, used)
		}
		for {
			if _, dup := used[tag]; !dup {
				break
			}
			tag = freeProxyTag(1, used)
		}
		used[tag] = struct{}{}
		outbounds[i]["tag"] = tag
	}
}
