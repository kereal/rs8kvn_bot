package subserver

import (
	"encoding/json"
	"fmt"
	"sort"
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
		if port := portToInt(v); port > 0 {
			out["port"] = port
		}
	}
	// Hysteria2 port-hopping: "ports: 443,20000-50000" — use first concrete port
	// when the singular "port" field is absent so the proxy is not dropped.
	if _, ok := out["port"]; !ok {
		if port := firstPortFromPorts(p["ports"]); port > 0 {
			out["port"] = port
		}
	}

	switch proxyType {
	case "vless":
		setStrFallback(out, p, []string{"uuid", "id", "userId"}, "uuid")
		setStrFallback(out, p, []string{"flow"}, "flow")
		setStrFallback(out, p, []string{"encryption"}, "encryption")
		setStrFallback(out, p, []string{"network", "net"}, "network")
		normaliseTransportNetwork(out)
		setStrFallback(out, p, []string{"servername", "sni"}, "sni")
		setStrFallback(out, p, []string{"client-fingerprint", "fp"}, "fp")
		setAlpn(out, p)
		setPacketEncoding(out, p)
		if toBool(p["skip-cert-verify"]) || toBool(p["allowInsecure"]) {
			out["allowInsecure"] = true
		}
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
		// xhttp (SplitHTTP / XTLS) transport.
		if x := getMap(p, "xhttp-opts"); x != nil {
			setStrFallback(out, x, []string{"path"}, "path")
			setStrFallback(out, x, []string{"host"}, "host")
			setStrFallback(out, x, []string{"mode"}, "mode")
		}
		// http transport (HTTP/1.1 disguise).
		if h := getMap(p, "http-opts"); h != nil {
			if s := firstListOrString(h["path"]); s != "" {
				out["path"] = s
			}
			if s := httpOptsHost(h); s != "" {
				out["host"] = s
			}
		}
		// h2 transport options.
		if h := getMap(p, "h2-opts"); h != nil {
			if s := firstListOrString(h["path"]); s != "" {
				out["path"] = s
			}
			if s := httpOptsHost(h); s != "" {
				out["host"] = s
			}
		}
	case "vmess":
		setStrFallback(out, p, []string{"uuid", "id", "userId"}, "uuid")
		setStrFallback(out, p, []string{"cipher", "scy"}, "scy")
		setStrFallback(out, p, []string{"network", "net"}, "network")
		normaliseTransportNetwork(out)
		setStrFallback(out, p, []string{"servername", "sni"}, "sni")
		setStrFallback(out, p, []string{"client-fingerprint", "fp"}, "fp")
		setAlpn(out, p)
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
		// For vmess, the v2ray "header type" (none/http) is expressed in Clash
		// via network: http (HTTP-obfs). http-opts carries path/host.
		if net, _ := out["network"].(string); net == "http" {
			out["headerType"] = "http"
		}
		if h := getMap(p, "http-opts"); h != nil {
			if s := firstListOrString(h["path"]); s != "" {
				out["path"] = s
			}
			if s := httpOptsHost(h); s != "" {
				out["host"] = s
			}
		}
		// h2 transport options.
		if h := getMap(p, "h2-opts"); h != nil {
			if s := firstListOrString(h["path"]); s != "" {
				out["path"] = s
			}
			if s := httpOptsHost(h); s != "" {
				out["host"] = s
			}
		}
	case "trojan":
		setStrFallback(out, p, []string{"password", "pass"}, "password")
		setStrFallback(out, p, []string{"network", "net"}, "network")
		normaliseTransportNetwork(out)
		setStrFallback(out, p, []string{"sni", "servername"}, "sni")
		setStrFallback(out, p, []string{"client-fingerprint", "fp"}, "fp")
		setAlpn(out, p)
		if toBool(p["skip-cert-verify"]) || toBool(p["allowInsecure"]) {
			out["allowInsecure"] = true
		}
		// Clash Meta always runs Trojan over TLS; the tls field is often omitted.
		if toBool(p["tls"]) || !toBool(p["tls"]) && p["tls"] == nil {
			// Default security=tls unless the source explicitly disables it.
			if v, ok := p["tls"]; ok && !toBool(v) {
				// explicit tls: false — leave security unset
			} else {
				out["security"] = "tls"
			}
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
		// xhttp / http / h2 transports (same shape as VLESS).
		if x := getMap(p, "xhttp-opts"); x != nil {
			setStrFallback(out, x, []string{"path"}, "path")
			setStrFallback(out, x, []string{"host"}, "host")
			setStrFallback(out, x, []string{"mode"}, "mode")
		}
		if h := getMap(p, "http-opts"); h != nil {
			if s := firstListOrString(h["path"]); s != "" {
				out["path"] = s
			}
			if s := httpOptsHost(h); s != "" {
				out["host"] = s
			}
		}
		if h := getMap(p, "h2-opts"); h != nil {
			if s := firstListOrString(h["path"]); s != "" {
				out["path"] = s
			}
			if s := httpOptsHost(h); s != "" {
				out["host"] = s
			}
		}
	case "shadowsocks", "ss":
		setStrFallback(out, p, []string{"cipher", "method"}, "method")
		setStrFallback(out, p, []string{"password", "pass"}, "password")
		// SIP002 plugin (simple-obfs / v2ray-plugin). The Clash "plugin" field
		// names the plugin and "plugin-opts" carries its parameters.
		if plugin, ok := p["plugin"].(string); ok && plugin != "" {
			out["plugin"] = plugin
			if po := getMap(p, "plugin-opts"); po != nil {
				out["pluginOpts"] = formatSSPluginOpts(plugin, po)
			}
		}
	case "hysteria", "hysteria2", "hy2":
		// Share links and most clients use hysteria2://; keep hy2 as an input
		// alias only so Happ/sing-box do not see a non-standard scheme.
		if proxyType == "hy2" || proxyType == "hysteria2" {
			out["type"] = "hysteria2"
		}
		setStrFallback(out, p, []string{"password", "auth", "auth-str", "auth_str"}, "password")
		setStrFallback(out, p, []string{"sni", "servername"}, "sni")
		setStrFallback(out, p, []string{"obfs"}, "obfs")
		setStrFallback(out, p, []string{"obfs-password", "obfs_password", "obfsPassword"}, "obfsPassword")
		if toBool(p["skip-cert-verify"]) || toBool(p["allowInsecure"]) || toBool(p["insecure"]) {
			out["allowInsecure"] = true
		}
		setStrFallback(out, p, []string{"client-fingerprint", "fp"}, "fp")
		setAlpn(out, p)
	case "tuic":
		setStrFallback(out, p, []string{"uuid", "id", "userId"}, "uuid")
		setStrFallback(out, p, []string{"password", "pass"}, "password")
		setStrFallback(out, p, []string{"sni", "servername"}, "sni")
		setAlpn(out, p)
		if toBool(p["skip-cert-verify"]) || toBool(p["allowInsecure"]) {
			out["allowInsecure"] = true
		}
		setStrFallback(out, p, []string{"client-fingerprint", "fp"}, "fp")
	default:
		return nil, fmt.Errorf("unsupported clash proxy type: %s", proxyType)
	}

	// Skip entries missing the minimum required connection fields instead of
	// emitting a malformed URI (e.g. vless://uuid@:443?... with an empty host).
	if addr, _ := out["address"].(string); addr == "" {
		return nil, fmt.Errorf("clash proxy %q missing address", proxyType)
	}
	port, _ := out["port"].(int)
	if port <= 0 {
		return nil, fmt.Errorf("clash proxy %q missing port", proxyType)
	}

	return out, nil
}

