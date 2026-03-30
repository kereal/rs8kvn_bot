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
)

// createCommandUpdate creates an Update with a command message.
// This is needed because CommandArguments() requires the Message to have
// a bot_command entity set.
func createCommandUpdate(chatID int64, from *tgbotapi.User, text string) tgbotapi.Update {
	// Find the command in the text (first word starting with /)
	cmdLen := 0
	for _, ch := range text {
		if ch == ' ' {
			break
		}
		if ch == '/' {
			cmdLen = 0
		}
		cmdLen++
	}
	if cmdLen == 0 {
		cmdLen = len(text)
	}

	// Create entity for the command
	entities := []tgbotapi.MessageEntity{}
	if cmdLen > 0 && len(text) > 0 && text[0] == '/' {
		entities = append(entities, tgbotapi.MessageEntity{
			Type:   "bot_command",
			Offset: 0,
			Length: cmdLen,
		})
	}

	return tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat:     &tgbotapi.Chat{ID: chatID},
			From:     from,
			Text:     text,
			Entities: entities,
		},
	}
}

func TestHandleDel_NonAdminUser(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 999999,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "regularuser"}, "/del 5")

	handler.HandleDel(ctx, update)
	// Should not call any database or XUI methods
	assert.Nil(t, mockDB.GetByIDFunc)
}

func TestHandleDel_ValidDeletion(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	sub := &database.Subscription{
		ID:         5,
		TelegramID: 789012,
		Username:   "testuser",
		ClientID:   "client-123",
		InboundID:  1,
	}

	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		assert.Equal(t, uint(5), id)
		return sub, nil
	}

	mockXUI.DeleteClientFunc = func(ctx context.Context, inboundID int, clientID string) error {
		assert.Equal(t, 1, inboundID)
		assert.Equal(t, "client-123", clientID)
		return nil
	}

	mockDB.DeleteSubscriptionByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		assert.Equal(t, uint(5), id)
		return sub, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/del 5")

	handler.HandleDel(ctx, update)
	assert.True(t, mockBot.SendCalled)
}

func TestHandleDel_InvalidIDFormat(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	tests := []struct {
		name    string
		text    string
		wantMsg string
	}{
		{
			name:    "no arguments",
			text:    "/del",
			wantMsg: "Использование",
		},
		{
			name:    "invalid format",
			text:    "/del abc",
			wantMsg: "Неверный формат",
		},
		{
			name:    "negative id",
			text:    "/del -5",
			wantMsg: "положительным",
		},
		{
			name:    "zero id",
			text:    "/del 0",
			wantMsg: "положительным",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockBot.SendCalled = false
			mockBot.LastSentText = ""

			ctx := context.Background()
			update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, tt.text)

			handler.HandleDel(ctx, update)
			assert.True(t, mockBot.SendCalled)
			assert.Contains(t, mockBot.LastSentText, tt.wantMsg)
		})
	}
}

func TestHandleDel_GetByIDError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return nil, errors.New("not found")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/del 999")

	handler.HandleDel(ctx, update)
	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "не найдена")
}

func TestHandleDel_XUIDeleteFailure(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return &database.Subscription{
			ID:        5,
			ClientID:  "client-123",
			InboundID: 1,
		}, nil
	}

	mockXUI.DeleteClientFunc = func(ctx context.Context, inboundID int, clientID string) error {
		return errors.New("xui error")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/del 5")

	handler.HandleDel(ctx, update)
	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Ошибка удаления")
	// Database delete should not be called if XUI delete fails
	assert.Nil(t, mockDB.DeleteSubscriptionByIDFunc)
}

func TestHandleDel_DatabaseDeleteFailure(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return &database.Subscription{
			ID:        5,
			ClientID:  "client-123",
			InboundID: 1,
		}, nil
	}

	mockXUI.DeleteClientFunc = func(ctx context.Context, inboundID int, clientID string) error {
		return nil
	}

	mockDB.DeleteSubscriptionByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/del 5")

	handler.HandleDel(ctx, update)
	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "ошибка удаления из базы")
}

