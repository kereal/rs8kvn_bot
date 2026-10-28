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
// Malformed configs, nodes with an empty address or zero port, and Shadowsocks
// nodes without a method are also skipped. It returns an error only when no
// entries remain, including empty input.
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
// It returns an error for an empty address, a zero port, an unsupported protocol,
// or a Shadowsocks config without a method. Hysteria variants render as hysteria2.
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

// singboxOutboundBase creates the common fields every server outbound carries,
// including the readable tag from the upstream remark (node name), so clients
// show recognizable names instead of positional proxy-N tags.
func singboxOutboundBase(proxyType string, cfg *serverConfig) map[string]any {
	out := map[string]any{
		"type":        proxyType,
		"server":      cfg.Address,
		"server_port": cfg.Port,
	}
	if cfg.Remark != "" {
		out["tag"] = cfg.Remark
	}
	return out
}

// buildVLESSSingbox builds a VLESS outbound with flow, TLS, and transport options.
func buildVLESSSingbox(cfg *serverConfig) map[string]any {
	out := singboxOutboundBase("vless", cfg)
	out["uuid"] = cfg.UUID
	if cfg.Flow != "" {
		out["flow"] = cfg.Flow
	}
	applySingboxTLS(out, cfg)
	applySingboxTransport(out, cfg)
	return out
}

// buildVMessSingbox builds a VMess outbound with TLS and transport options.
// Missing or non-integer aid becomes zero; an empty cipher becomes "auto".
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
	out := singboxOutboundBase("vmess", cfg)
	out["uuid"] = cfg.UUID
	out["alter_id"] = aid
	out["security"] = security
	applySingboxTLS(out, cfg)
	applySingboxTransport(out, cfg)
	return out
}

// buildTrojanSingbox builds a Trojan outbound with flow, TLS, and transport
// options, using the UUID as the password when Password is empty.
func buildTrojanSingbox(cfg *serverConfig) map[string]any {
	password := cfg.Password
	if password == "" {
		password = cfg.UUID
	}
	out := singboxOutboundBase("trojan", cfg)
	out["password"] = password
	if cfg.Flow != "" {
		out["flow"] = cfg.Flow
	}
	applySingboxTLS(out, cfg)
	applySingboxTransport(out, cfg)
	return out
}

// buildShadowsocksSingbox builds a Shadowsocks outbound with cipher and password,
// including plugin options only when a plugin name is present.
func buildShadowsocksSingbox(cfg *serverConfig) map[string]any {
	out := singboxOutboundBase("shadowsocks", cfg)
	out["method"] = cfg.Method
	out["password"] = cfg.Password
	if cfg.Plugin != "" {
		out["plugin"] = cfg.Plugin
		out["plugin_opts"] = cfg.PluginOpts
	}
	return out
}

