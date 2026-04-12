package subproxy

import (
	"encoding/base64"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectFormat_Plain(t *testing.T) {
	t.Parallel()

	body := []byte("vless://abc@server.com:443\nvmess://data")
	assert.Equal(t, FormatPlain, DetectFormat(body))
}

func TestDetectFormat_Plain_Golden(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../testdata/subproxy/vless_single.txt")
	require.NoError(t, err)
	assert.Equal(t, FormatPlain, DetectFormat(data))
}

func TestDetectFormat_Base64(t *testing.T) {
	t.Parallel()

	plain := "vless://abc@server.com:443\nvmess://data"
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))
	assert.Equal(t, FormatBase64, DetectFormat([]byte(encoded)))
}

func TestDetectFormat_Base64_Golden(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../testdata/subproxy/base64_encoded.txt")
	require.NoError(t, err)
	assert.Equal(t, FormatBase64, DetectFormat(data))
}

func TestDetectFormat_InvalidBase64(t *testing.T) {
	t.Parallel()

	assert.Equal(t, FormatPlain, DetectFormat([]byte("not-valid-base64!!!")))
}

func TestMergeSubscriptions_NoExtraServers(t *testing.T) {
	t.Parallel()

	original := []byte("vless://original\nvmess://original2")
	result := MergeSubscriptions(original, nil, FormatPlain)
	assert.Equal(t, original, result)
}

func TestMergeSubscriptions_PlainText(t *testing.T) {
	t.Parallel()

	original := []byte("vless://original\nvmess://original2")
	extra := []string{"trojan://extra1", "ss://extra2"}

	result := MergeSubscriptions(original, extra, FormatPlain)
	expected := "vless://original\nvmess://original2\ntrojan://extra1\nss://extra2"
	assert.Equal(t, expected, string(result))
}

func TestMergeSubscriptions_Base64(t *testing.T) {
	t.Parallel()

	plain := "vless://original\nvmess://original2"
	original := []byte(base64.StdEncoding.EncodeToString([]byte(plain)))
	extra := []string{"trojan://extra1", "ss://extra2"}

	result := MergeSubscriptions(original, extra, FormatBase64)

	decoded, err := base64.StdEncoding.DecodeString(string(result))
	assert.NoError(t, err)

	expected := "vless://original\nvmess://original2\ntrojan://extra1\nss://extra2"
	assert.Equal(t, expected, string(decoded))
}

func TestMergeSubscriptions_Base64WithNewlines(t *testing.T) {
	t.Parallel()

	plain := "vless://original\nvmess://original2\n"
	original := []byte(base64.StdEncoding.EncodeToString([]byte(plain)))
	extra := []string{"trojan://extra1"}

	result := MergeSubscriptions(original, extra, FormatBase64)

	decoded, err := base64.StdEncoding.DecodeString(string(result))
	assert.NoError(t, err)

	expected := "vless://original\nvmess://original2\ntrojan://extra1"
	assert.Equal(t, expected, string(decoded))
}

func TestMergeSubscriptions_InvalidBase64FallsBack(t *testing.T) {
	t.Parallel()

	original := []byte("not-valid-base64")
	extra := []string{"trojan://extra1"}

	result := MergeSubscriptions(original, extra, FormatBase64)
	assert.Equal(t, original, result)
}

func TestMergeSubscriptions_EmptyOriginal(t *testing.T) {
	t.Parallel()

	extra := []string{"trojan://extra1"}

	result := MergeSubscriptions([]byte(""), extra, FormatPlain)
	expected := "trojan://extra1"
	assert.Equal(t, expected, string(result))
}

func TestMergeSubscriptions_GoldenFile(t *testing.T) {
	t.Parallel()

	original, err := os.ReadFile("../testdata/subproxy/vmess_multi.txt")
	require.NoError(t, err)
	extra := []string{"ss://new-server.example.com"}

	result := MergeSubscriptions(original, extra, FormatPlain)
	assert.Contains(t, string(result), "vmess://")
	assert.Contains(t, string(result), "trojan://")
	assert.Contains(t, string(result), "ss://new-server.example.com")
}
