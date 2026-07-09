package subserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertSingleJSONToXray_VLESSRealityTCP(t *testing.T) {
	t.Parallel()
	raw := mustNormalise(t, `proxies:
  - client-fingerprint: chrome
    encryption: none
    flow: xtls-rprx-vision
    name: DE_7
    network: tcp
    port: "443"
    reality-opts:
      public-key: p2kybyvJhP9F98fLS4TcL8nNcFE-MdhfGoVBlGkdHUs
      short-id: d1
    server: grm.janaj.space
    servername: api-maps.yandex.ru
    tls: true
    type: vless
    uuid: 8a4d32e8-ab02-4398-84a7-8d7672426ad6
`)
	x, err := ConvertSingleJSONToXray(raw)
	require.NoError(t, err)

	var ob map[string]any
	require.NoError(t, json.Unmarshal(x, &ob))
	assert.Equal(t, "vless", ob["protocol"])
	assert.Equal(t, "DE_7", ob["tag"])

	settings := ob["settings"].(map[string]any)
	vnext := settings["vnext"].([]any)[0].(map[string]any)
	assert.Equal(t, "grm.janaj.space", vnext["address"])
	assert.Equal(t, float64(443), vnext["port"])
	user := vnext["users"].([]any)[0].(map[string]any)
	assert.Equal(t, "8a4d32e8-ab02-4398-84a7-8d7672426ad6", user["id"])
	assert.Equal(t, "none", user["encryption"])
	assert.Equal(t, "xtls-rprx-vision", user["flow"])

	ss := ob["streamSettings"].(map[string]any)
	assert.Equal(t, "tcp", ss["network"])
	assert.Equal(t, "reality", ss["security"])
	rs := ss["realitySettings"].(map[string]any)
	assert.Equal(t, "api-maps.yandex.ru", rs["serverName"])
	assert.Equal(t, "chrome", rs["fingerprint"])
	assert.Equal(t, "p2kybyvJhP9F98fLS4TcL8nNcFE-MdhfGoVBlGkdHUs", rs["publicKey"])
	assert.Equal(t, "d1", rs["shortId"])
	assert.Equal(t, "/", rs["spiderX"])
}

func TestConvertSingleJSONToXray_VLESSWSTLS(t *testing.T) {
	t.Parallel()
	raw := mustNormalise(t, `proxies:
  - client-fingerprint: chrome
    encryption: none
    name: SE_28
    network: ws
    port: "443"
    server: 104.17.180.39
    servername: t1s1.rittbo.kdns.fr
    tls: true
    type: vless
    uuid: eeb6823c-b926-4ea2-866a-5542edd26e59
    ws-opts:
      headers:
        Host: t1s1.rittbo.kdns.fr
      path: /@NebulaVPNx
`)
	x, err := ConvertSingleJSONToXray(raw)
	require.NoError(t, err)

	var ob map[string]any
	require.NoError(t, json.Unmarshal(x, &ob))
	ss := ob["streamSettings"].(map[string]any)
	assert.Equal(t, "ws", ss["network"])
	assert.Equal(t, "tls", ss["security"])

	ts := ss["tlsSettings"].(map[string]any)
	assert.Equal(t, "t1s1.rittbo.kdns.fr", ts["serverName"])
	assert.Equal(t, "chrome", ts["fingerprint"])

	ws := ss["wsSettings"].(map[string]any)
	assert.Equal(t, "/@NebulaVPNx", ws["path"])
	headers := ws["headers"].(map[string]any)
	assert.Equal(t, "t1s1.rittbo.kdns.fr", headers["Host"])
}

func TestConvertSingleJSONToXray_VLESSWSNoTLS(t *testing.T) {
	t.Parallel()
	raw := mustNormalise(t, `proxies:
  - encryption: none
    name: SE_27
    network: ws
    port: "8880"
    server: 162.159.156.214
    type: vless
    uuid: 25322a43-4ef3-45cc-9e96-db44bcccd7be
    ws-opts:
      headers:
        Host: purple-disk-f68d.14-360.workers.dev
      path: /pyip=ProxyIP.US.CMLiussss.net
`)
	x, err := ConvertSingleJSONToXray(raw)
	require.NoError(t, err)

	var ob map[string]any
	require.NoError(t, json.Unmarshal(x, &ob))
	ss := ob["streamSettings"].(map[string]any)
	assert.Equal(t, "ws", ss["network"])
	assert.Equal(t, "none", ss["security"])
	// No TLS layer must be emitted for plaintext ws.
	_, hasTLS := ss["tlsSettings"]
	assert.False(t, hasTLS)

	ws := ss["wsSettings"].(map[string]any)
	assert.Equal(t, "/pyip=ProxyIP.US.CMLiussss.net", ws["path"])
	headers := ws["headers"].(map[string]any)
	assert.Equal(t, "purple-disk-f68d.14-360.workers.dev", headers["Host"])
}