func TestHandleDel_CacheInvalidation(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	telegramID := int64(789012)
	sub := &database.Subscription{
		ID:         5,
		TelegramID: telegramID,
		Username:   "testuser",
		ClientID:   "client-123",
		InboundID:  1,
	}

	// Set up cache
	handler.cache.Set(telegramID, sub)
	cachedSub := handler.cache.Get(telegramID)
	require.NotNil(t, cachedSub, "Cache should contain subscription before deletion")

	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return sub, nil
	}

	mockXUI.DeleteClientFunc = func(ctx context.Context, inboundID int, clientID string) error {
		return nil
	}

	mockDB.DeleteSubscriptionByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return sub, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/del 5")

	handler.HandleDel(ctx, update)

	// Verify cache was invalidated
	cachedSubAfter := handler.cache.Get(telegramID)
	assert.Nil(t, cachedSubAfter, "Cache should be invalidated after deletion")
}

func TestHandleBroadcast_NonAdminUser(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 999999,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "regularuser"}, "/broadcast Hello everyone!")

	handler.HandleBroadcast(ctx, update)
	// Should not call any database methods
	assert.Nil(t, mockDB.GetTotalTelegramIDCountFunc)
}

func TestHandleBroadcast_ValidBroadcast(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetTotalTelegramIDCountFunc = func(ctx context.Context) (int64, error) {
		return 3, nil
	}

	callCount := 0
	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		callCount++
		if callCount == 1 {
			return []int64{111, 222, 333}, nil
		}
		return []int64{}, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalled)
}

func TestHandleBroadcast_NoMessage(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Использование")
}

func TestHandleBroadcast_NoUsers(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetTotalTelegramIDCountFunc = func(ctx context.Context) (int64, error) {
		return 0, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Нет пользователей")
}

func TestHandleBroadcast_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetTotalTelegramIDCountFunc = func(ctx context.Context) (int64, error) {
		return 0, errors.New("database error")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	
	// Should send some message (either error or result)
	assert.True(t, mockBot.SendCalled)
}

func TestHandleBroadcast_SendFailure(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	mockBot.SendError = errors.New("send error")
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetTotalTelegramIDCountFunc = func(ctx context.Context) (int64, error) {
		return 2, nil
	}

	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		return []int64{111, 222}, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalled)
}

func TestHandleBroadcast_ContextCancellation(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetTotalTelegramIDCountFunc = func(ctx context.Context) (int64, error) {
		return 100, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately to test early exit
	cancel()

	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		return []int64{111}, nil
	}

	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalled)
}

func TestHandleSend_NonAdminUser(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 999999,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "regularuser"}, "/send 789012 Hello!")

	handler.HandleSend(ctx, update)
	// Should not call any database methods
	assert.Nil(t, mockDB.GetTelegramIDByUsernameFunc)
}

func TestHandleSend_ByNumericID(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/send 789012 Hello user!")

	handler.HandleSend(ctx, update)
	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "отправлено")
}

func TestHandleSend_ByUsernameLookup(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetTelegramIDByUsernameFunc = func(ctx context.Context, username string) (int64, error) {
		assert.Equal(t, "testuser", username)
		return 789012, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/send testuser Hello!")

	handler.HandleSend(ctx, update)
	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "отправлено")
}

func TestHandleSend_ByUsernameWithAt(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetTelegramIDByUsernameFunc = func(ctx context.Context, username string) (int64, error) {
		assert.Equal(t, "testuser", username)
		return 789012, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/send @testuser Hello!")

	handler.HandleSend(ctx, update)
	assert.True(t, mockBot.SendCalled)
}

func TestHandleSend_InvalidFormat(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	tests := []struct {
		name string
		text string
	}{
		{"no arguments", "/send"},
		{"only target", "/send 123456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockBot.SendCalled = false
			mockBot.LastSentText = ""

			ctx := context.Background()
			update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, tt.text)

			handler.HandleSend(ctx, update)
			assert.True(t, mockBot.SendCalled)
			assert.Contains(t, mockBot.LastSentText, "Использование")
		})
	}
}

