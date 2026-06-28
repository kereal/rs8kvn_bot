package bot

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestAdminHandler creates a Handler with a stub SubscriptionService
// wired to the provided mock objects. This eliminates the repeated two-line
// pattern of NewHandler + subscriptionService assignment across all admin tests.
func newTestAdminHandler(cfg *config.Config, mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient, mockBot *testutil.MockBotAPI) *Handler {
	h := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	xuiClients := map[uint]interfaces.XUIClient{1: mockXUI}
	nodes := []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", APIToken: "test-token", InboundIDs: "[1]", SubscriptionURL: "http://example.com/sub/"}}
	h.subscriptionService = service.NewSubscriptionService(mockDB, xuiClients, nil, nodes, cfg)
	// Wire cache invalidation for tests that manually set subscriptionService
	h.subscriptionService.SetInvalidateFunc(h.cache.Invalidate)
	return h
}

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
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 999999,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "regularuser"}, "/del 5")

	handler.HandleDel(ctx, update)
	// Should not call any database or XUI methods
	assert.Nil(t, mockDB.GetByIDFunc)
}

func TestHandleDel_ValidDeletion(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	xuiClients := map[uint]interfaces.XUIClient{1: mockXUI}
	nodes := []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", APIToken: "test-token", InboundIDs: "[1]", SubscriptionURL: "http://example.com/sub/"}}
	handler.subscriptionService = service.NewSubscriptionService(mockDB, xuiClients, nil, nodes, cfg)

	sub := &database.Subscription{
		ID:         5,
		TelegramID: 789012,
		Username:   "testuser",
		ClientID:   "client-123",
	}

	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		assert.Equal(t, uint(5), id)
		return sub, nil
	}

	mockDB.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return nil, nil
	}

	mockDB.DeleteSubscriptionByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		assert.Equal(t, uint(5), id)
		return sub, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/del 5")

	handler.HandleDel(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "удалена")
}

func TestHandleDel_InvalidIDFormat(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

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
			mockBot.SetSendCalled(false)
			mockBot.LastSentText = ""

			ctx := context.Background()
			update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, tt.text)

			handler.HandleDel(ctx, update)
			assert.True(t, mockBot.SendCalledSafe())
			assert.Contains(t, mockBot.LastSentTextSafe(), tt.wantMsg)
		})
	}
}

func TestHandleDel_GetByIDError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return nil, errors.New("not found")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/del 999")

	handler.HandleDel(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка удаления подписки")
}

func TestHandleDel_XUIDeleteFailure(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return &database.Subscription{
			ID:       5,
			ClientID: "client-123",
		}, nil
	}

	mockXUI.DeleteClientFunc = func(ctx context.Context, email string) error {
		return errors.New("xui error")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/del 5")

	handler.HandleDel(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка удаления")
}

func TestHandleDel_DatabaseDeleteFailure(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return &database.Subscription{
			ID:       5,
			ClientID: "client-123",
		}, nil
	}

	mockXUI.DeleteClientFunc = func(ctx context.Context, email string) error {
		return nil
	}

	var deleteByIDCalled bool
	mockDB.DeleteSubscriptionByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		deleteByIDCalled = true
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/del 5")

	handler.HandleDel(ctx, update)
	assert.True(t, deleteByIDCalled, "DeleteSubscriptionByID should have been called")
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка удаления")
}

func TestHandleDel_CacheInvalidation(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	telegramID := int64(789012)
	sub := &database.Subscription{
		ID:         5,
		TelegramID: telegramID,
		Username:   "testuser",
		ClientID:   "client-123",
	}

	// Set up cache
	handler.cache.Set(telegramID, sub)
	cachedSub := handler.cache.Get(telegramID)
	require.NotNil(t, cachedSub, "Cache should contain subscription before deletion")

	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return sub, nil
	}

	mockXUI.DeleteClientFunc = func(ctx context.Context, email string) error {
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
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 999999,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "regularuser"}, "/broadcast Hello everyone!")

	handler.HandleBroadcast(ctx, update)
	// Should not call any database methods
	assert.Nil(t, mockDB.GetTotalTelegramIDCountFunc)
}

