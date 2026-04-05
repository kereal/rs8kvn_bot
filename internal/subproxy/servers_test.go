package subproxy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadExtraConfig_EmptyPath(t *testing.T) {
	cfg, err := LoadExtraConfig("")
	assert.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestLoadExtraConfig_HeadersAndServers(t *testing.T) {
	content := `X-Custom-Header: custom-value
Profile-Title: My VPN
# This is a comment

vless://abc123@server1.example.com:443
vmess://base64data
trojan://password@server2.example.com:443

ss://base64@server3.example.com:8080
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	cfg, err := LoadExtraConfig(filePath)
	require.NoError(t, err)

	assert.Equal(t, "custom-value", cfg.Headers["X-Custom-Header"])
	assert.Equal(t, "My VPN", cfg.Headers["Profile-Title"])
	assert.Len(t, cfg.Servers, 4)
	assert.Equal(t, "vless://abc123@server1.example.com:443", cfg.Servers[0])
	assert.Equal(t, "vmess://base64data", cfg.Servers[1])
	assert.Equal(t, "trojan://password@server2.example.com:443", cfg.Servers[2])
	assert.Equal(t, "ss://base64@server3.example.com:8080", cfg.Servers[3])
}

func TestLoadExtraConfig_OnlyHeaders(t *testing.T) {
	content := `X-Custom: value
# comment
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	cfg, err := LoadExtraConfig(filePath)
	require.NoError(t, err)

	assert.Equal(t, "value", cfg.Headers["X-Custom"])
	assert.Empty(t, cfg.Servers)
}

func TestLoadExtraConfig_OnlyServers(t *testing.T) {
	content := `
vless://server@example.com:443
trojan://pass@server2.com:443
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	cfg, err := LoadExtraConfig(filePath)
	require.NoError(t, err)

	assert.Empty(t, cfg.Headers)
	assert.Len(t, cfg.Servers, 2)
}

func TestLoadExtraConfig_FileNotFound(t *testing.T) {
	_, err := LoadExtraConfig("/nonexistent/path/config.txt")
	assert.Error(t, err)
}

func TestLoadExtraConfig_InvalidLines(t *testing.T) {
	content := `X-Valid: header
not-a-valid-header-or-server
vless://valid@server.com:443
http://not-a-proxy-link
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	cfg, err := LoadExtraConfig(filePath)
	require.NoError(t, err)

	assert.Equal(t, "header", cfg.Headers["X-Valid"])
	assert.Len(t, cfg.Servers, 1)
	assert.Equal(t, "vless://valid@server.com:443", cfg.Servers[0])
}

func TestLoadExtraConfig_HeaderOverride(t *testing.T) {
	content := `X-Duplicate: first
X-Duplicate: second

vless://server@example.com:443
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.txt")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	cfg, err := LoadExtraConfig(filePath)
	require.NoError(t, err)

	assert.Equal(t, "second", cfg.Headers["X-Duplicate"])
}
