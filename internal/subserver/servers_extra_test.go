package subserver

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== ExtractJSONConfigs Tests ====================

func TestExtractJSONConfigs_Array(t *testing.T) {
	t.Parallel()

	body := []byte(`[{"type":"vless","address":"s1.example.com","port":443},{"type":"trojan","address":"s2.example.com","port":8443}]`)
	configs, err := ExtractJSONConfigs(body)
	require.NoError(t, err)
	assert.Len(t, configs, 2)
}

func TestExtractJSONConfigs_SingleObject(t *testing.T) {
	t.Parallel()

	body := []byte(`{"type":"vless","address":"s1.example.com","port":443}`)
	configs, err := ExtractJSONConfigs(body)
	require.NoError(t, err)
	assert.Len(t, configs, 1)
}

func TestExtractJSONConfigs_LeadingWhitespaceObject(t *testing.T) {
	t.Parallel()

	// DetectFormat trims the body before JSON validation, so a single JSON
	// object with leading whitespace must parse the same way (regression: the
	// old body[0] == '{' fallback lost the source and the subscription 404'd).
	body := []byte("  \n\t{\"type\":\"vless\",\"address\":\"s1.example.com\",\"port\":443}")
	configs, err := ExtractJSONConfigs(body)
	require.NoError(t, err)
	require.Len(t, configs, 1)
}

func TestExtractJSONConfigs_InvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := ExtractJSONConfigs([]byte("not-json"))
	assert.Error(t, err)
}

// ==================== ConvertSingleJSONToLink Tests ====================

func TestConvertSingleJSONToLink_VLESS(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "vless",
		"address": "vless.example.com",
		"port": 443,
		"uuid": "b0504246-8d12-4f96-a1c4-e6e9c65a7d70",
		"encryption": "none",
		"flow": "xtls-rprx-vision",
		"security": "reality",
		"sni": "reality.example.com",
		"fp": "chrome",
		"pbk": "some-public-key",
		"sid": "some-short-id",
		"network": "tcp",
		"remark": "VLESS-Node"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "vless://b0504246-8d12-4f96-a1c4-e6e9c65a7d70@vless.example.com:443")
	assert.Contains(t, link, "flow=xtls-rprx-vision")
	assert.Contains(t, link, "security=reality")
	assert.Contains(t, link, "sni=reality.example.com")
	assert.Contains(t, link, "fp=chrome")
	assert.Contains(t, link, "pbk=some-public-key")
	assert.Contains(t, link, "sid=some-short-id")
	assert.Contains(t, link, "#VLESS-Node")
}

func TestConvertSingleJSONToLink_Trojan(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "trojan",
		"address": "trojan.example.com",
		"port": 443,
		"password": "my-secret",
		"security": "tls",
		"sni": "real.example.com",
		"remark": "Trojan-Node"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "trojan://my-secret@trojan.example.com:443")
	assert.Contains(t, link, "security=tls")
	assert.Contains(t, link, "sni=real.example.com")
	assert.Contains(t, link, "#Trojan-Node")
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

func TestConvertSingleJSONToLink_Shadowsocks(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "ss",
		"address": "ss.example.com",
		"port": 8388,
		"method": "chacha20-ietf-poly1305",
		"password": "ss-pass",
		"remark": "SS-Node"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "ss://")
	assert.Contains(t, link, "#SS-Node")
}

func TestConvertSingleJSONToLink_Hysteria2(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "hysteria2",
		"address": "hy2.example.com",
		"port": 443,
		"password": "hy2-pass",
		"host": "hy2-real.example.com",
		"remark": "Hy2-Node"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "hysteria2://hy2-pass@hy2.example.com:443")
	assert.Contains(t, link, "#Hy2-Node")
}

func TestConvertSingleJSONToLink_TUIC_SNIFromCfg(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "tuic",
		"address": "tuic.example.com",
		"port": 8443,
		"uuid": "tuic-uuid",
		"password": "tuic-pass",
		"sni": "tuic-sni.example.com",
		"remark": "TUIC-Node"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	// TUIC credential must be in userinfo as uuid:password@host:port
	assert.Contains(t, link, "tuic://tuic-uuid:tuic-pass@tuic.example.com:8443")
	assert.Contains(t, link, "sni=tuic-sni.example.com")
	assert.Contains(t, link, "#TUIC-Node")
}

