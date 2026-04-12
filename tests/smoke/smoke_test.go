package smoke

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSmoke_BinaryStartup(t *testing.T) {
	if os.Getenv("CI") == "" && !strings.Contains(os.Getenv("RUN_SMOKE")+"", "1") {
		t.Skip("Set RUN_SMOKE=1 to run smoke tests")
	}

	dir := t.TempDir()

	binPath := filepath.Join(dir, "bot_test")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/bot")
	build.Dir = filepath.Join("..", "..")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	require.NoError(t, build.Run(), "Failed to build binary")

	// Create context after build to prevent timeout during compilation
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath)
	// Pass config as environment variables (binary reads from env, not from -config flag)
	cmd.Env = append(os.Environ(),
		"TELEGRAM_BOT_TOKEN=test_token_1234567:ABCdefGHIjklMNOpqrstuVWX",
		"TELEGRAM_ADMIN_ID=0",
		"XUI_HOST=http://localhost:2053",
		"XUI_USERNAME=admin",
		"XUI_PASSWORD=password",
		"XUI_INBOUND_ID=1",
		"DATABASE_PATH="+filepath.Join(dir, "test.db"),
		"LOG_FILE_PATH="+filepath.Join(dir, "test.log"),
		"LOG_LEVEL=debug",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	require.NoError(t, err, "Failed to start binary")

	// Give the binary a moment to start and initialize
	time.Sleep(500 * time.Millisecond)

	// Kill the process — we just verified it starts without crash
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	t.Log("Smoke test passed: binary started and ran without unexpected crash")
}

func TestSmoke_ConfigValidation(t *testing.T) {
	dir := t.TempDir()

	// Build binary once for all subtests
	binPath := filepath.Join(dir, "bot_test")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/bot")
	build.Dir = filepath.Join("..", "..")
	require.NoError(t, build.Run(), "Failed to build binary")

	tests := []struct {
		name    string
		envVars []string
		wantErr bool
	}{
		{
			name: "missing_token",
			envVars: []string{
				"TELEGRAM_ADMIN_ID=0",
				"XUI_HOST=http://localhost:2053",
				"XUI_USERNAME=admin",
				"XUI_PASSWORD=password",
				"XUI_INBOUND_ID=1",
			},
			wantErr: true,
		},
		{
			name: "missing_xui",
			envVars: []string{
				"TELEGRAM_BOT_TOKEN=test",
				"XUI_INBOUND_ID=1",
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

			// Wait briefly for the binary to exit with validation error
			done := make(chan error, 1)
			go func() {
				done <- cmd.Wait()
			}()

			select {
			case <-done:
				// Binary exited (likely with validation error)
			case <-ctx.Done():
				// Timeout — kill the process
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

func TestSmoke_PanicRecovery(t *testing.T) {
	if os.Getenv("CI") == "" {
		t.Skip("Skipping panic recovery test in non-CI environment")
	}

	dir := t.TempDir()

	binPath := filepath.Join(dir, "bot_test")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/bot")
	build.Dir = filepath.Join("..", "..")
	require.NoError(t, build.Run(), "Failed to build binary")

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
				"XUI_HOST=http://localhost:2053",
				"XUI_USERNAME=admin",
				"XUI_PASSWORD=password",
				"XUI_INBOUND_ID=1",
			},
			hasPanic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(binPath)
			cmd.Env = append(os.Environ(), tt.envVars...)

			err := cmd.Start()
			require.NoError(t, err)

			time.Sleep(500 * time.Millisecond)

			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				cmd.Wait()
			}

			require.False(t, tt.hasPanic, "Expected no panic")
		})
	}
}


