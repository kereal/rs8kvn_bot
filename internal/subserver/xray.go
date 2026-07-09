package subserver

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// ConvertSingleJSONToXray converts a single raw JSON server config (the same
// normalised shape produced by ExtractClashConfigs / ExtractJSONConfigs) into a
// valid Xray-core outbound JSON object. This is the structured equivalent of the
// v2rayN/v2rayNG share link and can be embedded directly into an Xray client
// config's "outbounds" array.
func ConvertSingleJSONToXray(raw json.RawMessage) (json.RawMessage, error) {
	cfg, err := toServerConfig(raw)
	if err != nil {
		logger.Error("Failed to convert JSON config to Xray outbound",
			zap.Error(err),
			zap.String("raw_preview", truncateJSON(raw)))
		return nil, err
	}

	outbound, err := buildXrayOutbound(cfg)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(outbound)
	if err != nil {
		return nil, fmt.Errorf("marshal xray outbound: %w", err)
	}
	return data, nil
}

// ConvertJSONConfigsToXray converts a batch of normalised server configs into a
// slice of Xray outbound JSON objects. Unsupported protocols are skipped (not
// fatal) so a partial subscription still yields usable nodes.
func ConvertJSONConfigsToXray(items []json.RawMessage) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(items))
	for i, raw := range items {
		x, err := ConvertSingleJSONToXray(raw)
		if err != nil {
			logger.Warn("Skipping config in Xray conversion",
				zap.Int("index", i),
				zap.Error(err))
			continue
		}
		out = append(out, x)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid xray configs")
	}
	return out, nil
}

// buildXrayOutbound assembles a single Xray outbound object for the given config.
func buildXrayOutbound(cfg *serverConfig) (map[string]any, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("missing address")
	}
	if cfg.Port == 0 {
		return nil, fmt.Errorf("missing port")
	}

	var outbound map[string]any
	switch strings.ToLower(cfg.Type) {
	case "vless":
		outbound = buildVLESSXray(cfg)
	case "vmess":
		outbound = buildVMessXray(cfg)
	case "trojan":
		outbound = buildTrojanXray(cfg)
	case "shadowsocks", "ss":
		outbound = buildShadowsocksXray(cfg)
	case "hysteria", "hysteria2", "hy2":
		outbound = buildHysteria2Xray(cfg)
	default:
		return nil, fmt.Errorf("unsupported protocol for xray: %s", cfg.Type)
	}

	if cfg.Remark != "" {
		outbound["tag"] = cfg.Remark
	}
	return outbound, nil
}

func buildVLESSXray(cfg *serverConfig) map[string]any {
	if cfg.UUID == "" {
		cfg.UUID = cfg.UserID
	}
	user := map[string]any{
		"id":         cfg.UUID,
		"encryption": "none",
		"level":      0,
	}
	if cfg.Flow != "" {
		user["flow"] = cfg.Flow
	}
	return map[string]any{
		"protocol": "vless",
		"settings": map[string]any{
			"vnext": []map[string]any{
				{
					"address": cfg.Address,
					"port":    cfg.Port,
					"users":   []map[string]any{user},
				},
			},
		},
		"streamSettings": buildStreamSettings(cfg, "vless"),
	}
}

func buildVMessXray(cfg *serverConfig) map[string]any {
	if cfg.UUID == "" {
		cfg.UUID = cfg.UserID
	}
	aid := 0
	if cfg.Aid != "" {
		if n, err := strconv.Atoi(cfg.Aid); err == nil {
			aid = n
		}
	}
	scy := cfg.Scy
	if scy == "" {
		scy = "auto"
	}
	user := map[string]any{
		"id":       cfg.UUID,
		"alterId":  aid,
		"security": scy,
		"level":    0,
	}
	return map[string]any{
		"protocol": "vmess",
		"settings": map[string]any{
			"vnext": []map[string]any{
				{
					"address": cfg.Address,
					"port":    cfg.Port,
					"users":   []map[string]any{user},
				},
			},
		},
		"streamSettings": buildStreamSettings(cfg, "vmess"),
	}
}

func buildTrojanXray(cfg *serverConfig) map[string]any {
	password := cfg.Password
	if password == "" {
		password = cfg.UUID
	}
	server := map[string]any{
		"address":  cfg.Address,
		"port":     cfg.Port,
		"password": password,
		"level":    0,
	}
	return map[string]any{
		"protocol": "trojan",
		"settings": map[string]any{
			"servers": []map[string]any{server},
		},
		"streamSettings": buildStreamSettings(cfg, "trojan"),
	}
}

