package subserver

import (
	"encoding/json"
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
    server: 10.0.0.5
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
	// No TLS layer must be emitted for plaintext ws: neither the key nor any
	// empty security/tlsSettings block anywhere in the outbound.
	_, hasTLS := ss["tlsSettings"]
	assert.False(t, hasTLS)
	assert.NotContains(t, string(x), "tlsSettings")

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

	// Batch contract: unsupported protocols are skipped, not fatal.
	configs = append(configs, json.RawMessage(`{"type":"wireguard","address":"wg.example","port":51820}`))

	out, err := ConvertJSONConfigsToXray(configs)
	require.NoError(t, err)
	assert.Len(t, out, len(configs)-1, "unsupported protocol must be skipped")

	for _, x := range out {
		var ob map[string]any
		require.NoError(t, json.Unmarshal(x, &ob))
		// Every outbound must be a valid Xray node object.
		assert.Contains(t, []any{"vless", "vmess", "trojan", "shadowsocks", "hysteria2"}, ob["protocol"])
		assert.NotEqual(t, "wireguard", ob["protocol"])
	}
}

func mustNormalise(t *testing.T, yamlBody string) json.RawMessage {
	t.Helper()

	configs, err := ExtractClashConfigs([]byte(yamlBody))
	require.NoError(t, err)
	require.Len(t, configs, 1)

	return configs[0]
}

