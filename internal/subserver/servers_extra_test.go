package subserver

import (
	"encoding/json"
	"testing"

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
		"sni": "real.example.com",
		"remark": "Trojan-Node"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "trojan://my-secret@trojan.example.com:443")
	assert.Contains(t, link, "sni=real.example.com")
	assert.Contains(t, link, "#Trojan-Node")
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

func TestConvertSingleJSONToLink_SOCKS(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "socks5",
		"address": "socks.example.com",
		"port": 1080,
		"uuid": "user-uuid",
		"remark": "SOCKS-Node"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "socks://user-uuid@socks.example.com:1080")
	assert.Contains(t, link, "#SOCKS-Node")
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

func TestConvertSingleJSONToLink_TUIC(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"type": "tuic",
		"address": "tuic.example.com",
		"port": 8443,
		"uuid": "tuic-uuid",
		"password": "tuic-pass",
		"host": "tuic-sni.example.com",
		"remark": "TUIC-Node"
	}`)

	link, err := ConvertSingleJSONToLink(raw)
	require.NoError(t, err)
	assert.Contains(t, link, "tuic://tuic.example.com:8443")
	assert.Contains(t, link, "uuid=tuic-uuid")
	assert.Contains(t, link, "password=tuic-pass")
	assert.Contains(t, link, "#TUIC-Node")
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

// ==================== truncateString Tests ====================

func TestTruncateString_Short(t *testing.T) {
	t.Parallel()

	result := truncateString("hello", 10)
	assert.Equal(t, "hello", result)
}

func TestTruncateString_Exact(t *testing.T) {
	t.Parallel()

	result := truncateString("hello", 5)
	assert.Equal(t, "hello", result)
}

func TestTruncateString_Long(t *testing.T) {
	t.Parallel()

	result := truncateString("hello world", 5)
	assert.Equal(t, "hello...", result)
}