func TestConvertSingleJSONToLink_Shadowsocks_SIP002(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "ss",
		"address": "ss.example.com",
		"port": 8388,
		"method": "chacha20-ietf-poly1305",
		"password": "ss-pass",
		"remark": "SS-Node"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	// SIP002: ss://base64url(method:password)@host:port#tag, host:port in cleartext.
	assert.Equal(t, "ss://"+base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:ss-pass"))+"@ss.example.com:8388#SS-Node", link)
}

func TestConvertSingleJSONToLink_VLESS_SecurityDefaultNone(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "vless",
		"address": "v.example.com",
		"port": 80,
		"uuid": "uuid-1",
		"encryption": "none",
		"remark": "VLESS-NoTLS"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "security=none")
	assert.NotContains(t, link, "sni=")
}

func TestConvertSingleJSONToLink_VMess_PortIsString(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "vmess",
		"address": "vm.example.com",
		"port": 443,
		"uuid": "vm-uuid",
		"network": "tcp",
		"remark": "VMESS-Node"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(link, "vmess://"))

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	require.NoError(t, err)
	// v2rayNG (Gson) requires port as a string; a bare JSON number would fail to import.
	assert.Contains(t, string(decoded), `"port":"443"`)
	assert.NotContains(t, string(decoded), `"port":443`)
}

func TestConvertSingleJSONToLink_VMess_Aid(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "vmess",
		"address": "vmess.example.com",
		"port": 443,
		"uuid": "vmess-uuid",
		"aid": "1",
		"scy": "auto",
		"network": "tcp",
		"sni": "vmess-sni.example.com",
		"remark": "VMESS-Node"
	}`)

	cfg, err := toServerConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, "1", cfg.Aid)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(link, "vmess://"))

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	require.NoError(t, err)
	assert.Contains(t, string(decoded), `"aid":"1"`)
}

func TestConvertSingleJSONToLink_MissingAddressOrPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "missing address", raw: `{"type":"vless","port":443,"uuid":"u1"}`},
		{name: "missing port", raw: `{"type":"vless","address":"x.example.com","uuid":"u1"}`},
		{name: "zero port", raw: `{"type":"vless","address":"x.example.com","port":0,"uuid":"u1"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ConvertSingleJSONToLink(json.RawMessage(tt.raw))
			assert.Error(t, err, "broken configs must be rejected, not emitted as malformed links")
		})
	}
}