// normaliseTransportNetwork maps Clash transport aliases to share-link values
// and defaults a missing network to "tcp" (Clash/Xray default).
func normaliseTransportNetwork(out map[string]any) {
	network, _ := out["network"].(string)
	if network == "" {
		out["network"] = "tcp"
		return
	}

	network = strings.ToLower(network)
	switch network {
	case "raw":
		out["network"] = "tcp"
	case "splithttp":
		out["network"] = "xhttp"
	default:
		out["network"] = network
	}
}

// setPacketEncoding maps Clash packet-encoding / xudp into the share-link
// packetEncoding field (v2rayN: none|packet|xudp).
func setPacketEncoding(out, p map[string]any) {
	if v, ok := p["packet-encoding"]; ok {
		if s, ok := v.(string); ok && s != "" {
			out["packetEncoding"] = s
			return
		}
	}
	if v, ok := p["packetEncoding"]; ok {
		if s, ok := v.(string); ok && s != "" {
			out["packetEncoding"] = s
			return
		}
	}
	// Clash boolean "xudp: true" is the common Reality/VLESS form.
	if toBool(p["xudp"]) {
		out["packetEncoding"] = "xudp"
	}
}

// firstPortFromPorts extracts the first concrete port from a Clash "ports"
// hop list ("443,20000-50000" / "20000-50000" / 443). Returns 0 if unusable.
func firstPortFromPorts(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case string:
		// Take the first comma-separated token; if it is a range, take the start.
		token := strings.TrimSpace(strings.Split(x, ",")[0])
		if token == "" {
			return 0
		}
		if i := strings.IndexByte(token, '-'); i >= 0 {
			token = token[:i]
		}
		return portToInt(token)
	case []any:
		if len(x) == 0 {
			return 0
		}
		return firstPortFromPorts(x[0])
	default:
		return portToInt(v)
	}
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

