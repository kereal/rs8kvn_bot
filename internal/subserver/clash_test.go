package subserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const clashVlessRealityYAML = `proxies:
  - client-fingerprint: firefox
    encryption: none
    flow: xtls-rprx-vision
    name: "\U0001F1F1\U0001F1F9LT_18|2.5MB/s|34%"
    network: tcp
    port: "8443"
    reality-opts:
      public-key: L3X1eh1Jq_6PKJ6LlwjgiWq0XNaDOqCVKgIElJ5nkVA
      short-id: 2cfb5a0ae8ab0cb0
    server: 45.194.10.152
    servername: storage.yandex.net
    tls: true
    type: vless
    udp: true
    uuid: 3e7cede4-721a-4807-b0a2-5fe6586af907
    xudp: true
  - client-fingerprint: qq
    encryption: none
    grpc-opts:
      grpc-service-name: admin
    name: "\U0001F1F3\U0001F1F1NL_5|2.5MB/s|0%"
    network: grpc
    port: "3443"
    reality-opts:
      public-key: iVXjg_bUPNovcw-goWQyAFfoNg4zYojkbP2h2urOY1o
      short-id: 43dcff53849b81e6
    server: 89.105.205.116
    servername: ozon.ru
    tls: true
    type: vless
    udp: true
    uuid: 497631c4-a1a4-45ff-a86a-f42873473bb6
    xudp: true
  - client-fingerprint: firefox
    flow: xtls-rprx-vision
    name: "LT_33"
    network: tcp
    port: "8443"
    reality-opts:
      public-key: L3X1eh1Jq_6PKJ6LlwjgiWq0XNaDOqCVKgIElJ5nkVA
      short-id: 2cfb5a0ae8ab0cb0
    server: 45.194.10.102
    servername: storage.yandex.net
    tls: true
    type: vless
    udp: true
    uuid: 3e7cede4-721a-4807-b0a2-5fe6586af907
    xudp: true
  - client-fingerprint: firefox
    encryption: none
    grpc-opts:
      grpc-service-name: ns
    name: "AE_2"
    network: grpc
    port: "9443"
    reality-opts:
      public-key: QZz4tjRkxYsgTZBlRALOQU4O5YnAGtSGkXF8OSX11m8
      short-id: ""
    server: 5.188.115.226
    servername: id.pervye.ru
    tls: true
    type: vless
    udp: true
    uuid: 3483aa7a-8a7d-4561-8f77-4fbf7a0b1033
    xudp: true
`

const clashMixedProtocolsYAML = `proxies:
  - name: "vmess-tcp"
    type: vmess
    server: 1.2.3.4
    port: 443
    uuid: aaaa-bbbb
    cipher: auto
    network: tcp
    tls: true
    servername: example.com
  - name: "trojan-ws"
    type: trojan
    server: 5.6.7.8
    port: 443
    password: trojpass
    sni: sni.example.com
    network: ws
    ws-opts:
      path: /ws
      headers:
        Host: ws.example.com
  - name: "ss-aes"
    type: shadowsocks
    server: 9.10.11.12
    port: 8388
    cipher: aes-256-gcm
    password: sspass
  - name: "hy2-node"
    type: hysteria2
    server: 13.14.15.16
    port: 443
    password: hy2pass
    sni: hy2.example.com
    skip-cert-verify: true
  - name: "tuic-v5"
    type: tuic
    server: 17.18.19.20
    port: 443
    uuid: tuic-uuid
    password: tuicpass
    sni: tuic.example.com
`

