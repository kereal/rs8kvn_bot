package subserver

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertJSONToShareLinks_VLESS(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"type": "vless",
		"address": "server.example.com",
		"port": 443,
		"uuid": "b0504246-8d12-4f96-a1c4-e6e9c65a7d70",
		"encryption": "none",
		"flow": "xtls-rprx-vision",
		"security": "tls",
		"sni": "proxy.example.com",
		"network": "tcp",
		"remark": "VIP"
	}`)

	links, err := ConvertJSONToShareLinks(body)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Contains(t, links[0], "vless://b0504246-8d12-4f96-a1c4-e6e9c65a7d70@server.example.com:443")
	assert.Contains(t, links[0], "flow=xtls-rprx-vision")
	assert.Contains(t, links[0], "security=tls")
	assert.Contains(t, links[0], "sni=proxy.example.com")
	assert.Contains(t, links[0], "encryption=none")
	assert.Contains(t, links[0], "#VIP")
}

func TestConvertJSONToShareLinks_VMess(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"type": "vmess",
		"address": "vmess.example.com",
		"port": 8443,
		"uuid": "cf7d6e8a-e7f3-4c96-b1a2-d8e9f0a1b2c3",
		"network": "ws",
		"host": "cdn.example.com",
		"path": "/ws",
		"tls": "tls",
		"remark": "VMess-Node"
	}`)

	links, err := ConvertJSONToShareLinks(body)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Contains(t, links[0], "vmess://")
}

func TestConvertJSONToShareLinks_Trojan(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"type": "trojan",
		"address": "trojan.example.com",
		"port": 443,
		"password": "trojan-pass",
		"sni": "real.example.com",
		"remark": "Trojan"
	}`)

	links, err := ConvertJSONToShareLinks(body)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Contains(t, links[0], "trojan://trojan-pass@trojan.example.com:443")
	assert.Contains(t, links[0], "sni=real.example.com")
	assert.Contains(t, links[0], "#Trojan")
}

func TestConvertJSONToShareLinks_Shadowsocks(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"type": "ss",
		"address": "ss.example.com",
		"port": 8388,
		"method": "chacha20-ietf-poly1305",
		"password": "ss-password",
		"remark": "SS-Node"
	}`)

	links, err := ConvertJSONToShareLinks(body)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Contains(t, links[0], "ss://")
}

func TestConvertJSONToShareLinks_JSONArray(t *testing.T) {
	t.Parallel()

	body := []byte(`[
		{
			"type": "vless",
			"address": "s1.example.com",
			"port": 443,
			"uuid": "11111111-1111-1111-1111-111111111111",
			"encryption": "none",
			"remark": "Server1"
		},
		{
			"type": "trojan",
			"address": "s2.example.com",
			"port": 8443,
			"password": "pass2",
			"remark": "Server2"
		}
	]`)

	links, err := ConvertJSONToShareLinks(body)
	require.NoError(t, err)
	require.Len(t, links, 2)
	assert.Contains(t, links[0], "vless://")
	assert.Contains(t, links[1], "trojan://")
}

func TestConvertJSONToShareLinks_TrojanTLS(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"type": "trojan",
		"address": "trojan.example.com",
		"port": 443,
		"password": "trojan-pass",
		"security": "tls",
		"sni": "real.example.com",
		"remark": "Trojan-TLS"
	}`)

	links, err := ConvertJSONToShareLinks(body)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Contains(t, links[0], "trojan://trojan-pass@trojan.example.com:443")
	assert.Contains(t, links[0], "security=tls")
	assert.Contains(t, links[0], "sni=real.example.com")
	assert.Contains(t, links[0], "#Trojan-TLS")
}

func TestConvertSingleJSONToLink_Hysteria_IPv6(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "hysteria2",
		"address": "::1",
		"port": 443,
		"password": "hy2-pass",
		"remark": "Hy2-IPv6"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "hysteria2://hy2-pass@[::1]:443")
	assert.Contains(t, link, "#Hy2-IPv6")
}

func TestConvertJSONToShareLinks_InvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := ConvertJSONToShareLinks([]byte("not-json"))
	assert.Error(t, err)
}

func TestConvertJSONToShareLinks_UnsupportedType(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"type": "unknown-proto",
		"address": "x.example.com",
		"port": 1234
	}`)

	links, err := ConvertJSONToShareLinks(body)
	require.NoError(t, err)
	assert.Empty(t, links)
}