// buildHysteria2Singbox builds a Hysteria2 outbound with obfuscation and TLS
// options, using the UUID as the password when Password is empty. An obfuscation
// password is included only when an obfuscation mode is set.
func buildHysteria2Singbox(cfg *serverConfig) map[string]any {
	password := cfg.Password
	if password == "" {
		password = cfg.UUID
	}
	out := singboxOutboundBase("hysteria2", cfg)
	out["password"] = password
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

// buildTUICSingbox builds a TUIC outbound with BBR congestion control and ALPN,
// using the UUID as the password when Password is empty.
func buildTUICSingbox(cfg *serverConfig) map[string]any {
	password := cfg.Password
	if password == "" {
		password = cfg.UUID
	}
	out := singboxOutboundBase("tuic", cfg)
	out["uuid"] = cfg.UUID
	out["password"] = password
	out["congestion_control"] = "bbr"
	t := map[string]any{"enabled": true}
	if cfg.SNI != "" {
		t["server_name"] = cfg.SNI
	}
	if cfg.AllowInsecure {
		t["insecure"] = true
	}
	if alpn := splitComma(cfg.Alpn); len(alpn) > 0 {
		t["alpn"] = alpn
	}
	out["tls"] = t
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
// Path supplies the gRPC service name or transport path, and Host supplies the
// WebSocket Host header or HTTP/XHTTP host. Unrecognized networks leave out unchanged.
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

// checkIPDomains mirrors the Xray template's IP-check blocklist: services that
// reveal the exit IP are routed direct so users see their real location.
var checkIPDomains = []string{
	"api.ipify.org", "checkip.amazonaws.com", "ifconfig.me", "ip.mail.ru",
	"ipv4-internet.yandex.net", "ipv6-internet.yandex.net", "2ip.ru", "2ip.io",
	"whoer.net", "ipinfo.io", "ipapi.co", "ip-api.com", "ipify.org", "ifconfig.co",
}

// SagerNet/sing-geosite and sing-geoip provide the binary rule-sets that replace
// the deprecated geosite/geoip fields. All four URLs respond with HTTP 206 (partial
// content) indicating the .srs files are live and downloadable. The update_interval
// field (1d) controls how aggressively clients refresh the cache.
var ruleSetDefs = []map[string]any{
	{"type": "remote", "tag": "ads-all", "format": "binary",
		"url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ads-all.srs",
		"update_interval": "24h"},
	{"type": "remote", "tag": "geosite-ru", "format": "binary",
		"url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ru.srs",
		"update_interval": "24h"},
	{"type": "remote", "tag": "geosite-private", "format": "binary",
		"url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-private.srs",
		"update_interval": "24h"},
	{"type": "remote", "tag": "geoip-ru", "format": "binary",
		"url": "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-ru.srs",
		"update_interval": "24h"},
}

// BuildSingboxConfig builds a complete sing-box 1.12+ client config. It mirrors
// the Xray profile template but uses the modern rule-set mechanism for geosite/geoip
// lookups (since those fields were removed in sing-box 1.12), the action: reject
// rule for ad blocking (replacing the removed block outbound from 1.13), and
// typed DNS servers with type/server fields. The sniffing rule migrates
// the legacy inbound sniff field to the recommended route action.
// The profile includes a mixed listener on 127.0.0.1:10808, a "proxy" selector
// listing nonempty outbound tags, and a direct outbound. Callers must assign
// suitable unique tags before calling. The config references remote rule sets
// for clients to download.
func BuildSingboxConfig(outbounds []map[string]any) map[string]any {
	tags := make([]string, 0, len(outbounds))
	for _, ob := range outbounds {
		if tag, _ := ob["tag"].(string); tag != "" {
			tags = append(tags, tag)
		}
	}

	directOutbound := map[string]any{"type": "direct", "tag": "direct"}
	selector := map[string]any{
		"type":                        "selector",
		"tag":                         "proxy",
		"outbounds":                   tags,
		"interrupt_exist_connections": false,
	}

	// route.rules are ordered: first match wins. sniffer runs first to populate
	// connection metadata (domain, sni) so subsequent rules can match by content.
	// reject handles ads; IP-check and private/range rules route direct; the
	// rule-set rules replace the removed geosite/geoip fields. final = proxy.
	return map[string]any{
		"log": map[string]any{"level": "warn"},
		"dns": map[string]any{
			// New typed DNS server format (1.12+; legacy address/detour strings
			// were removed in 1.14). proxy-dns routes through the proxy selector,
			// ru-dns through direct.
			"servers": []map[string]any{
				{"type": "https", "tag": "proxy-dns", "server": "1.1.1.1", "detour": "proxy"},
				{"type": "udp", "tag": "ru-dns", "server": "77.88.8.8", "detour": "direct"},
			},
			// Rule: ru/private domains use ru-dns (mirrors Xray skipFallback for geosite:category-ru).
			"rules": []map[string]any{
				{"rule_set": []string{"geosite-ru", "geosite-private"}, "server": "ru-dns"},
			},
			"strategy": "prefer_ipv4",
		},
		"inbounds": []map[string]any{
			{
				"type":        "mixed",
				"tag":         "mixed-in",
				"listen":      "127.0.0.1",
				"listen_port": 10808,
			},
		},
		"outbounds": append(append([]map[string]any{}, outbounds...), selector, directOutbound),
		"route": map[string]any{
			"auto_detect_interface": true,
			"final":                 "proxy",
			"rule_set":              ruleSetDefs,
			// Required since 1.14 when multiple DNS servers exist and outbounds
			// use domain server addresses (node hostnames / reality sni): resolves
			// node domains via ru-dns (direct), avoiding a circular dependency
			// through the tunnel itself.
			"default_domain_resolver": "ru-dns",
			"rules": []map[string]any{
				// Migrate legacy inbound sniff → route action sniff.
				{"action": "sniff", "inbound": []string{"mixed-in"}},
				// Block ads via rule-set (block outbound removed in 1.13).
				{"rule_set": []string{"ads-all"}, "action": "reject"},
				// IP-check sites route direct so users see their real exit IP.
				{"domain_suffix": checkIPDomains, "outbound": "direct"},
				// Private IPs never go through the proxy.
				{"ip_is_private": true, "outbound": "direct"},
				// Russian IP ranges route direct (modern equivalent of geoip:ru, removed 1.12).
				{"rule_set": []string{"geoip-ru"}, "outbound": "direct"},
				// Russian/private sites route direct (modern equivalent of geosite:category-ru/private, removed 1.12).
				{"rule_set": []string{"geosite-ru", "geosite-private"}, "outbound": "direct"},
			},
		},
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
