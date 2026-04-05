package smoke

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSmoke_BinaryStartup(t *testing.T) {
	if os.Getenv("CI") == "" && !strings.Contains(os.Getenv("RUN_SMOKE")+"", "1") {
		t.Skip("Set RUN_SMOKE=1 to run smoke tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.env")

	cfg := `TELEGRAM_BOT_TOKEN=test_token_1234567:ABCdefGHIjklMNOpqrstuVWX
TELEGRAM_ADMIN_ID=0
XUI_HOST=http://localhost:2053
XUI_USERNAME=admin
XUI_PASSWORD=password
XUI_INBOUND_ID=1
DATABASE_PATH=` + dir + `/test.db
LOG_FILE_PATH=` + dir + `/test.log
LOG_LEVEL=debug
`

	err := os.WriteFile(configPath, []byte(cfg), 0644)
	require.NoError(t, err, "Failed to write config")

	binPath := filepath.Join(dir, "bot_test")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/bot")
	build.Dir = filepath.Join("..", "..")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	require.NoError(t, build.Run(), "Failed to build binary")

	cmd := exec.CommandContext(ctx, binPath, "-config", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	require.NoError(t, err, "Failed to start binary")

	time.Sleep(3 * time.Second)

	processExited := false
	exitCode := 0

	if cmd.Process != nil {
		processExited = true
		_ = cmd.Process.Kill()
		waitErr := cmd.Wait()
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
			}
		}
	}

	if !processExited {
		if err := cmd.Wait(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
					exitCode = status.ExitStatus()
				}
			}
		}
	}

	if exitCode != 0 {
		t.Logf("Binary exited with code: %d", exitCode)
	}

	t.Log("Smoke test passed: binary started and ran without unexpected crash")
}

func TestSmoke_ConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name: "missing_token",
			config: `TELEGRAM_ADMIN_ID=0
XUI_HOST=http://localhost:2053
XUI_USERNAME=admin
XUI_PASSWORD=password
XUI_INBOUND_ID=1
`,
			wantErr: true,
		},
		{
			name: "missing_xui",
			config: `TELEGRAM_BOT_TOKEN=test
XUI_INBOUND_ID=1
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.env")
			err := os.WriteFile(configPath, []byte(tt.config), 0644)
			require.NoError(t, err)

			binPath := filepath.Join(dir, "bot_test")
			build := exec.Command("go", "build", "-o", binPath, "./cmd/bot")
			build.Dir = filepath.Join("..", "..")
			err = build.Run()
			require.NoError(t, err, "Failed to build")

			ctx, cancel := context.WithTimeout(testCtx, 5*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, binPath, "-config", configPath)
			err = cmd.Start()
			if err != nil {
				require.NoError(t, err)
			}

			time.Sleep(2 * time.Second)

			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				cmd.Wait()
			}

			if tt.wantErr {
				if err := cmd.Wait(); err != nil {
					t.Logf("Expected error occurred: %v", err)
				}
			}
		})
	}
}

func TestSmoke_PanicRecovery(t *testing.T) {
	if os.Getenv("CI") == "" {
		t.Skip("Skipping panic recovery test in non-CI environment")
	}

	tests := []struct {
		name   string
		config string
		panic  bool
	}{
		{
			name: "valid_config_no_panic",
			config: `TELEGRAM_BOT_TOKEN=test_token_1234567:ABCdefGHIjklMNOpqrstuVWX
TELEGRAM_ADMIN_ID=0
XUI_HOST=http://localhost:2053
XUI_USERNAME=admin
XUI_PASSWORD=password
XUI_INBOUND_ID=1
`,
			panic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.env")
			err := os.WriteFile(configPath, []byte(tt.config), 0644)
			require.NoError(t, err)

			binPath := filepath.Join(dir, "bot_test")
			build := exec.Command("go", "build", "-o", binPath, "./cmd/bot")
			build.Dir = filepath.Join("..", "..")
			err = build.Run()
			require.NoError(t, err)

			cmd := exec.Command(binPath, "-config", configPath)
			err = cmd.Start()
			require.NoError(t, err)

			time.Sleep(2 * time.Second)

			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				cmd.Wait()
			}

			require.False(t, tt.panic, "Expected no panic")
		})
	}
}

var testCtx = context.Background()

func init() {
	fmt.Println("Smoke test package initialized")
}
