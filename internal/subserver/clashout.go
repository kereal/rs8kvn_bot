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
// Malformed configs and nodes with an empty address or zero port are also
// skipped. It returns an error only when no entries remain, including empty input.
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
// serverConfig, using its remark as the name or "proxy-<address>" if absent.
// Hysteria2 and its hy2 alias are rendered as hysteria2; legacy hysteria (v1) is
// rejected as an unsupported protocol, as are all other unknown types. Address,
// port, and credentials are not validated here.
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
	case "hysteria2", "hy2":
		return buildHysteria2Clash(cfg, name), nil
	case "tuic":
		return buildTUICClash(cfg, name), nil
	default:
		return nil, fmt.Errorf("unsupported protocol for clash: %s", cfg.Type)
	}
}

// baseClashNode creates a named proxy entry with the server endpoint and UDP enabled.
func baseClashNode(cfg *serverConfig, proxyType string, name string) map[string]any {
	return map[string]any{
		"name":   name,
		"type":   proxyType,
		"server": cfg.Address,
		"port":   cfg.Port,
		"udp":    true,
	}
}

// applyClashNetwork sets transport fields in p. Path is the WebSocket path or
// gRPC service name, or XHTTP path; Host supplies the WebSocket Host header or
// XHTTP host. Unknown networks leave p unchanged.
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
	case "xhttp", "splithttp":
		x := map[string]any{}
		if cfg.Path != "" {
			x["path"] = cfg.Path
		}
		if cfg.Host != "" {
			x["host"] = cfg.Host
		}
		p["network"] = "xhttp"
		p["xhttp-opts"] = x
	case "h2", "http", "tcp", "raw":
		p["network"] = cfg.Network
	}
}

// applyClashTLS adds TLS, SNI, fingerprint, ALPN, Reality, and certificate
// verification options to p when configured. TLS is enabled by TLS="true"/"1"
// or Security="tls"/"reality", ignoring case; other options are copied independently.
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

// buildVLESSClash builds a named VLESS proxy with flow, transport, and TLS options.
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

// buildVMessClash builds a named VMess proxy with transport and TLS options,
// defaulting an empty cipher to "auto" and preserving aid as a string.
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

// buildTrojanClash builds a named Trojan proxy with password, flow, transport,
// and TLS options. An empty password is omitted.
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

// buildShadowsocksClash builds a named Shadowsocks proxy, copying nonempty
// cipher, password, and plugin fields without validating them.
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

// buildHysteria2Clash builds a named Hysteria2 proxy with obfuscation and SNI
// options, using the UUID as the password when Password is empty.
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

// buildTUICClash builds a named TUIC proxy with UUID, password, and ALPN options.
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

// clashPolicyRules land the terminal routing policy that mirrors the Xray and
// sing-box profiles: ads rejected, private/RU domains and IPs direct, everything
// else through the manual PROXY group. Private/loopback ranges are explicit
// IP-CIDR/IP-CIDR6 entries rather than GEOIP,LAN, whose coverage varies between
// the Mihomo and Stash geoip databases.
var clashPolicyRules = []string{
	"GEOSITE,category-ads-all,REJECT",
	"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
	"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
	"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
	"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
	"IP-CIDR,169.254.0.0/16,DIRECT,no-resolve",
	"IP-CIDR6,::1/128,DIRECT,no-resolve",
	"IP-CIDR6,fc00::/7,DIRECT,no-resolve",
	"IP-CIDR6,fe80::/10,DIRECT,no-resolve",
	"GEOIP,RU,DIRECT,no-resolve",
	"GEOSITE,category-ru,DIRECT",
	"GEOSITE,private,DIRECT",
	"MATCH,PROXY",
}

// buildClashConfig adds a manual selector, local DNS and routing policy around
// converted nodes. Proxy names remain the source remarks assigned by
// tagClashProxies; the group and terminal rule use the stable PROXY name.
func buildClashConfig(proxies []map[string]any) clashConfig {
	proxyNames := make([]string, 0, len(proxies))
	for _, proxy := range proxies {
		name, _ := proxy["name"].(string)
		if name != "" {
			proxyNames = append(proxyNames, name)
		}
	}

	rules := make([]string, 0, len(checkIPDomains)+len(clashPolicyRules))
	for _, domain := range checkIPDomains {
		rules = append(rules, "DOMAIN-SUFFIX,"+domain+",DIRECT")
	}
	rules = append(rules, clashPolicyRules...)

	return clashConfig{
		MixedPort: 7890,
		AllowLAN:  false,
		Mode:      "rule",
		LogLevel:  "warning",
		IPv6:      true,
		Profile:   map[string]bool{"store-selected": true},
		DNS: map[string]any{
			"enable":                          true,
			"ipv6":                            true,
			"enhanced-mode":                   "fake-ip",
			"fake-ip-range":                   "198.18.0.1/16",
			"default-nameserver":              []string{"77.88.8.8", "1.1.1.1"},
			"nameserver":                      []string{"https://1.1.1.1/dns-query", "77.88.8.8"},
			"proxy-server-nameserver":         []string{"77.88.8.8", "1.1.1.1"},
			"direct-nameserver":               []string{"77.88.8.8", "1.1.1.1"},
			"direct-nameserver-follow-policy": true,
			"nameserver-policy": map[string]any{
				"geosite:category-ru,private": []string{"77.88.8.8"},
			},
			"fake-ip-filter": []string{"geosite:private", "geosite:category-ru", "+.lan", "+.local"},
		},
		Proxies: proxies,
		ProxyGroups: []map[string]any{
			{"name": "PROXY", "type": "select", "proxies": proxyNames},
		},
		Rules: rules,
	}
}

// clashGroupNames lists profile-internal outbound names (the PROXY selector).
// tagClashProxies reserves them so a node remarked "PROXY" is renamed instead of
// stealing the group name.
var clashGroupNames = []string{"PROXY"}

// tagClashProxies guarantees every proxy entry has a unique non-empty "name".
// The upstream remark is kept when unique and not reserved for the PROXY group;
// otherwise the first free "proxy-N" name starting at 1 is used. It mutates the
// supplied entries in place.
func tagClashProxies(proxies []map[string]any) {
	used := make(map[string]struct{}, len(proxies)+len(clashGroupNames))
	for _, reserved := range clashGroupNames {
		used[reserved] = struct{}{}
	}
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
