package subproxy

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rs8kvn_bot/internal/config"
)

func TestService_NewService_LoadsConfig(t *testing.T) {
	tmpDir := t.TempDir()
	serversFile := filepath.Join(tmpDir, "config.txt")
	err := os.WriteFile(serversFile, []byte("X-Custom: value\n\nvless://server1@example.com:443\nvless://server2@example.com:443\n"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{
		SubExtraServersEnabled: true,
		SubExtraServersFile:    serversFile,
	}

	svc := NewService(cfg)
	defer svc.Stop()

	servers := svc.GetExtraServers()
	assert.Len(t, servers, 2)
	assert.Equal(t, "vless://server1@example.com:443", servers[0])
	assert.Equal(t, "vless://server2@example.com:443", servers[1])

	headers := svc.GetExtraHeaders()
	assert.Equal(t, "value", headers["X-Custom"])
}

func TestService_NewService_Disabled(t *testing.T) {
	cfg := &config.Config{
		SubExtraServersEnabled: false,
		SubExtraServersFile:    "/some/path.txt",
	}

	svc := NewService(cfg)
	defer svc.Stop()

	servers := svc.GetExtraServers()
	assert.Empty(t, servers)

	headers := svc.GetExtraHeaders()
	assert.Empty(t, headers)
}

func TestService_ReloadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	serversFile := filepath.Join(tmpDir, "config.txt")
	err := os.WriteFile(serversFile, []byte("X-Old: old\n\nvless://initial@example.com:443\n"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{
		SubExtraServersEnabled: true,
		SubExtraServersFile:    serversFile,
	}

	svc := NewService(cfg)
	defer svc.Stop()

	servers := svc.GetExtraServers()
	assert.Len(t, servers, 1)
	headers := svc.GetExtraHeaders()
	assert.Equal(t, "old", headers["X-Old"])

	err = os.WriteFile(serversFile, []byte("X-New: new\n\nvless://updated1@example.com:443\nvless://updated2@example.com:443\n"), 0644)
	require.NoError(t, err)

	svc.ReloadConfig()

	servers = svc.GetExtraServers()
	assert.Len(t, servers, 2)
	assert.Equal(t, "vless://updated1@example.com:443", servers[0])
	assert.Equal(t, "vless://updated2@example.com:443", servers[1])

	headers = svc.GetExtraHeaders()
	assert.Equal(t, "new", headers["X-New"])
	assert.Empty(t, headers["X-Old"])
}

func TestService_ReloadConfig_FileDeleted(t *testing.T) {
	tmpDir := t.TempDir()
	serversFile := filepath.Join(tmpDir, "config.txt")
	err := os.WriteFile(serversFile, []byte("X-Key: val\n\nvless://initial@example.com:443\n"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{
		SubExtraServersEnabled: true,
		SubExtraServersFile:    serversFile,
	}

	svc := NewService(cfg)
	defer svc.Stop()

	servers := svc.GetExtraServers()
	assert.Len(t, servers, 1)

	err = os.Remove(serversFile)
	require.NoError(t, err)

	svc.ReloadConfig()

	servers = svc.GetExtraServers()
	assert.Len(t, servers, 1)
	assert.Equal(t, "vless://initial@example.com:443", servers[0])
}

func TestService_StartReloadLoop(t *testing.T) {
	tmpDir := t.TempDir()
	serversFile := filepath.Join(tmpDir, "config.txt")
	err := os.WriteFile(serversFile, []byte("vless://v1@example.com:443\n"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{
		SubExtraServersEnabled: true,
		SubExtraServersFile:    serversFile,
	}

	svc := NewService(cfg)
	defer svc.Stop()

	stopCh := make(chan struct{})
	go svc.StartReloadLoop(100*time.Millisecond, stopCh)

	time.Sleep(150 * time.Millisecond)

	err = os.WriteFile(serversFile, []byte("X-Dyn: dynamic\n\nvless://v2@example.com:443\nvless://v3@example.com:443\n"), 0644)
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond)

	servers := svc.GetExtraServers()
	assert.Len(t, servers, 2)
	assert.Equal(t, "vless://v2@example.com:443", servers[0])

	headers := svc.GetExtraHeaders()
	assert.Equal(t, "dynamic", headers["X-Dyn"])

	close(stopCh)
}
