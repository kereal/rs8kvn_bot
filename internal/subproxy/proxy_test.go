package subproxy

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectFormat_Plain(t *testing.T) {
	body := []byte("vless://abc@server.com:443\nvmess://data")
	assert.Equal(t, FormatPlain, DetectFormat(body))
}

func TestDetectFormat_Base64(t *testing.T) {
	plain := "vless://abc@server.com:443\nvmess://data"
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))
	assert.Equal(t, FormatBase64, DetectFormat([]byte(encoded)))
}

func TestDetectFormat_InvalidBase64(t *testing.T) {
	assert.Equal(t, FormatPlain, DetectFormat([]byte("not-valid-base64!!!")))
}

func TestMergeSubscriptions_NoExtraServers(t *testing.T) {
	original := []byte("vless://original\nvmess://original2")
	result := MergeSubscriptions(original, nil, FormatPlain)
	assert.Equal(t, original, result)
}

func TestMergeSubscriptions_PlainText(t *testing.T) {
	original := []byte("vless://original\nvmess://original2")
	extra := []string{"trojan://extra1", "ss://extra2"}

	result := MergeSubscriptions(original, extra, FormatPlain)
	expected := "vless://original\nvmess://original2\ntrojan://extra1\nss://extra2"
	assert.Equal(t, expected, string(result))
}

func TestMergeSubscriptions_Base64(t *testing.T) {
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
	original := []byte("not-valid-base64")
	extra := []string{"trojan://extra1"}

	result := MergeSubscriptions(original, extra, FormatBase64)
	assert.Equal(t, original, result)
}

func TestMergeSubscriptions_EmptyOriginal(t *testing.T) {
	extra := []string{"trojan://extra1"}

	result := MergeSubscriptions([]byte(""), extra, FormatPlain)
	expected := "trojan://extra1"
	assert.Equal(t, expected, string(result))
}