func TestHandleBroadcast_ValidBroadcast(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

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
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Рассылка завершена")
}

func TestHandleBroadcast_NoMessage(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Использование")
}

func TestHandleBroadcast_NoUsers(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		return []int64{}, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Всего: 0")
}

func TestHandleBroadcast_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка")
}

func TestHandleBroadcast_SendFailure(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	mockBot.SendError = errors.New("send error")
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	callCount := 0
	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		callCount++
		if callCount == 1 {
			return []int64{111, 222}, nil
		}
		return []int64{}, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Рассылка завершена")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибок: 2")
}

func TestHandleBroadcast_ContextCancellation(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		return []int64{111}, nil
	}

	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalledSafe(), "Cancellation report must be sent to admin even on ctx cancel")
	assert.Contains(t, mockBot.LastSentTextSafe(), "прервана")
}

// TestHandleBroadcast_MultipleBatches tests broadcast with multiple batches of users
func TestHandleBroadcast_MultipleBatches(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	callCount := 0
	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		callCount++
		switch callCount {
		case 1:
			ids := make([]int64, 100)
			for i := 0; i < 100; i++ {
				ids[i] = int64(i + 1)
			}
			return ids, nil
		case 2:
			ids := make([]int64, 100)
			for i := 0; i < 100; i++ {
				ids[i] = int64(i + 101)
			}
			return ids, nil
		case 3:
			ids := make([]int64, 50)
			for i := 0; i < 50; i++ {
				ids[i] = int64(i + 201)
			}
			return ids, nil
		default:
			return []int64{}, nil
		}
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Equal(t, 4, callCount, "Should call GetTelegramIDsBatch 4 times (3 batches + 1 empty)")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Всего: 250")
}

// TestHandleBroadcast_BatchError tests broadcast when GetTelegramIDsBatch fails
func TestHandleBroadcast_BatchError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	callCount := 0
	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		callCount++
		if callCount == 1 {
			return []int64{111, 222}, nil
		}
		return nil, errors.New("database connection lost")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalledSafe(), "Should send error report")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка")
}

// TestHandleBroadcast_EmptyBatchAfterFirst tests handling of empty subsequent batches
func TestHandleBroadcast_EmptyBatchAfterFirst(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	callCount := 0
	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		callCount++
		if callCount == 1 {
			return []int64{111, 222}, nil
		}
		return []int64{}, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Всего: 2")
}

// TestHandleBroadcast_GetTelegramIDsBatchErrorOnFirstCall tests error on first batch call
func TestHandleBroadcast_GetTelegramIDsBatchErrorOnFirstCall(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		return nil, errors.New("database unavailable")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	handler.HandleBroadcast(ctx, update)
	assert.True(t, mockBot.SendCalledSafe(), "Should send error report")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка")
}

// TestHandleBroadcast_ConcurrentBroadcasts tests handling of concurrent broadcast attempts
func TestHandleBroadcast_ConcurrentBroadcasts(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	var callCount int64
	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		n := atomic.AddInt64(&callCount, 1)
		if n <= 2 {
			return []int64{111, 222, 333, 444, 555}, nil
		}
		return []int64{}, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast Test message")

	done1 := make(chan bool)
	done2 := make(chan bool)

	go func() {
		handler.HandleBroadcast(ctx, update)
		done1 <- true
	}()

	go func() {
		handler.HandleBroadcast(ctx, update)
		done2 <- true
	}()

	<-done1
	<-done2

	assert.True(t, mockBot.SendCalledSafe(), "Should have sent messages")
}