func TestConvertSingleJSONToLink_UnsupportedType(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"type":"wireguard","address":"wg.example.com","port":51820}`)
	_, err := ConvertSingleJSONToLink(raw)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported protocol")
}

func TestConvertSingleJSONToLink_InvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := ConvertSingleJSONToLink(json.RawMessage("not-json"))
	assert.Error(t, err)
}

// ==================== toServerConfig Alias Normalisation Tests ====================

func TestToServerConfig_AddressAlias(t *testing.T) {
	t.Parallel()

	// "host" field should be used as "address" when address is empty
	raw := json.RawMessage(`{"type":"vless","host":"alias.example.com","port":443,"uuid":"uuid1","encryption":"none"}`)
	cfg, err := toServerConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, "alias.example.com", cfg.Address)
}

func TestToServerConfig_PortAlias(t *testing.T) {
	t.Parallel()

	// "portNumber" should be used when "port" is 0
	raw := json.RawMessage(`{"type":"vless","address":"s.example.com","portNumber":8443,"uuid":"uuid1","encryption":"none"}`)
	cfg, err := toServerConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, 8443, cfg.Port)
}

func TestToServerConfig_UUIDAlias(t *testing.T) {
	t.Parallel()

	// "userId" should be used as "uuid" when uuid is empty
	raw := json.RawMessage(`{"type":"vless","address":"s.example.com","port":443,"userId":"user-id-1","encryption":"none"}`)
	cfg, err := toServerConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, "user-id-1", cfg.UUID)
}

func TestToServerConfig_RemarkAlias(t *testing.T) {
	t.Parallel()

	// "tag" should be used as "remark" when remark is empty
	raw := json.RawMessage(`{"type":"vless","address":"s.example.com","port":443,"uuid":"uuid1","encryption":"none","tag":"MyTag"}`)
	cfg, err := toServerConfig(raw)
	require.NoError(t, err)
	assert.Equal(t, "MyTag", cfg.Remark)
}

// ==================== TruncateString Tests ====================

func TestTruncateString_Short(t *testing.T) {
	t.Parallel()

	result := utils.TruncateString("hello", 10)
	assert.Equal(t, "hello", result)
}

func TestTruncateString_Exact(t *testing.T) {
	t.Parallel()

	result := utils.TruncateString("hello", 5)
	assert.Equal(t, "hello", result)
}

func TestTruncateString_Long(t *testing.T) {
	t.Parallel()

	result := utils.TruncateString("hello world", 5)
	assert.Equal(t, "hello...", result)
}

func TestTruncateString_Cyrillic(t *testing.T) {
	t.Parallel()

	// rune-safe: 4 Cyrillic runes + "..."
	result := utils.TruncateString("привет мир", 4)
	assert.Equal(t, "прив...", result)
}

// TestConvertSingleJSONToLink_Hysteria2_PinSHA256: hysteria2 links carry the
// leaf-cert hash as pinSHA256 so Xray-core 26.7.28+ clients can trust
// self-signed nodes without the removed allowInsecure (mirrors what 3x-ui's
// own subscription emits: pinSHA256 plus legacy insecure).
func TestConvertSingleJSONToLink_Hysteria2_PinSHA256(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "hysteria2",
		"address": "hy2.example.com",
		"port": 443,
		"password": "hy2-pass",
		"sni": "hy2-real.example.com",
		"allowInsecure": true,
		"pinSHA256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"remark": "Hy2-Pin"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "pinSHA256=e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	assert.Contains(t, link, "insecure=1")
	assert.Contains(t, link, "sni=hy2-real.example.com")
}

// TestConvertSingleJSONToLink_VLESS_PcsVcn: pcs/vcn are the v2rayN-era link
// params for pinnedPeerCertSha256/verifyPeerCertByName.
func TestConvertSingleJSONToLink_VLESS_PcsVcn(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "vless",
		"address": "v.example.com",
		"port": 443,
		"uuid": "aaaa-bbbb",
		"security": "tls",
		"sni": "v.example.com",
		"network": "ws",
		"path": "/ws",
		"pcs": "aabb",
		"vcn": "v.example.com",
		"remark": "V-Pin"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "pcs=aabb")
	assert.Contains(t, link, "vcn=v.example.com")
	assert.NotContains(t, link, "allowInsecure")
}

// TestToServerConfig_PcsAlias normalises the hysteria2 pinSHA256 spelling into
// the canonical PinSHA256 and lets an explicit pinSHA256 win over pcs.
func TestToServerConfig_PcsAlias(t *testing.T) {
	t.Parallel()

	cfg, err := toServerConfig(json.RawMessage(`{"type":"vless","address":"a","port":1,"pcs":"aa"}`))
	require.NoError(t, err)
	assert.Equal(t, "aa", cfg.PinSHA256)

	cfg, err = toServerConfig(json.RawMessage(`{"type":"hysteria2","address":"a","port":1,"pcs":"aa","pinSHA256":"bb"}`))
	require.NoError(t, err)
	assert.Equal(t, "bb", cfg.PinSHA256)
}

// TestConvertSingleJSONToLink_Trojan_PcsVcn: trojan links carry pcs/vcn like
// vless ones (see TestConvertSingleJSONToLink_VLESS_PcsVcn).
func TestConvertSingleJSONToLink_Trojan_PcsVcn(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "trojan",
		"address": "tj.example.com",
		"port": 443,
		"password": "pw",
		"security": "tls",
		"sni": "tj.example.com",
		"pcs": "aabb",
		"vcn": "tj.example.com",
		"remark": "TJ-Pin"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "pcs=aabb")
	assert.Contains(t, link, "vcn=tj.example.com")
	assert.NotContains(t, link, "allowInsecure")
}