func TestExtractClashConfigs_VlessReality(t *testing.T) {
	t.Parallel()

	configs, err := ExtractClashConfigs([]byte(clashVlessRealityYAML))
	require.NoError(t, err)
	assert.Len(t, configs, 4)

	// First proxy: tcp + reality.
	cfg0, err := toServerConfig(configs[0])
	require.NoError(t, err)
	assert.Equal(t, "vless", cfg0.Type)
	assert.Equal(t, "45.194.10.152", cfg0.Address)
	assert.Equal(t, 8443, cfg0.Port)
	assert.Equal(t, "3e7cede4-721a-4807-b0a2-5fe6586af907", cfg0.UUID)
	assert.Equal(t, "xtls-rprx-vision", cfg0.Flow)
	assert.Equal(t, "none", cfg0.Encryption)
	assert.Equal(t, "tcp", cfg0.Network)
	assert.Equal(t, "storage.yandex.net", cfg0.SNI)
	assert.Equal(t, "firefox", cfg0.Fingerprint)
	assert.Equal(t, "reality", cfg0.Security)
	assert.Equal(t, "L3X1eh1Jq_6PKJ6LlwjgiWq0XNaDOqCVKgIElJ5nkVA", cfg0.PublicKey)
	assert.Equal(t, "2cfb5a0ae8ab0cb0", cfg0.ShortID)
	assert.Equal(t, "\U0001F1F1\U0001F1F9LT_18|2.5MB/s|34%", cfg0.Remark)

	// Second proxy: grpc + reality.
	cfg1, err := toServerConfig(configs[1])
	require.NoError(t, err)
	assert.Equal(t, "grpc", cfg1.Network)
	assert.Equal(t, "admin", cfg1.Path)
	assert.Equal(t, "reality", cfg1.Security)
	assert.Equal(t, "ozon.ru", cfg1.SNI)

	// Third proxy: encryption absent.
	cfg2, err := toServerConfig(configs[2])
	require.NoError(t, err)
	assert.Empty(t, cfg2.Encryption)
	assert.Equal(t, "reality", cfg2.Security)

	// Fourth proxy: empty short-id.
	cfg3, err := toServerConfig(configs[3])
	require.NoError(t, err)
	assert.Empty(t, cfg3.ShortID)
	assert.Equal(t, "grpc", cfg3.Network)
	assert.Equal(t, "ns", cfg3.Path)

	// All configs must convert to valid vless share links.
	for i, raw := range configs {
		link, err := ConvertSingleJSONToLink(raw)
		require.NoError(t, err, "config %d", i)
		assert.True(t, strings.HasPrefix(link, "vless://"), "config %d: %s", i, link)
		assert.Contains(t, link, "security=reality")
	}
}

func TestExtractClashConfigs_MixedProtocols(t *testing.T) {
	t.Parallel()

	configs, err := ExtractClashConfigs([]byte(clashMixedProtocolsYAML))
	require.NoError(t, err)
	assert.Len(t, configs, 5)

	// VMess.
	cfg0, err := toServerConfig(configs[0])
	require.NoError(t, err)
	assert.Equal(t, "vmess", cfg0.Type)
	assert.Equal(t, "1.2.3.4", cfg0.Address)
	assert.Equal(t, 443, cfg0.Port)
	assert.Equal(t, "aaaa-bbbb", cfg0.UUID)
	assert.Equal(t, "auto", cfg0.Scy)
	assert.Equal(t, "tcp", cfg0.Network)
	assert.Equal(t, "example.com", cfg0.SNI)

	// Trojan + ws.
	cfg1, err := toServerConfig(configs[1])
	require.NoError(t, err)
	assert.Equal(t, "trojan", cfg1.Type)
	assert.Equal(t, "trojpass", cfg1.Password)
	assert.Equal(t, "sni.example.com", cfg1.SNI)
	assert.Equal(t, "ws", cfg1.Network)
	assert.Equal(t, "/ws", cfg1.Path)
	assert.Equal(t, "ws.example.com", cfg1.Host)

	// Shadowsocks.
	cfg2, err := toServerConfig(configs[2])
	require.NoError(t, err)
	assert.Equal(t, "shadowsocks", cfg2.Type)
	assert.Equal(t, "aes-256-gcm", cfg2.Method)
	assert.Equal(t, "sspass", cfg2.Password)

	// Hysteria2.
	cfg3, err := toServerConfig(configs[3])
	require.NoError(t, err)
	assert.Equal(t, "hysteria2", cfg3.Type)
	assert.Equal(t, "hy2pass", cfg3.Password)
	assert.Equal(t, "hy2.example.com", cfg3.SNI)
	assert.True(t, cfg3.AllowInsecure)

	// TUIC.
	cfg4, err := toServerConfig(configs[4])
	require.NoError(t, err)
	assert.Equal(t, "tuic", cfg4.Type)
	assert.Equal(t, "tuic-uuid", cfg4.UUID)
	assert.Equal(t, "tuicpass", cfg4.Password)
	assert.Equal(t, "tuic.example.com", cfg4.SNI)

	// All must convert to share links.
	for i, raw := range configs {
		link, err := ConvertSingleJSONToLink(raw)
		require.NoError(t, err, "config %d", i)
		assert.NotEmpty(t, link, "config %d", i)
	}
}