func TestHandleSend_NonAdminUser(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 999999,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "regularuser"}, "/send 789012 Hello!")

	handler.HandleSend(ctx, update)
	// Should not call any database methods
	assert.Nil(t, mockDB.GetTelegramIDByUsernameFunc)
}

func TestHandleSend_ByNumericID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/send 789012 Hello user!")

	handler.HandleSend(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "отправлено")
}

func TestHandleSend_ByUsernameLookup(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.GetTelegramIDByUsernameFunc = func(ctx context.Context, username string) (int64, error) {
		assert.Equal(t, "testuser", username)
		return 789012, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/send testuser Hello!")

	handler.HandleSend(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "отправлено")
}

func TestHandleSend_ByUsernameWithAt(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.GetTelegramIDByUsernameFunc = func(ctx context.Context, username string) (int64, error) {
		assert.Equal(t, "testuser", username)
		return 789012, nil
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/send @testuser Hello!")

	handler.HandleSend(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
}

func TestHandleSend_InvalidFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{"no arguments", "/send"},
		{"only target", "/send 123456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				TelegramAdminID: 123456,
			}
			mockDB := testutil.NewMockDatabaseService()
			mockXUI := testutil.NewMockXUIClient()
			mockBot := testutil.NewMockBotAPI()
			handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

			mockBot.SetSendCalled(false)
			mockBot.LastSentText = ""

			ctx := context.Background()
			update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, tt.text)

			handler.HandleSend(ctx, update)
			assert.True(t, mockBot.SendCalledSafe())
			assert.Contains(t, mockBot.LastSentTextSafe(), "Использование")
		})
	}
}

func TestHandleSend_UsernameNotFound(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.GetTelegramIDByUsernameFunc = func(ctx context.Context, username string) (int64, error) {
		return 0, errors.New("not found")
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/send unknownuser Hello!")

	handler.HandleSend(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "не найден")
}

func TestHandleSend_SendFailure(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	mockBot.SendError = errors.New("send error")
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/send 789012 Hello!")

	handler.HandleSend(ctx, update)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка")
}

func TestNotifyAdminError_WithAdminID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 999888,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	handler.notifyAdminError(ctx, "Test error message")

	assert.True(t, mockBot.SendCalledSafe())
	assert.Equal(t, int64(999888), mockBot.LastChatID)
	assert.Contains(t, mockBot.LastSentTextSafe(), "Test error message")
}

func TestHandleAdminLastReg_NonAdminUser(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 999999,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	handler.handleAdminLastReg(ctx, 123456, "regularuser", 1)

	// Should not call database
	assert.Nil(t, mockDB.GetLatestSubscriptionsFunc)
}

func TestHandleAdminLastReg_EmptyList(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.GetLatestSubscriptionsFunc = func(ctx context.Context, limit int) ([]database.Subscription, error) {
		return []database.Subscription{}, nil
	}

	ctx := context.Background()
	handler.handleAdminLastReg(ctx, 123456, "admin", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Нет активных")
}

func TestHandleAdminLastReg_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.GetLatestSubscriptionsFunc = func(ctx context.Context, limit int) ([]database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleAdminLastReg(ctx, 123456, "admin", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка")
}

func TestHandleAdminLastReg_WithSubscriptions(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

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

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Последние регистрации")
	assert.Contains(t, mockBot.LastSentTextSafe(), "user1")
	assert.Contains(t, mockBot.LastSentTextSafe(), "user2")
	assert.Contains(t, mockBot.LastSentTextSafe(), "unknown")
}

func TestHandleAdminStats_NonAdminUser(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 999999,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	handler.handleAdminStats(ctx, 123456, "regularuser", 1)

	// Should not call database
	assert.Nil(t, mockDB.CountAllSubscriptionsFunc)
}

func TestHandleAdminStats_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.CountAllSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 0, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleAdminStats(ctx, 123456, "admin", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка")
}

func TestHandleAdminStats_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	mockDB.CountAllSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 100, nil
	}
	mockDB.CountActiveSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 80, nil
	}

	ctx := context.Background()
	handler.handleAdminStats(ctx, 123456, "admin", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "100")
	assert.Contains(t, mockBot.LastSentTextSafe(), "80")
}

