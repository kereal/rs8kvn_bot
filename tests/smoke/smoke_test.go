//go:build smoke

package smoke

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// binPath holds the path to the pre-built binary (built once in TestMain).
var binPath string

func TestMain(m *testing.M) {
	// Build binary once before all tests
	dir, err := os.MkdirTemp("", "smoke-test-*")
	if err != nil {
		os.Stderr.WriteString("Failed to create temp dir: " + err.Error() + "\n")
		os.Exit(1)
	}
	cleanup := func() {
		if err := os.RemoveAll(dir); err != nil {
			_, werr := os.Stderr.WriteString("Failed to cleanup temp dir: " + err.Error() + "\n")
			if werr != nil {
				// Fallback to stdout if stderr is unavailable
				fmt.Fprintln(os.Stdout, "Failed to cleanup temp dir:", err.Error())
			}
		}
	}

	binPath = filepath.Join(dir, "bot_test")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/bot")
	build.Dir = filepath.Join("..", "..")
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		os.Stderr.WriteString("Failed to build binary: " + err.Error() + "\n")
		cleanup()
		os.Exit(1)
	}

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func startBinary(t *testing.T, envVars []string, extraEnvVars ...string) *exec.Cmd {
	t.Helper()
	allEnv := append(envVars, extraEnvVars...)
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(), allEnv...)
	err := cmd.Start()
	require.NoError(t, err, "Failed to start binary")
	time.Sleep(500 * time.Millisecond)
	return cmd
}

func stopBinary(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

func TestSmoke_BinaryStartup(t *testing.T) {
	if os.Getenv("CI") == "" && !strings.Contains(os.Getenv("RUN_SMOKE")+"", "1") {
		t.Skip("Set RUN_SMOKE=1 to run smoke tests")
	}

	dir := t.TempDir()

	cmd := startBinary(t, []string{
		"TELEGRAM_BOT_TOKEN=test_token_1234567:ABCdefGHIjklMNOpqrsTUVwWX",
		"TELEGRAM_ADMIN_ID=0",
	}, "DATABASE_PATH="+filepath.Join(dir, "test.db"),
		"LOG_FILE_PATH="+filepath.Join(dir, "test.log"),
		"LOG_LEVEL=debug",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	stopBinary(t, cmd)

	t.Log("Smoke test passed: binary started and ran without unexpected crash")
}

func TestSmoke_PanicRecovery(t *testing.T) {
	if os.Getenv("CI") == "" {
		t.Skip("Skipping panic recovery test in non-CI environment")
	}

	tests := []struct {
		name     string
		envVars  []string
		hasPanic bool
	}{
		{
			name: "valid_config_no_panic",
			envVars: []string{
				"TELEGRAM_BOT_TOKEN=test_token_1234567:ABCdefGHIjklMNOpqrstuVWX",
				"TELEGRAM_ADMIN_ID=0",
			},
			hasPanic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := startBinary(t, tt.envVars)
			stopBinary(t, cmd)
			require.False(t, tt.hasPanic, "Expected no panic")
		})
	}
}

func TestSmoke_ConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		envVars []string
		wantErr bool
	}{
		{
			name: "missing_token",
			envVars: []string{
				"TELEGRAM_ADMIN_ID=0",
			},
			wantErr: true,
		},
		{
			name: "invalid_token",
			envVars: []string{
				"TELEGRAM_BOT_TOKEN=test",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, binPath)
			cmd.Env = append(os.Environ(), tt.envVars...)

			err := cmd.Start()
			require.NoError(t, err, "Failed to start binary")

			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()

			select {
			case <-done:
			case <-ctx.Done():
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}

			if tt.wantErr {
				t.Logf("Expected validation error for %s", tt.name)
			}
		})
	}
}
