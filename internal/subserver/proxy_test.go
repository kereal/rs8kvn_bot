package subserver

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

	data, err := os.ReadFile("../testdata/subserver/vless_single.txt")
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

	data, err := os.ReadFile("../testdata/subserver/base64_encoded.txt")
	require.NoError(t, err)
	assert.Equal(t, FormatBase64, DetectFormat(data))
}

func TestDetectFormat_JSON(t *testing.T) {
	t.Parallel()

	body := []byte(`{"type":"vless","address":"x.com","port":443,"uuid":"abc","encryption":"none"}`)
	assert.Equal(t, FormatJSON, DetectFormat(body))
}

func TestDetectFormat_JSONArray(t *testing.T) {
	t.Parallel()

	body := []byte(`[{"type":"vless","address":"x.com","port":443,"uuid":"abc","encryption":"none"}]`)
	assert.Equal(t, FormatJSON, DetectFormat(body))
}

func TestDetectFormat_InvalidBase64(t *testing.T) {
	t.Parallel()

	assert.Equal(t, FormatUnknown, DetectFormat([]byte("not-valid-base64!!!")))
}

func TestDetectFormat_Empty(t *testing.T) {
	t.Parallel()

	assert.Equal(t, FormatUnknown, DetectFormat([]byte("")))
	assert.Equal(t, FormatUnknown, DetectFormat([]byte("  ")))
}

func TestBase64StdEncode(t *testing.T) {
	t.Parallel()

	input := []byte("hello world")
	expected := base64.StdEncoding.EncodeToString(input)
	assert.Equal(t, expected, base64StdEncode(input))
}

func TestConvertJSONToShareLinks_VLESS_ThroughProxy(t *testing.T) {
	t.Parallel()

	body := []byte(`{"type":"vless","address":"x.com","port":443,"uuid":"abc","encryption":"none","remark":"Test"}`)
	links, err := ConvertJSONToShareLinks(body)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Contains(t, links[0], "vless://abc@x.com:443")
}