func TestHandleAdminStats_PartialDatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

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

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "100")
}

func TestHandleRefstats_NonAdminUser(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 999999,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "regularuser"}, "/refstats")

	handler.HandleRefstats(ctx, update)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "только администратору")
}

func TestHandleRefstats_EmptyReferrals(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/refstats")

	handler.HandleRefstats(ctx, update)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Нет данных о рефералах")
}

func TestHandleRefstats_WithMultipleReferrers(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	handler.referralCache.SetForTest(111, 10)
	handler.referralCache.SetForTest(222, 5)
	handler.referralCache.SetForTest(333, 20)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/refstats")

	handler.HandleRefstats(ctx, update)

	assert.True(t, mockBot.SendCalledSafe())
	text := mockBot.LastSentTextSafe()
	assert.Contains(t, text, "35")  // total referrals
	assert.Contains(t, text, "3")   // unique referrers
	assert.Contains(t, text, "333") // top referrer
	assert.Contains(t, text, "111")
	assert.Contains(t, text, "222")
	// Verify ordering: 333 (20) should appear before 111 (10) before 222 (5)
	idx333 := strings.Index(text, "333")
	idx111 := strings.Index(text, "111")
	idx222 := strings.Index(text, "222")
	assert.Less(t, idx333, idx111, "333 should appear before 111")
	assert.Less(t, idx111, idx222, "111 should appear before 222")
}

func TestHandleRefstats_Top10Limit(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	for i := 0; i < 15; i++ {
		handler.referralCache.SetForTest(int64(1000+i), int64(100-i*5))
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/refstats")

	handler.HandleRefstats(ctx, update)

	assert.True(t, mockBot.SendCalledSafe())
	text := mockBot.LastSentTextSafe()
	assert.Contains(t, text, "15") // unique referrers count

	// Count how many "ID" entries appear (should be exactly 10)
	idCount := strings.Count(text, "ID ")
	assert.Equal(t, 10, idCount, "Should only show top 10 referrers")

	// Top referrer (1000 with 100 referrals) should be present
	assert.Contains(t, text, "1000")
	// 11th referrer (1010 with 50 referrals) should NOT be present
	assert.NotContains(t, text, "1010")
}

func TestEscapeMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"no special chars", "hello world", "hello world"},
		{"underscore", "hello_world", "hello\\_world"},
		{"asterisk", "hello*world", "hello\\*world"},
		{"brackets", "[test](url)", "\\[test\\]\\(url\\)"},
		{"backticks", "`code`", "\\`code\\`"},
		{"tilde", "~~strike~~", "\\~\\~strike\\~\\~"},
		{"pipe", "a|b", "a\\|b"},
		{"dot", "file.txt", "file.txt"},
		{"exclamation", "wow!", "wow!"},
		{"plus", "a+b", "a\\+b"},
		{"minus", "a-b", "a\\-b"},
		{"equals", "a=b", "a\\=b"},
		{"hash", "#heading", "\\#heading"},
		{"greater than", "a>b", "a\\>b"},
		{"curly braces", "{a}", "\\{a\\}"},
		{"caret", "a^b", "a\\^b"},
		{"all special chars", "_*[test](url)`~>#+-=|{}^", "\\_\\*\\[test\\]\\(url\\)\\`\\~\\>\\#\\+\\-\\=\\|\\{\\}\\^"},
		{"cyrillic text", "Привет мир", "Привет мир"},
		{"cyrillic with special", "Привет_мир", "Привет\\_мир"},
		{"multiple underscores", "a_b_c", "a\\_b\\_c"},
		{"exclamation not escaped", "Hello!", "Hello!"},
		{"dot not escaped", "file.txt", "file.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := utils.EscapeMarkdown(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