func TestConvertSingleJSONToXray_VLESSWSAlpn(t *testing.T) {
	t.Parallel()
	raw := mustNormalise(t, `proxies:
  - alpn:
      - http/1.1
    client-fingerprint: chrome
    encryption: none
    name: SE_29
    network: ws
    port: "2083"
    server: 104.16.43.192
    servername: ez-2cB1f5.sabZIpolObAMAHi9.WORkerS.deV
    tls: true
    type: vless
    uuid: 88e32d76-b49f-44ee-a90a-1ad01d542d55
    ws-opts:
      headers:
        Host: ez-2cb1f5.sabzipolobamahi9.workers.dev
      path: /x
`)
	x, err := ConvertSingleJSONToXray(raw)
	require.NoError(t, err)

	var ob map[string]any
	require.NoError(t, json.Unmarshal(x, &ob))
	ss := ob["streamSettings"].(map[string]any)
	ts := ss["tlsSettings"].(map[string]any)
	alpn, ok := ts["alpn"].([]any)
	require.True(t, ok)
	assert.Equal(t, []any{"http/1.1"}, alpn)
}

func TestConvertSingleJSONToXray_SS(t *testing.T) {
	t.Parallel()
	raw := mustNormalise(t, `proxies:
  - cipher: aes-256-gcm
    name: SA_2
    password: g5MeD6Ft3CWlJId
    port: "5004"
    server: 156.244.8.155
    type: ss
`)
	x, err := ConvertSingleJSONToXray(raw)
	require.NoError(t, err)

	var ob map[string]any
	require.NoError(t, json.Unmarshal(x, &ob))
	assert.Equal(t, "shadowsocks", ob["protocol"])
	settings := ob["settings"].(map[string]any)
	server := settings["servers"].([]any)[0].(map[string]any)
	assert.Equal(t, "156.244.8.155", server["address"])
	assert.Equal(t, float64(5004), server["port"])
	assert.Equal(t, "aes-256-gcm", server["method"])
	assert.Equal(t, "g5MeD6Ft3CWlJId", server["password"])

	ss := ob["streamSettings"].(map[string]any)
	assert.Equal(t, "tcp", ss["network"])
}

func TestConvertSingleJSONToXray_RawNormalisesToTCP(t *testing.T) {
	t.Parallel()
	raw := mustNormalise(t, `proxies:
  - client-fingerprint: chrome
    encryption: none
    name: DE_6
    network: raw
    port: "8443"
    reality-opts:
      public-key: Crjd0I6hcasRXZcfNf7cs-YtJLpQ8u-t1CbwHIDAciU
      short-id: 478dc6
    server: 178.215.238.148
    servername: eh.vk.com
    tls: true
    type: vless
    uuid: 21c1132b-068c-46fb-880e-3268f6a30e7f
`)
	x, err := ConvertSingleJSONToXray(raw)
	require.NoError(t, err)

	var ob map[string]any
	require.NoError(t, json.Unmarshal(x, &ob))
	ss := ob["streamSettings"].(map[string]any)
	assert.Equal(t, "tcp", ss["network"])
	assert.Equal(t, "reality", ss["security"])
}

func TestConvertJSONConfigsToXray_Batch(t *testing.T) {
	t.Parallel()
	configs, err := ExtractClashConfigs([]byte(clashVlessRealityYAML))
	require.NoError(t, err)

	out, err := ConvertJSONConfigsToXray(configs)
	require.NoError(t, err)
	assert.Len(t, out, len(configs))

	for _, x := range out {
		var ob map[string]any
		require.NoError(t, json.Unmarshal(x, &ob))
		// Every outbound must be a valid Xray node object.
		assert.Contains(t, []any{"vless", "vmess", "trojan", "shadowsocks", "hysteria2"}, ob["protocol"])
	}
}

func mustNormalise(t *testing.T, yamlBody string) json.RawMessage {
	t.Helper()
	configs, err := ExtractClashConfigs([]byte(yamlBody))
	require.NoError(t, err)
	require.Len(t, configs, 1)
	return configs[0]
}

// Ensure the generated JSON round-trips through encoding/json without issue
// and contains no empty security/tlsSettings blocks.
func TestConvertSingleJSONToXray_NoEmptySecurityBlocks(t *testing.T) {
	t.Parallel()
	raw := mustNormalise(t, `proxies:
  - encryption: none
    name: SE_39
    network: ws
    port: "80"
    server: 104.24.38.86
    servername: zeus-panel-8hkm7j.zeus-zew7l5.workers.dev
    type: vless
    uuid: 77b90f21-d414-43ae-a81d-ab6769649fc4
    ws-opts:
      headers:
        Host: zeus-panel-8hkm7j.zeus-zew7l5.workers.dev
      path: /x
`)
	x, err := ConvertSingleJSONToXray(raw)
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(x), "tlsSettings"))
}