// setAlpn copies the Clash "alpn" field into out["alpn"] as a comma-joined
// string. In Clash the field is a YAML list (alpn: [h2, http/1.1]); share-link
// specs (v2rayN) expect a single comma-separated value (alpn=h2,http/1.1).
func setAlpn(out, p map[string]any) {
	v, ok := p["alpn"]
	if !ok {
		return
	}
	switch a := v.(type) {
	case string:
		if a != "" {
			out["alpn"] = a
		}
	case []any:
		parts := make([]string, 0, len(a))
		for _, item := range a {
			if s, ok := item.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			out["alpn"] = strings.Join(parts, ",")
		}
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

// firstListOrString returns the string value of v, or the first string element
// of a list value (Clash http-opts/h2-opts use list-valued path/host fields).
func firstListOrString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		if len(x) > 0 {
			if s, ok := x[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

// ssPluginKeyMap maps Clash plugin-opts keys to SIP002 URI plugin parameter
// keys for the supported Shadowsocks plugins. Keys not listed are passed
// through verbatim (lowercased) so unknown plugins still produce a usable link.
var ssPluginKeyMap = map[string]map[string]string{
	"obfs": {
		"mode": "obfs",
		"host": "obfs-host",
		"path": "obfs-uri",
	},
	"obfs-local": {
		"mode": "obfs",
		"host": "obfs-host",
		"path": "obfs-uri",
	},
	"v2ray-plugin": {
		"mode":    "mode",
		"host":    "host",
		"path":    "path",
		"tls":     "tls",
		"mux":     "mux",
		"headers": "headers",
	},
}

// formatSSPluginOpts serialises Clash plugin-opts into the SIP002 plugin string
// ("key=value;key2=value2"), applying per-plugin key translation where known.
// Clash uses "obfs" while SIP002 expects "obfs-local"; that alias is applied here.
func formatSSPluginOpts(plugin string, opts map[string]any) string {
	name := plugin
	if plugin == "obfs" {
		name = "obfs-local"
	}
	keyMap := ssPluginKeyMap[plugin]

	pairs := make([]string, 0, len(opts))
	for _, k := range sortedKeys(opts) {
		outKey := k
		if keyMap != nil {
			if mapped, ok := keyMap[k]; ok {
				outKey = mapped
			}
		}
		pairs = append(pairs, outKey+"="+ssPluginValue(opts[k]))
	}
	return name + ";" + strings.Join(pairs, ";")
}

// ssPluginValue renders a plugin-opts value: bools as 0/1, lists joined by "&".
func ssPluginValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "1"
		}
		return "0"
	case int:
		return strconv.Itoa(x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "&")
	}
	return fmt.Sprintf("%v", v)
}

// httpOptsHost returns the host value from a Clash http-opts/h2-opts block.
// http-opts nests it under headers.Host (a list); h2-opts places host directly
// (also a list). The first string value is used.
func httpOptsHost(h map[string]any) string {
	if headers := getMap(h, "headers"); headers != nil {
		for k, v := range headers {
			if strings.EqualFold(k, "host") {
				if s := firstListOrString(v); s != "" {
					return s
				}
				break
			}
		}
	}
	return firstListOrString(h["host"])
}

// sortedKeys returns the keys of m sorted lexicographically for deterministic
// serialisation of plugin options.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