func buildShadowsocksXray(cfg *serverConfig) map[string]any {
	server := map[string]any{
		"address":  cfg.Address,
		"port":     cfg.Port,
		"method":   cfg.Method,
		"password": cfg.Password,
		"level":    0,
	}
	if cfg.Plugin != "" {
		server["plugin"] = cfg.Plugin
		server["pluginOpts"] = cfg.PluginOpts
	}
	return map[string]any{
		"protocol": "shadowsocks",
		"settings": map[string]any{
			"servers": []map[string]any{server},
		},
		"streamSettings": buildStreamSettings(cfg, "shadowsocks"),
	}
}

func buildHysteria2Xray(cfg *serverConfig) map[string]any {
	password := cfg.Password
	if password == "" {
		password = cfg.UUID
	}
	server := map[string]any{
		"address":  net.JoinHostPort(cfg.Address, strconv.Itoa(cfg.Port)),
		"password": password,
		"level":    0,
	}
	if cfg.SNI != "" {
		server["sni"] = cfg.SNI
	}
	if cfg.Obfs != "" {
		obfs := map[string]any{"type": cfg.Obfs}
		if cfg.ObfsPassword != "" {
			obfs["password"] = cfg.ObfsPassword
		}
		server["obfs"] = obfs
	}
	if cfg.AllowInsecure {
		server["allowInsecure"] = true
	}
	return map[string]any{
		"protocol": "hysteria2",
		"settings": map[string]any{
			"servers": []map[string]any{server},
		},
		"streamSettings": map[string]any{"network": "tcp"},
	}
}



// buildStreamSettings builds the Xray streamSettings object. For VLESS/VMess/Trojan
// it fully resolves the transport (ws/grpc/xhttp/h2/http/tcp) and security layer
// (none/tls/reality). For Shadowsocks/Hysteria2 the transport is plain TCP
// and TLS is handled inside the protocol itself.
func buildStreamSettings(cfg *serverConfig, protocol string) map[string]any {
	switch protocol {
	case "shadowsocks", "hysteria", "hysteria2", "hy2":
		return map[string]any{"network": "tcp"}
	}

	network := cfg.Network
	if network == "" {
		network = "tcp"
	}
	// Xray uses "tcp", not Clash's "raw"; splithttp is now "xhttp".
	if network == "raw" {
		network = "tcp"
	}
	if network == "splithttp" {
		network = "xhttp"
	}
	// Xray uses "http" (httpSettings), not Clash's "h2".
	if network == "h2" {
		network = "http"
	}

	ss := map[string]any{"network": network}
	ss["security"] = resolveXraySecurity(cfg)

	switch ss["security"] {
	case "reality":
		rs := map[string]any{"spiderX": "/"}
		if cfg.SNI != "" {
			rs["serverName"] = cfg.SNI
		}
		if cfg.Fingerprint != "" {
			rs["fingerprint"] = cfg.Fingerprint
		}
		if cfg.PublicKey != "" {
			rs["publicKey"] = cfg.PublicKey
		}
		if cfg.ShortID != "" {
			rs["shortId"] = cfg.ShortID
		}
		ss["realitySettings"] = rs
	case "tls":
		ts := map[string]any{}
		if cfg.SNI != "" {
			ts["serverName"] = cfg.SNI
		}
		if cfg.Fingerprint != "" {
			ts["fingerprint"] = cfg.Fingerprint
		}
		if cfg.AllowInsecure {
			ts["allowInsecure"] = true
		}
		if cfg.Alpn != "" {
			ts["alpn"] = splitComma(cfg.Alpn)
		}
		ss["tlsSettings"] = ts
	}

	switch network {
	case "ws":
		ws := map[string]any{}
		if cfg.Path != "" {
			ws["path"] = cfg.Path
		}
		if cfg.Host != "" {
			ws["headers"] = map[string]any{"Host": cfg.Host}
		}
		ss["wsSettings"] = ws
	case "grpc":
		grpc := map[string]any{"multiMode": false}
		if cfg.Path != "" {
			grpc["serviceName"] = cfg.Path
		}
		ss["grpcSettings"] = grpc
	case "xhttp":
		xh := map[string]any{}
		if cfg.Path != "" {
			xh["path"] = cfg.Path
		}
		if cfg.Host != "" {
			xh["host"] = cfg.Host
		}
		if cfg.Mode != "" {
			xh["mode"] = cfg.Mode
		}
		ss["xhttpSettings"] = xh
	case "h2", "http":
		hs := map[string]any{}
		if cfg.Path != "" {
			hs["path"] = cfg.Path
		}
		if cfg.Host != "" {
			hs["host"] = []string{cfg.Host}
		}
		ss["httpSettings"] = hs
	}

	return ss
}

// resolveXraySecurity determines the Xray security layer from the normalised
// config. VLESS/Trojan carry it in Security; VMess in TLS; the rest default to none.
func resolveXraySecurity(cfg *serverConfig) string {
	if cfg.Security != "" {
		return cfg.Security
	}
	if cfg.TLS != "" {
		return cfg.TLS
	}
	return "none"
}

func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncateJSON(raw json.RawMessage) string {
	s := string(raw)
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