func TestConvertSingleJSONToXray_Hysteria2(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"hysteria2","address":"hy2.example","port":443,"password":"secret","sni":"front.example","obfs":"salamander","obfsPassword":"obfs-secret"}`)
	outbound, err := ConvertSingleJSONToXray(raw)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(outbound, &got))
	require.Equal(t, "hysteria", got["protocol"])
	settings := got["settings"].(map[string]any)
	require.Equal(t, float64(2), settings["version"])
	transport := got["streamSettings"].(map[string]any)
	require.Equal(t, "hysteria", transport["network"])
	hysteriaSettings := transport["hysteriaSettings"].(map[string]any)
	require.Equal(t, float64(2), hysteriaSettings["version"])
	require.Equal(t, "secret", hysteriaSettings["auth"])
	require.Equal(t, "front.example", transport["tlsSettings"].(map[string]any)["serverName"])
	require.NotContains(t, transport, "method")
	require.NotContains(t, hysteriaSettings, "obfs")
	masks := transport["finalmask"].(map[string]any)["udp"].([]any)
	require.Len(t, masks, 1)
	mask := masks[0].(map[string]any)
	require.Equal(t, "salamander", mask["type"])
	require.Equal(t, "obfs-secret", mask["settings"].(map[string]any)["password"])

	withoutObfs, err := ConvertSingleJSONToXray(json.RawMessage(`{"type":"hysteria2","address":"hy2.example","port":443,"password":"secret","obfsPassword":"unused"}`))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(withoutObfs, &got))
	require.NotContains(t, got["streamSettings"].(map[string]any), "finalmask")
}

// TestConvertSingleJSONToXray_NoAllowInsecure pins the Xray-core 26.7.28+
// contract: allowInsecure must NEVER appear in a rendered outbound. The core
// rejects a true value with a removed-feature error that aborts loading of
// the whole profile (happ/v2rayNG popup "The feature allowInsecure has been
// removed"), taking every node down instead of just the affected one.
func TestConvertSingleJSONToXray_NoAllowInsecure(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"hysteria2":  `{"type":"hysteria2","address":"hy2.example","port":443,"password":"secret","sni":"front.example","allowInsecure":true}`,
		"vless_tls":  `{"type":"vless","address":"v.example","port":443,"uuid":"aaaa","security":"tls","sni":"v.example","network":"ws","path":"/x","allowInsecure":true}`,
		"trojan_tls": `{"type":"trojan","address":"t.example","port":443,"password":"pw","tls":"true","allowInsecure":true}`,
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out, err := ConvertSingleJSONToXray(json.RawMessage(raw))
			require.NoError(t, err)
			assert.NotContains(t, string(out), "allowInsecure")
		})
	}
}

// TestConvertSingleJSONToXray_CertPins verifies the allowInsecure replacements
// (pinnedPeerCertSha256 from pcs/pinSHA256, verifyPeerCertByName from vcn)
// reach tlsSettings in the Xray-expected comma-separated string form.
func TestConvertSingleJSONToXray_CertPins(t *testing.T) {
	t.Parallel()

	t.Run("hysteria2_pinSHA256", func(t *testing.T) {
		t.Parallel()

		out, err := ConvertSingleJSONToXray(json.RawMessage(
			`{"type":"hysteria2","address":"hy2.example","port":443,"password":"secret","sni":"front.example","allowInsecure":true,"pinSHA256":"aa,bb"}`,
		))
		require.NoError(t, err)

		var got map[string]any
		require.NoError(t, json.Unmarshal(out, &got))
		ts := got["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)
		assert.Equal(t, "aa,bb", ts["pinnedPeerCertSha256"])
	})

	t.Run("vless_tls_pcs_vcn", func(t *testing.T) {
		t.Parallel()

		out, err := ConvertSingleJSONToXray(json.RawMessage(
			`{"type":"vless","address":"v.example","port":443,"uuid":"aaaa","security":"tls","sni":"v.example","network":"ws","path":"/x","allowInsecure":true,"pcs":"cc","vcn":"v.example"}`,
		))
		require.NoError(t, err)

		var got map[string]any
		require.NoError(t, json.Unmarshal(out, &got))
		ts := got["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)
		assert.Equal(t, "cc", ts["pinnedPeerCertSha256"])
		assert.Equal(t, "v.example", ts["verifyPeerCertByName"])
	})
}

// TestConvertSingleJSONToXray_BoolTLSMarker: flat panel JSON may mark TLS as
// "tls":"true"; it must resolve to Xray security "tls", not the literal
// string (the core rejects Unknown security "true").
func TestConvertSingleJSONToXray_BoolTLSMarker(t *testing.T) {
	t.Parallel()

	out, err := ConvertSingleJSONToXray(json.RawMessage(
		`{"type":"trojan","address":"t.example","port":443,"password":"pw","tls":"true","sni":"t.example","pcs":"aa"}`,
	))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	ss := got["streamSettings"].(map[string]any)
	assert.Equal(t, "tls", ss["security"])
	assert.Equal(t, "aa", ss["tlsSettings"].(map[string]any)["pinnedPeerCertSha256"])
}

// TestConvertJSONConfigsToXray_PlaintextQuarantine: plaintext vless/trojan at
// public addresses are skipped (new Xray cores abort the whole profile on
// them), while TLS/reality nodes and private-address plaintext nodes survive.
func TestConvertJSONConfigsToXray_PlaintextQuarantine(t *testing.T) {
	t.Parallel()

	batch := []json.RawMessage{
		json.RawMessage(`{"type":"vless","address":"nl4.example.sbs","port":80,"uuid":"u1","network":"ws","remark":"NL_4"}`),
		json.RawMessage(`{"type":"trojan","address":"203.0.113.9","port":80,"password":"pw","remark":"TJ_PUB"}`),
		json.RawMessage(`{"type":"vless","address":"10.0.0.5","port":2082,"uuid":"u2","network":"tcp","remark":"LAB"}`),
		json.RawMessage(`{"type":"vless","address":"ru1.example.sbs","port":443,"uuid":"u3","security":"reality","pbk":"k","sid":"d1","remark":"RU_1"}`),
	}

	outs, err := ConvertJSONConfigsToXray(batch)
	require.NoError(t, err)
	require.Len(t, outs, 2)

	var tags []string
	for _, o := range outs {
		var m map[string]any
		require.NoError(t, json.Unmarshal(o, &m))
		tags = append(tags, m["tag"].(string))
	}
	assert.Equal(t, []string{"LAB", "RU_1"}, tags)
}

// TestConvertSingleJSONToXray_PlaintextPublicErrors: the single-node entry
// point surfaces the rejection instead of emitting a config new cores refuse.
func TestConvertSingleJSONToXray_PlaintextPublicErrors(t *testing.T) {
	t.Parallel()

	_, err := ConvertSingleJSONToXray(json.RawMessage(
		`{"type":"vless","address":"nl4.example.sbs","port":80,"uuid":"u1"}`,
	))
	require.ErrorContains(t, err, "without transport security")
}
