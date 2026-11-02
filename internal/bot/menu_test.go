package bot

import (
	"context"
	"errors"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHandleBackToStart_WithActiveSubscription(t *testing.T) {
	t.Parallel()

	// Status "active" and "inactive" both land on the main-menu screen;
	// only nil DB errors skip the edit.
	tests := []struct {
		name   string
		status string
	}{
		{"active subscription", "active"},
		{"inactive subscription", "inactive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()

			mockBot := testutil.NewBotAPI()
			mockDB := testutil.NewBotStoreFake()
			status := tt.status
			mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
				return &database.Subscription{
					ID:             1,
					TelegramID:     12345,
					Username:       "testuser",
					Status:         status,
					SubscriptionID: "abc123",
				}, nil
			}

			cfg := &config.Config{
				TelegramBotToken: "test:token",
				TelegramAdminID:  0,
			}

			handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
			handler.handleBackToStart(ctx, 12345, "testuser", 100)

			require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
			editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
			require.True(t, ok, "Should be EditMessageTextConfig")
			assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID")
			assert.Equal(t, 100, editMsg.MessageID, "MessageID")
			assert.NotEmpty(t, editMsg.Text, "Message text should not be empty")
		})
	}
}

func TestHandleBackToStart_NoSubscription(t *testing.T) {
	t.Parallel()

	// gorm.ErrRecordNotFound and (nil, nil) both mean "no subscription row".
	tests := []struct {
		name string
		dbFn func(ctx context.Context, telegramID int64) (*database.Subscription, error)
	}{
		{
			name: "record not found",
			dbFn: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
				return nil, gorm.ErrRecordNotFound
			},
		},
		{
			name: "nil subscription",
			dbFn: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
				return nil, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()

			mockBot := testutil.NewBotAPI()
			mockDB := testutil.NewBotStoreFake()
			mockDB.GetByTelegramIDFunc = tt.dbFn

			cfg := &config.Config{
				TelegramBotToken: "test:token",
				TelegramAdminID:  0,
			}

			handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
			handler.handleBackToStart(ctx, 12345, "testuser", 100)

			require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
			editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
			require.True(t, ok, "Should be EditMessageTextConfig")
			assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID")
			assert.Equal(t, 100, editMsg.MessageID, "MessageID")
		})
	}
}

func TestHandleBackToStart_DatabaseError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewBotStoreFake()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database connection failed")
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.handleBackToStart(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent even on database error")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID should match")
}

func TestHandleMenuDonate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewBotStoreFake()

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.handleMenuDonate(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID")
	assert.Equal(t, 100, editMsg.MessageID, "MessageID")
	assert.Equal(t, "Markdown", editMsg.ParseMode, "ParseMode should be Markdown")
	assert.NotEmpty(t, editMsg.Text, "Message text should not be empty")
}

func TestHandleMenuHelp_WithSubscription(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewBotStoreFake()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			ID:             1,
			TelegramID:     12345,
			Username:       "testuser",
			Status:         "active",
			SubscriptionID: "abc123",
		}, nil
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.handleMenuHelp(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID")
	assert.Equal(t, 100, editMsg.MessageID, "MessageID")
	assert.Equal(t, "Markdown", editMsg.ParseMode, "ParseMode should be Markdown")
	assert.NotEmpty(t, editMsg.Text, "Message text should not be empty")
}

func TestHandleMenuHelp_NoSubscription(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewBotStoreFake()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.handleMenuHelp(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Contains(t, editMsg.Text, "нет активной подписки", "Should indicate no subscription")
}

func TestHandleMenuHelp_DatabaseError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewBotStoreFake()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database connection failed")
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
	}

	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	handler.handleMenuHelp(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent even on database error")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Contains(t, editMsg.Text, "Временная ошибка", "Should indicate temporary error")
}