func TestExtractClashConfigs_Empty(t *testing.T) {
	t.Parallel()

	_, err := ExtractClashConfigs([]byte("proxies: []"))
	assert.Error(t, err)
}

func TestExtractClashConfigs_NoProxies(t *testing.T) {
	t.Parallel()

	_, err := ExtractClashConfigs([]byte("mixed-port: 7890\nmode: rule\n"))
	assert.Error(t, err)
}

func TestExtractClashConfigs_InvalidYAML(t *testing.T) {
	t.Parallel()

	_, err := ExtractClashConfigs([]byte("proxies:\n  - [invalid"))
	assert.Error(t, err)
}

func TestExtractClashConfigs_UnsupportedType(t *testing.T) {
	t.Parallel()

	// Unsupported type should be skipped, not error the whole batch.
	yaml := "proxies:\n  - type: http\n    server: 1.2.3.4\n    port: 80\n  - type: vless\n    server: 5.6.7.8\n    port: 443\n    uuid: test-uuid\n"
	configs, err := ExtractClashConfigs([]byte(yaml))
	require.NoError(t, err)
	assert.Len(t, configs, 1)
}

func TestNormaliseClashProxy_VMessAlterId(t *testing.T) {
	t.Parallel()

	yaml := "proxies:\n  - type: vmess\n    server: 1.2.3.4\n    port: 443\n    uuid: aaaa-bbbb\n    alterId: 2\n    cipher: auto\n    servername: example.com\n"
	configs, err := ExtractClashConfigs([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, configs, 1)

	cfg, err := toServerConfig(configs[0])
	require.NoError(t, err)
	assert.Equal(t, "2", cfg.Aid)
}

// TestExtractClashConfigs_ALPNList verifies that the Clash "alpn" list field is
// serialised as a comma-joined value in the resulting share link (v2rayN spec).
func TestExtractClashConfigs_ALPNList(t *testing.T) {
	t.Parallel()

	yaml := "proxies:\n  - name: vless-alpn\n    type: vless\n    server: 1.2.3.4\n    port: 443\n    uuid: aaaa-bbbb\n    network: ws\n    tls: true\n    alpn:\n      - h2\n      - http/1.1\n"
	configs, err := ExtractClashConfigs([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, configs, 1)

	cfg, err := toServerConfig(configs[0])
	require.NoError(t, err)
	assert.Equal(t, "h2,http/1.1", cfg.Alpn)

	link, err := ConvertSingleJSONToLink(configs[0])
	require.NoError(t, err)
	assert.Contains(t, link, "alpn=h2%2Chttp%2F1.1")
}

// TestExtractClashConfigs_VMessHTTPObfs verifies vmess header type and http-opts
// host/path are captured (previously dropped).
func TestExtractClashConfigs_VMessHTTPObfs(t *testing.T) {
	t.Parallel()

	yaml := "proxies:\n  - name: vmess-http\n    type: vmess\n    server: 1.2.3.4\n    port: 443\n    uuid: aaaa-bbbb\n    cipher: auto\n    tls: true\n    network: http\n    http-opts:\n      path:\n        - /a\n        - /b\n      headers:\n        Host:\n          - fake.com\n"
	configs, err := ExtractClashConfigs([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, configs, 1)

	cfg, err := toServerConfig(configs[0])
	require.NoError(t, err)
	assert.Equal(t, "http", cfg.HeaderType)
	assert.Equal(t, "/a", cfg.Path)
	assert.Equal(t, "fake.com", cfg.Host)

	link, err := ConvertSingleJSONToLink(configs[0])
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(link, "vmess://"))
}

// TestExtractClashConfigs_SSPlugin verifies Shadowsocks simple-obfs plugin is
// emitted as a SIP002 plugin parameter (Clash "obfs" -> "obfs-local").
func TestExtractClashConfigs_SSPlugin(t *testing.T) {
	t.Parallel()

	yaml := "proxies:\n  - name: ss-obfs\n    type: shadowsocks\n    server: 1.2.3.4\n    port: 8388\n    cipher: aes-256-gcm\n    password: sspass\n    plugin: obfs\n    plugin-opts:\n      mode: http\n      host: bing.com\n"
	configs, err := ExtractClashConfigs([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, configs, 1)

	cfg, err := toServerConfig(configs[0])
	require.NoError(t, err)
	assert.Equal(t, "obfs-local;obfs-host=bing.com;obfs=http", cfg.PluginOpts)

	link, err := ConvertSingleJSONToLink(configs[0])
	require.NoError(t, err)
	assert.Contains(t, link, "plugin=obfs-local")
	assert.Contains(t, link, "obfs%3Dhttp")
	assert.Contains(t, link, "obfs-host%3Dbing.com")
}

// TestExtractClashConfigs_VLESSXHTTP verifies the XTLS SplitHTTP (xhttp)
// transport is converted to type=xhttp with path/host/mode in the link.
func TestExtractClashConfigs_VLESSXHTTP(t *testing.T) {
	t.Parallel()

	yaml := "proxies:\n  - name: vless-xhttp\n    type: vless\n    server: 1.2.3.4\n    port: 443\n    uuid: aaaa-bbbb\n    flow: xtls-rprx-vision\n    network: xhttp\n    tls: true\n    alpn:\n      - h2\n    reality-opts:\n      public-key: pk\n      short-id: sid\n    xhttp-opts:\n      path: /x\n      host: x.example.com\n      mode: stream-up\n"
	configs, err := ExtractClashConfigs([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, configs, 1)

	cfg, err := toServerConfig(configs[0])
	require.NoError(t, err)
	assert.Equal(t, "xhttp", cfg.Network)
	assert.Equal(t, "/x", cfg.Path)
	assert.Equal(t, "x.example.com", cfg.Host)
	assert.Equal(t, "stream-up", cfg.Mode)
	assert.Equal(t, "h2", cfg.Alpn)

	link, err := ConvertSingleJSONToLink(configs[0])
	require.NoError(t, err)
	assert.Contains(t, link, "type=xhttp")
	assert.Contains(t, link, "path=%2Fx")
	assert.Contains(t, link, "host=x.example.com")
	assert.Contains(t, link, "mode=stream-up")
	assert.Contains(t, link, "alpn=h2")
}

// TestExtractClashConfigs_VLESSSplitHTTPAlias verifies legacy "splithttp"
// network normalises to "xhttp" in the generated link.
func TestExtractClashConfigs_VLESSSplitHTTPAlias(t *testing.T) {
	t.Parallel()

	yaml := "proxies:\n  - name: vless-splithttp\n    type: vless\n    server: 1.2.3.4\n    port: 443\n    uuid: aaaa-bbbb\n    network: splithttp\n    tls: true\n    xhttp-opts:\n      path: /s\n"
	configs, err := ExtractClashConfigs([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, configs, 1)

	link, err := ConvertSingleJSONToLink(configs[0])
	require.NoError(t, err)
	assert.Contains(t, link, "type=xhttp")
	assert.Contains(t, link, "path=%2Fs")
}

// TestExtractClashConfigs_VLESSSkipCertVerify verifies that Clash
// skip-cert-verify/allowInsecure propagates to allowInsecure=1 in the VLESS
// share link (v2rayN equivalent).
func TestExtractClashConfigs_VLESSSkipCertVerify(t *testing.T) {
	t.Parallel()

	yaml := "proxies:\n  - name: vless-skip\n    type: vless\n    server: 1.2.3.4\n    port: 443\n    uuid: aaaa-bbbb\n    tls: true\n    skip-cert-verify: true\n    network: ws\n    ws-opts:\n      path: /ws\n      headers:\n        Host: example.com\n"
	configs, err := ExtractClashConfigs([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, configs, 1)

	cfg, err := toServerConfig(configs[0])
	require.NoError(t, err)
	assert.True(t, cfg.AllowInsecure)

	link, err := ConvertSingleJSONToLink(configs[0])
	require.NoError(t, err)
	assert.Contains(t, link, "allowInsecure=1")
}

// TestExtractClashConfigs_VLESSHTTPOpts verifies VLESS with network: http
// correctly extracts path and host from http-opts.
func TestExtractClashConfigs_VLESSHTTPOpts(t *testing.T) {
	t.Parallel()

	yaml := "proxies:\n  - name: vless-http\n    type: vless\n    server: 1.2.3.4\n    port: 443\n    uuid: aaaa-bbbb\n    network: http\n    tls: true\n    alpn:\n      - h2\n    http-opts:\n      path:\n        - /a\n      headers:\n        Host:\n          - fake.com\n"
	configs, err := ExtractClashConfigs([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, configs, 1)

	cfg, err := toServerConfig(configs[0])
	require.NoError(t, err)
	assert.Equal(t, "http", cfg.Network)
	assert.Equal(t, "/a", cfg.Path)
	assert.Equal(t, "fake.com", cfg.Host)
	assert.Equal(t, "h2", cfg.Alpn)

	link, err := ConvertSingleJSONToLink(configs[0])
	require.NoError(t, err)
	assert.Contains(t, link, "type=http")
	assert.Contains(t, link, "path=%2Fa")
	assert.Contains(t, link, "host=fake.com")
}

// TestExtractClashConfigs_VLESSH2Opts verifies VLESS with network: h2
// correctly extracts path and host from h2-opts.
func TestExtractClashConfigs_VLESSH2Opts(t *testing.T) {
	t.Parallel()

	yaml := "proxies:\n  - name: vless-h2\n    type: vless\n    server: 1.2.3.4\n    port: 443\n    uuid: aaaa-bbbb\n    network: h2\n    tls: true\n    alpn:\n      - h2\n    h2-opts:\n      path:\n        - /h2\n      headers:\n        Host:\n          - h2.example.com\n"
	configs, err := ExtractClashConfigs([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, configs, 1)

	cfg, err := toServerConfig(configs[0])
	require.NoError(t, err)
	assert.Equal(t, "h2", cfg.Network)
	assert.Equal(t, "/h2", cfg.Path)
	assert.Equal(t, "h2.example.com", cfg.Host)
	assert.Equal(t, "h2", cfg.Alpn)

	link, err := ConvertSingleJSONToLink(configs[0])
	require.NoError(t, err)
	assert.Contains(t, link, "type=h2")
	assert.Contains(t, link, "path=%2Fh2")
	assert.Contains(t, link, "host=h2.example.com")
}

// TestExtractClashConfigs_TrojanTLS verifies Trojan with tls: true outputs
// "security": "tls" (matching 3x-ui flat format), not "tls": "tls".
func TestExtractClashConfigs_TrojanTLS(t *testing.T) {
	t.Parallel()

	yaml := "proxies:\n  - name: trojan-tls\n    type: trojan\n    server: 5.6.7.8\n    port: 443\n    password: trojpass\n    sni: sni.example.com\n    tls: true\n    network: ws\n    ws-opts:\n      path: /ws\n      headers:\n        Host: ws.example.com\n"
	configs, err := ExtractClashConfigs([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, configs, 1)

	cfg, err := toServerConfig(configs[0])
	require.NoError(t, err)
	assert.Equal(t, "trojan", cfg.Type)
	assert.Equal(t, "trojpass", cfg.Password)
	assert.Equal(t, "ws.example.com", cfg.Host)
	assert.Equal(t, "/ws", cfg.Path)

	// Must use "security" field (3x-ui flat format), not "tls".
	assert.Equal(t, "tls", cfg.Security)

	link, err := ConvertSingleJSONToLink(configs[0])
	require.NoError(t, err)
	assert.Contains(t, link, "trojan://trojpass@5.6.7.8:443")
	assert.Contains(t, link, "security=tls")
	assert.Contains(t, link, "sni=sni.example.com")
}

// Suppress unused import warning if json is not directly referenced.
var _ = json.RawMessage(nil)