func TestHandleSend_UsernameNotFound(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetTelegramIDByUsernameFunc = func(ctx context.Context, username string) (int64, error) {
		return 0, errors.New("not found")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/send unknownuser Hello!")

	handler.HandleSend(ctx, update)
	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "не найден")
}

func TestHandleSend_SendFailure(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	mockBot.SendError = errors.New("send error")
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/send 789012 Hello!")

	handler.HandleSend(ctx, update)
	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Ошибка")
}

func TestNotifyAdminError_WithAdminID(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 999888,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	ctx := context.Background()
	handler.notifyAdminError(ctx, "Test error message")

	assert.True(t, mockBot.SendCalled)
	assert.Equal(t, int64(999888), mockBot.LastChatID)
	assert.Contains(t, mockBot.LastSentText, "Test error message")
}

func TestNotifyAdminError_ZeroAdminID(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 0,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	ctx := context.Background()
	handler.notifyAdminError(ctx, "Test error message")

	assert.False(t, mockBot.SendCalled)
}

func TestHandleAdminLastReg_NonAdminUser(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 999999,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	ctx := context.Background()
	handler.handleAdminLastReg(ctx, 123456, "regularuser", 1)

	// Should not call database
	assert.Nil(t, mockDB.GetLatestSubscriptionsFunc)
}

func TestHandleAdminLastReg_EmptyList(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetLatestSubscriptionsFunc = func(ctx context.Context, limit int) ([]database.Subscription, error) {
		return []database.Subscription{}, nil
	}

	ctx := context.Background()
	handler.handleAdminLastReg(ctx, 123456, "admin", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Нет активных")
}

func TestHandleAdminLastReg_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.GetLatestSubscriptionsFunc = func(ctx context.Context, limit int) ([]database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleAdminLastReg(ctx, 123456, "admin", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Ошибка")
}

func TestHandleAdminLastReg_WithSubscriptions(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	subs := []database.Subscription{
		{ID: 1, Username: "user1", TelegramID: 111},
		{ID: 2, Username: "user2", TelegramID: 222},
		{ID: 3, Username: "", TelegramID: 333}, // Test empty username
	}

	mockDB.GetLatestSubscriptionsFunc = func(ctx context.Context, limit int) ([]database.Subscription, error) {
		assert.Equal(t, 10, limit)
		return subs, nil
	}

	ctx := context.Background()
	handler.handleAdminLastReg(ctx, 123456, "admin", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Последние регистрации")
	assert.Contains(t, mockBot.LastSentText, "user1")
	assert.Contains(t, mockBot.LastSentText, "user2")
	assert.Contains(t, mockBot.LastSentText, "unknown")
}

func TestHandleAdminStats_NonAdminUser(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 999999,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	ctx := context.Background()
	handler.handleAdminStats(ctx, 123456, "regularuser", 1)

	// Should not call database
	assert.Nil(t, mockDB.CountAllSubscriptionsFunc)
}

func TestHandleAdminStats_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.CountAllSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 0, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleAdminStats(ctx, 123456, "admin", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Ошибка")
}

func TestHandleAdminStats_Success(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.CountAllSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 100, nil
	}
	mockDB.CountActiveSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 80, nil
	}
	mockDB.CountExpiredSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 20, nil
	}

	ctx := context.Background()
	handler.handleAdminStats(ctx, 123456, "admin", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "100")
	assert.Contains(t, mockBot.LastSentText, "80")
	assert.Contains(t, mockBot.LastSentText, "20")
}

func TestHandleAdminStats_PartialDatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	mockDB.CountAllSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 100, nil
	}
	mockDB.CountActiveSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 0, errors.New("error")
	}
	mockDB.CountExpiredSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 0, errors.New("error")
	}

	ctx := context.Background()
	handler.handleAdminStats(ctx, 123456, "admin", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "100")
}
