//go:build smoke

package smoke

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/stretchr/testify/require"
)

// binPath holds the path to the pre-built binary (built once in TestMain).
var binPath string

// readinessMarker — лог, который бинарный бот пишет в stdout после успешного
// запуска web server, до retry-цикла initBot с фейковым токеном Telegram.
const readinessMarker = "Web server started"

// validSmokeEnv возвращает env, который проходит cfg.validate() и доводит
// процесс до readiness-лога. dbPath указывает на заранее засеянную БД
// (бинарику нужен хотя бы один node: initDatabase падает с
// "no nodes configured" на пустой базе).
func validSmokeEnv(dbPath, logPath string) []string {
	return []string{
		"TELEGRAM_BOT_TOKEN=123456:smoke-test-token",
		"TELEGRAM_ADMIN_ID=1",
		"DATABASE_PATH=" + dbPath,
		"LOG_FILE_PATH=" + logPath,
		"LOG_LEVEL=debug",
		"GLOBAL_SUB_URL=https://sub.example.com",
		"SITE_URL=https://vpn.example.com",
		"WEB_SERVER_PORT=18744",
	}
}

// prepareEnvDir создаёт БД с одной нодой (host заведомо недоступен — клиенты
// строятся без сетевых вызовов) и возвращает env для запуска бинарника.
func prepareEnvDir(t *testing.T) []string {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	logPath := filepath.Join(dir, "test.log")

	svc, err := database.NewService(dbPath)
	require.NoError(t, err, "Failed to seed smoke database")
	require.NoError(t, svc.CreateNode(context.Background(), &database.Node{
		Name:       "smoke-node",
		Type:       database.NodeType3xUI,
		IsActive:   true,
		Host:       "http://127.0.0.1:1",
		APIToken:   "smoke-token",
		InboundIDs: `[1]`,
	}), "Failed to create smoke node")
	require.NoError(t, svc.Close(), "Failed to close seeded database")

	return validSmokeEnv(dbPath, logPath)
}

func TestMain(m *testing.M) {
	// Засев БД ниже вызывает logger.Info внутри database.NewService —
	// глобальный zap-логгер нужно инициализировать в процессе теста.
	if _, lerr := logger.Init("", "error"); lerr != nil {
		fmt.Fprintf(os.Stderr, "Failed to init test logger: %v\n", lerr)
		os.Exit(1)
	}

	// Build binary once before all tests
	dir, err := os.MkdirTemp("", "smoke-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create temp dir: %v\n", err)
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

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startBinary запускает бинарник с заданным env и ждёт readiness-лог в его
// stdout. Краш процесса до readiness-лога (результат cmd.Wait) приводит к
// падению теста с захваченным выводом — вместо фиксированного Sleep.
// Возвращает cmd и канал с результатом Wait; завершить процесс нужно через stopBinary.
func startBinary(t *testing.T, envVars []string) (*exec.Cmd, <-chan error) {
	t.Helper()

	cmd := exec.Command(binPath)
	cmd.Env = envVars
	var out syncBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	require.NoError(t, cmd.Start(), "Failed to start binary")

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		if strings.Contains(out.String(), readinessMarker) {
			return cmd, exited
		}
		select {
		case err := <-exited:
			require.Failf(t, "binary exited before readiness",
				"wait error: %v; output:\n%s", err, out.String())
		case <-deadline:
			_ = cmd.Process.Kill()
			<-exited
			require.Failf(t, "binary did not reach readiness in time",
				"missing %q in output:\n%s", readinessMarker, out.String())
		case <-ticker.C:
		}
	}
}

func stopBinary(t *testing.T, cmd *exec.Cmd, exited <-chan error) {
	t.Helper()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		<-exited
	}
}

func TestSmoke_BinaryStartup(t *testing.T) {
	if os.Getenv("CI") == "" && !strings.Contains(os.Getenv("RUN_SMOKE")+"", "1") {
		t.Skip("Set RUN_SMOKE=1 to run smoke tests")
	}

	cmd, exited := startBinary(t, prepareEnvDir(t))
	stopBinary(t, cmd, exited)

	t.Log("Smoke test passed: binary reached readiness without unexpected crash")
}

// TestSmoke_PanicRecovery удалён: в бинарнике нет инъекционной точки паники
// (никакой env-ручки или тестового хука нет, добавлять прод-ручку ради smoke —
// неоправданно). Паники и восстановление покрыты юнит-тестами: cmd/bot
// TestHandleUpdateSafely (паникующий deps + recover), supervisor-тесты
// TestRunSupervised_* (перезапуск после паники с backoff).

func TestSmoke_ConfigValidation(t *testing.T) {
	if os.Getenv("CI") == "" && !strings.Contains(os.Getenv("RUN_SMOKE")+"", "1") {
		t.Skip("Set RUN_SMOKE=1 to run smoke tests")
	}

	tests := []struct {
		name    string
		envVars []string
		wantIn  string // подстрока ошибки конфигурации в stderr
	}{
		{
			name: "missing_token",
			envVars: []string{
				"TELEGRAM_ADMIN_ID=1",
				"GLOBAL_SUB_URL=https://sub.example.com",
				"SITE_URL=https://vpn.example.com",
			},
			wantIn: "TELEGRAM_BOT_TOKEN",
		},
		{
			name: "invalid_token",
			envVars: []string{
				"TELEGRAM_BOT_TOKEN=test",
				"TELEGRAM_ADMIN_ID=1",
				"GLOBAL_SUB_URL=https://sub.example.com",
				"SITE_URL=https://vpn.example.com",
			},
			wantIn: "TELEGRAM_BOT_TOKEN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(binPath)
			// Полностью переопределяем env: родительская среда не должна
			// случайно вылечить невалидный конфиг.
			cmd.Env = tt.envVars
			var stderr bytes.Buffer
			cmd.Stderr = &stderr

			err := cmd.Start()
			require.NoError(t, err, "Failed to start binary")

			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()

			// Невалидный конфиг должен завершить процесс сам, быстро.
			select {
			case err := <-done:
				require.Error(t, err, "invalid config must exit non-zero")
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr, "process must be terminated by config validation")
				require.NotEqual(t, 0, exitErr.ExitCode(), "config validation failure must produce non-zero exit code")
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Fatalf("binary with invalid config did not exit in time")
			}

			require.Contains(t, stderr.String(), "Failed to load config",
				"validation failure must be reported to stderr")
			require.Contains(t, stderr.String(), tt.wantIn)
		})
	}
}
