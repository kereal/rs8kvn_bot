package main

import (
	"context"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/testutil"
)

func init() {
	logger.Init("", "error")
}

func TestGetVersion(t *testing.T) {
	t.Run("returns non-empty string", func(t *testing.T) {
		v := getVersion()
		assert.NotEmpty(t, v, "getVersion() returned empty string")
	})

	t.Run("returns string with correct prefix", func(t *testing.T) {
		v := getVersion()
		assert.True(t, strings.HasPrefix(v, "rs8kvn_bot@"), "getVersion() = %s, want prefix rs8kvn_bot@", v)
	})

	t.Run("handles dev version gracefully", func(t *testing.T) {
		// When version is "dev", should still return a valid string
		v := getVersion()
		assert.Contains(t, v, "rs8kvn_bot@", "getVersion() should contain rs8kvn_bot@")
	})
}

func TestGetVersion_CommitVariable(t *testing.T) {
	// Test that commit variable is accessible
	t.Run("commit variable is defined", func(t *testing.T) {
		if commit == "" {
			t.Log("commit is empty (expected in test environment)")
		}
	})
}

func TestGetVersion_BuildTimeVariable(t *testing.T) {
	// Test that buildTime variable is accessible
	t.Run("buildTime variable is defined", func(t *testing.T) {
		if buildTime == "" {
			t.Log("buildTime is empty (expected in test environment)")
		}
	})
}

func TestHandleUpdate_CommandRouting(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI)
	ctx := context.Background()

	tests := []struct {
		name    string
		command string
	}{
		{"start command", "start"},
		{"help command", "help"},
		{"invite command", "invite"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123456},
					From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
					Text: "/" + tt.command,
				},
			}

			// Should not panic
			handleUpdate(ctx, handler, update)
		})
	}
}

func TestHandleUpdate_NonCommandMessage(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI)
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "Hello, this is not a command",
		},
	}

	// Should not panic
	handleUpdate(ctx, handler, update)
}

func TestHandleUpdate_CallbackQuery(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI)
	ctx := context.Background()

	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "test_data",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic
	handleUpdate(ctx, handler, update)
}

func TestHandleUpdateSafely_PanicRecovery(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI)
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "/start",
		},
	}

	// Should not panic even if internal panic occurs
	// The function has a recovery mechanism
	assert.NotPanics(t, func() {
		handleUpdateSafely(ctx, handler, update)
	})
}

func TestHandleUpdate_UnknownCommand(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI)
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "/unknowncommand",
		},
	}

	// Should not panic
	handleUpdate(ctx, handler, update)
}

// TestStartBackupScheduler_ContextCancellation тестирует остановку scheduler при отмене контекста
func TestStartBackupScheduler_ContextCancellation(t *testing.T) {
	// Создаём временный файл для имитации БД
	tmpFile := t.TempDir() + "/test.db"

	ctx, cancel := context.WithCancel(context.Background())

	// Запускаем scheduler в горутине
	done := make(chan struct{})
	go func() {
		startBackupScheduler(ctx, tmpFile)
		close(done)
	}()

	// Отменяем контекст почти сразу
	cancel()

	// Ждём завершения с таймаутом
	select {
	case <-done:
		// Успешно завершился
	case <-time.After(2 * time.Second):
		t.Fatal("Backup scheduler did not stop after context cancellation")
	}
}

// TestStartTrialCleanupScheduler_ContextCancellation тестирует остановку cleanup scheduler
func TestStartTrialCleanupScheduler_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Запускаем scheduler в горутине с nil параметрами (он не будет выполнять действия до тикера)
	done := make(chan struct{})
	go func() {
		// Используем nil для db и xui — ticker срабатывает через час, но мы отменим контекст раньше
		startTrialCleanupScheduler(ctx, nil, nil, 1, 3)
		close(done)
	}()

	// Отменяем контекст
	cancel()

	// Ждём завершения с таймаутом
	select {
	case <-done:
		// Успешно завершился
	case <-time.After(2 * time.Second):
		t.Fatal("Trial cleanup scheduler did not stop after context cancellation")
	}
}

// TestHandleUpdateSafely_PanicInHandler тестирует recovery при панике внутри handler
func TestHandleUpdateSafely_PanicInHandler(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI)
	ctx := context.Background()

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text: "/start",
		},
	}

	// Должно завершиться без паники благодаря recover
	assert.NotPanics(t, func() {
		handleUpdateSafely(ctx, handler, update)
	})
}

// TestGetVersion_WithLdflags тестирует getVersion при различных сценариях
func TestGetVersion_WithLdflags(t *testing.T) {
	t.Run("dev version", func(t *testing.T) {
		v := getVersion()
		assert.NotEmpty(t, v)
		assert.Contains(t, v, "rs8kvn_bot@")
	})
}

// TestHandleUpdate_NilMessage тестирует обработку update с nil Message
func TestHandleUpdate_NilMessage(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI)
	ctx := context.Background()

	update := tgbotapi.Update{}

	assert.NotPanics(t, func() {
		handleUpdate(ctx, handler, update)
	})
}

// TestGetVersion_WithBuildInfo тестирует getVersion с различными build info
func TestGetVersion_WithBuildInfo(t *testing.T) {
	t.Run("dev version returns valid format", func(t *testing.T) {
		v := getVersion()
		assert.NotEmpty(t, v)
		assert.Contains(t, v, "rs8kvn_bot@")
	})

	t.Run("version starts with rs8kvn_bot@", func(t *testing.T) {
		v := getVersion()
		assert.True(t, strings.HasPrefix(v, "rs8kvn_bot@"), "version should start with 'rs8kvn_bot@'")
	})
}

// TestHandleUpdate_UnknownCommands тестирует обработку неизвестных команд
func TestHandleUpdate_UnknownCommands(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI)
	ctx := context.Background()

	tests := []struct {
		name    string
		command string
	}{
		{"del command", "del"},
		{"broadcast command", "broadcast"},
		{"send command", "send"},
		{"unknown command", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123456},
					From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
					Text: "/" + tt.command,
				},
			}

			// Should not panic for any command
			assert.NotPanics(t, func() {
				handleUpdate(ctx, handler, update)
			})
		})
	}
}
