package bot

import (
	"context"
	"errors"
	"testing"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHandleBackToStart_WithActiveSubscription(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
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
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
	handler.handleBackToStart(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID")
	assert.Equal(t, 100, editMsg.MessageID, "MessageID")
	assert.NotEmpty(t, editMsg.Text, "Message text should not be empty")
}

func TestHandleBackToStart_NoSubscription(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
	handler.handleBackToStart(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID")
	assert.Equal(t, 100, editMsg.MessageID, "MessageID")
}

func TestHandleBackToStart_InactiveSubscription(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			ID:         1,
			TelegramID: 12345,
			Username:   "testuser",
			Status:     "inactive",
		}, nil
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
	handler.handleBackToStart(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID should match")
	assert.NotEmpty(t, editMsg.Text, "Message text should not be empty")
}

func TestHandleBackToStart_DatabaseError(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database connection failed")
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
	handler.handleBackToStart(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent even on database error")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID should match")
}

func TestHandleBackToStart_NilSubscription(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, nil
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
	handler.handleBackToStart(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
}

func TestHandleMenuDonate(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
	handler.handleMenuDonate(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID")
	assert.Equal(t, 100, editMsg.MessageID, "MessageID")
	assert.Equal(t, "Markdown", editMsg.ParseMode, "ParseMode should be Markdown")
	assert.NotEmpty(t, editMsg.Text, "Message text should not be empty")
}

func TestHandleMenuDonate_WithDifferentUsernames(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())

	testCases := []struct {
		name     string
		username string
	}{
		{"regular username", "testuser"},
		{"empty username", ""},
		{"unicode username", "测试用户"},
		{"long username", "very_long_username_for_testing"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler.handleMenuDonate(ctx, 12345, tc.username, 100)
			require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
		})
	}
}

func TestHandleMenuHelp_WithSubscription(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			ID:              1,
			TelegramID:      12345,
			Username:        "testuser",
			Status:          "active",
			SubscriptionID:  "abc123",
			SubscriptionURL: "https://example.com/sub/abc123",
		}, nil
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
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
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
	handler.handleMenuHelp(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Contains(t, editMsg.Text, "нет активной подписки", "Should indicate no subscription")
}

func TestHandleMenuHelp_DatabaseError(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database connection failed")
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
	handler.handleMenuHelp(ctx, 12345, "testuser", 100)

	require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent even on database error")
	editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Contains(t, editMsg.Text, "Временная ошибка", "Should indicate temporary error")
}

func TestHandleMenuHelp_VariousTrafficLimits(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name        string
		trafficGB   int
		description string
	}{
		{"low limit", 10, "10GB limit"},
		{"standard limit", 30, "30GB limit"},
		{"high limit", 100, "100GB limit"},
		{"unlimited", 0, "no limit"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockBot := testutil.NewMockBotAPI()
			mockDB := testutil.NewMockDatabaseService()
			mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
				return &database.Subscription{
					ID:              1,
					TelegramID:      12345,
					Username:        "testuser",
					Status:          "active",
					SubscriptionID:  "abc123",
					SubscriptionURL: "https://example.com/sub/abc123",
				}, nil
			}

			cfg := &config.Config{
				TelegramBotToken: "test:token",
				TelegramAdminID:  0,
				TrafficLimitGB:   tc.trafficGB,
			}

			handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
			handler.handleMenuHelp(ctx, 12345, "testuser", 100)

			require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
		})
	}
}

func TestHandleBackToStart_VariousMessageIDs(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name      string
		messageID int
	}{
		{"message ID 1", 1},
		{"message ID 100", 100},
		{"message ID 999", 999},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockBot := testutil.NewMockBotAPI()
			mockDB := testutil.NewMockDatabaseService()
			mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
				return &database.Subscription{
					ID:         1,
					TelegramID: 12345,
					Status:     "active",
				}, nil
			}

			cfg := &config.Config{
				TelegramBotToken: "test:token",
				TelegramAdminID:  0,
				TrafficLimitGB:   30,
			}

			handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
			handler.handleBackToStart(ctx, 12345, "testuser", tc.messageID)

			require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
			editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
			require.True(t, ok, "Should be EditMessageTextConfig")
			assert.Equal(t, tc.messageID, editMsg.MessageID, "MessageID should match")
		})
	}
}

func TestHandleMenuHelp_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			ID:              1,
			TelegramID:      12345,
			Status:          "active",
			SubscriptionURL: "https://example.com/sub/abc123",
		}, nil
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())

	// Should not panic with cancelled context
	handler.handleMenuHelp(ctx, 12345, "testuser", 100)

	if mockBot.LastChattableSafe() != nil {
		assert.NotNil(t, mockBot.LastChattableSafe(), "Message should be captured if sent")
	}
}

func TestHandleBackToStart_SendError(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockBot.SendError = errors.New("send failed")
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			ID:         1,
			TelegramID: 12345,
			Status:     "active",
		}, nil
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())

	// Should not panic on send error
	handler.handleBackToStart(ctx, 12345, "testuser", 100)
}

func TestHandleMenuDonate_SendError(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockBot.SendError = errors.New("send failed")
	mockDB := testutil.NewMockDatabaseService()

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())

	// Should not panic on send error
	handler.handleMenuDonate(ctx, 12345, "testuser", 100)
}

func TestHandleMenuHelp_SendError(t *testing.T) {
	ctx := context.Background()

	mockBot := testutil.NewMockBotAPI()
	mockBot.SendError = errors.New("send failed")
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			ID:              1,
			TelegramID:      12345,
			Status:          "active",
			SubscriptionURL: "https://example.com/sub/abc123",
		}, nil
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())

	// Should not panic on send error
	handler.handleMenuHelp(ctx, 12345, "testuser", 100)
}

func TestHandleBackToStart_VariousChatIDs(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name   string
		chatID int64
	}{
		{"positive chat ID", 12345},
		{"large chat ID", 999999999},
		{"negative chat ID (group)", -1001234567890},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockBot := testutil.NewMockBotAPI()
			mockDB := testutil.NewMockDatabaseService()
			mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
				return nil, gorm.ErrRecordNotFound
			}

			cfg := &config.Config{
				TelegramBotToken: "test:token",
				TelegramAdminID:  0,
				TrafficLimitGB:   30,
			}

			handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())
			handler.handleBackToStart(ctx, tc.chatID, "testuser", 100)

			require.NotNil(t, mockBot.LastChattableSafe(), "Message should be sent")
			editMsg, ok := mockBot.LastChattableSafe().(tgbotapi.EditMessageTextConfig)
			require.True(t, ok, "Should be EditMessageTextConfig")
			assert.Equal(t, tc.chatID, editMsg.ChatID, "ChatID should match")
		})
	}
}
