package bot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestHandleDel_NonAdminUser(t *testing.T) {

	cfg := &config.Config{
		TelegramAdminID: 999999,
	}
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, mockXUI, mockBot)

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "regularuser"}, "/broadcast Hello everyone!")

	handler.HandleBroadcast(ctx, update)
	// Should not call any database methods
	assert.Nil(t, mockDB.GetTotalTelegramIDCountFunc)
	assert.False(t, handler.broadcastSessionActive(123456))
}

func TestHandleBroadcast_StartsSession(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)
	admin := &tgbotapi.User{ID: 123456, UserName: "admin"}

	handler.HandleBroadcast(context.Background(), createCommandUpdate(123456, admin, "/broadcast"))

	assert.True(t, handler.broadcastSessionActive(123456), "session should be active after /broadcast")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Отправьте сообщение")
}

func TestHandleBroadcast_DraftPreviewShowsButtons(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)
	admin := &tgbotapi.User{ID: 123456, UserName: "admin"}

	handler.HandleBroadcast(context.Background(), createCommandUpdate(123456, admin, "/broadcast"))

	// Capture all chatables to verify ParseMode and buttons.
	var captured []tgbotapi.Chattable
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		captured = append(captured, c)
		return tgbotapi.Message{MessageID: 1}, nil
	}

	draft := "Привет *все*!\nЭто _многострочное_ сообщение."
	handler.HandleBroadcastDraft(context.Background(), createTextUpdate(admin, draft))

	// Preview must use MarkdownV2.
	var previewed bool
	for _, c := range captured {
		if mc, ok := c.(tgbotapi.MessageConfig); ok && mc.ParseMode == "MarkdownV2" {
			previewed = true
			assert.Equal(t, utils.EscapeMarkdownV2(draft), mc.Text)
		}
	}
	assert.True(t, previewed, "draft preview must be sent with MarkdownV2")

	// Confirm/cancel buttons must be present on the final message.
	last, ok := mockBot.LastChattableSafe().(tgbotapi.MessageConfig)
	require.True(t, ok)
	require.NotNil(t, last.ReplyMarkup)
	kb, ok := last.ReplyMarkup.(tgbotapi.InlineKeyboardMarkup)
	require.True(t, ok)
	assert.Equal(t, "broadcast_confirm", *kb.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "broadcast_cancel", *kb.InlineKeyboard[0][1].CallbackData)

	// Session moved to preview stage.
	s := handler.getBroadcastSession(123456)
	require.NotNil(t, s)
	assert.Equal(t, broadcastStagePreview, s.stage)
	assert.Equal(t, draft, s.text)
}

func TestHandleBroadcast_AutoEscapesSpecialChars(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)
	admin := &tgbotapi.User{ID: 123456, UserName: "admin"}

	handler.HandleBroadcast(context.Background(), createCommandUpdate(123456, admin, "/broadcast"))

	// Dots, exclamation marks and broken markdown must not require manual escaping.
	draft := "Привет всем! Цены на vpn.ru обновлены. broken *bold"
	handler.HandleBroadcastDraft(context.Background(), createTextUpdate(admin, draft))

	// Preview must be sent (escaped) and session must advance to the preview stage.
	var previewed bool
	for _, c := range mockBot.GetAllSentMessages() {
		if strings.Contains(c.Text, `Привет всем\!`) {
			previewed = true
			assert.Contains(t, c.Text, `vpn\.ru`)
			assert.Contains(t, c.Text, `broken \*bold`)
		}
	}
	assert.True(t, previewed, "escaped draft preview must be sent")

	s := handler.getBroadcastSession(123456)
	require.NotNil(t, s)
	assert.Equal(t, broadcastStagePreview, s.stage)
}

func TestHandleBroadcast_DraftTooLong(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)
	admin := &tgbotapi.User{ID: 123456, UserName: "admin"}

	handler.HandleBroadcast(context.Background(), createCommandUpdate(123456, admin, "/broadcast"))

	long := make([]byte, config.MaxTelegramMessageLen+1)
	for i := range long {
		long[i] = 'a'
	}
	handler.HandleBroadcastDraft(context.Background(), createTextUpdate(admin, string(long)))

	assert.Contains(t, mockBot.LastSentTextSafe(), "слишком длинное")
	// Session stays in awaiting stage so admin can resend a shorter draft.
	s := handler.getBroadcastSession(123456)
	require.NotNil(t, s)
	assert.Equal(t, broadcastStageAwaitingDraft, s.stage)
}

func TestHandleBroadcast_CancelClearsSession(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)
	admin := &tgbotapi.User{ID: 123456, UserName: "admin"}

	handler.HandleBroadcast(context.Background(), createCommandUpdate(123456, admin, "/broadcast"))
	handler.HandleBroadcastDraft(context.Background(), createTextUpdate(admin, "/cancel"))

	assert.False(t, handler.broadcastSessionActive(123456))
	assert.Contains(t, mockBot.LastSentTextSafe(), "отменена")
}

// confirmBroadcast prepares a preview-stage session and runs the confirm handler.
func confirmBroadcast(h *Handler, mockBot *testutil.BotAPI, text string) {
	h.broadcastSessions[123456] = &broadcastSession{stage: broadcastStagePreview, text: text}
	h.handleBroadcastConfirm(context.Background(), 123456)
}

func TestHandleBroadcast_ConfirmSendsToAll(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)

	callCount := 0
	handler.db.(*testutil.DatabaseService).GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		callCount++
		if callCount == 1 {
			return []int64{111, 222, 333}, nil
		}
		return []int64{}, nil
	}

	confirmBroadcast(handler, mockBot, "Test *message*")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Рассылка завершена")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Всего: 3")
	assert.False(t, handler.broadcastSessionActive(123456), "session cleared after confirm")
}

func TestHandleBroadcast_ConfirmMultipleBatches(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)

	callCount := 0
	handler.db.(*testutil.DatabaseService).GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		callCount++
		switch callCount {
		case 1:
			ids := make([]int64, 100)
			for i := range ids {
				ids[i] = int64(i + 1)
			}
			return ids, nil
		case 2:
			ids := make([]int64, 100)
			for i := range ids {
				ids[i] = int64(i + 101)
			}
			return ids, nil
		case 3:
			ids := make([]int64, 50)
			for i := range ids {
				ids[i] = int64(i + 201)
			}
			return ids, nil
		default:
			return []int64{}, nil
		}
	}

	confirmBroadcast(handler, mockBot, "Test message")
	assert.Equal(t, 4, callCount, "should call GetTelegramIDsBatch 4 times (3 batches + 1 empty)")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Всего: 250")
}

func TestHandleBroadcast_ConfirmDatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)

	handler.db.(*testutil.DatabaseService).GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		return nil, errors.New("database error")
	}

	confirmBroadcast(handler, mockBot, "Test message")
	assert.True(t, mockBot.SendCalledSafe(), "should send error report")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка")
}

func TestHandleBroadcast_ConfirmSeparatesBlocked(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)

	// 111 -> blocked, 222 -> generic error, 333 -> success.
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		mc, ok := c.(tgbotapi.MessageConfig)
		if !ok {
			return tgbotapi.Message{}, nil
		}
		switch mc.ChatID {
		case 111:
			return tgbotapi.Message{}, errors.New("Forbidden: bot was blocked by the user")
		case 222:
			return tgbotapi.Message{}, errors.New("send error")
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	callCount := 0
	handler.db.(*testutil.DatabaseService).GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		callCount++
		if callCount == 1 {
			return []int64{111, 222, 333}, nil
		}
		return []int64{}, nil
	}

	confirmBroadcast(handler, mockBot, "Test message")
	report := mockBot.LastSentTextSafe()
	assert.Contains(t, report, "Рассылка завершена")
	assert.Contains(t, report, "Отправлено: 1")
	assert.Contains(t, report, "Заблокировали бота: 1")
	assert.Contains(t, report, "Ошибок: 1")
	assert.Contains(t, report, "Всего: 3")
}

func TestHandleBroadcast_ConfirmSendFailure(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	mockBot.SendError = errors.New("send error")
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)

	callCount := 0
	handler.db.(*testutil.DatabaseService).GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		callCount++
		if callCount == 1 {
			return []int64{111, 222}, nil
		}
		return []int64{}, nil
	}

	confirmBroadcast(handler, mockBot, "Test message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Рассылка завершена")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибок: 2")
}

func TestHandleBroadcast_ConfirmContextCancellation(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	handler.db.(*testutil.DatabaseService).GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		return []int64{111}, nil
	}

	handler.broadcastSessions[123456] = &broadcastSession{stage: broadcastStagePreview, text: "Test message"}
	handler.handleBroadcastConfirm(ctx, 123456)
	assert.True(t, mockBot.SendCalledSafe(), "cancellation report must be sent even on ctx cancel")
	assert.Contains(t, mockBot.LastSentTextSafe(), "прервана")
}

func TestSplitMessage_BelowLimit(t *testing.T) {
	t.Parallel()
	chunks := splitMessage("short", 4096)
	assert.Equal(t, []string{"short"}, chunks)
}

func TestSplitMessage_LineBoundary(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("a", 10) + "\n" + strings.Repeat("b", 10)
	chunks := splitMessage(text, 15)
	require.Len(t, chunks, 2)
	assert.Equal(t, strings.Repeat("a", 10), chunks[0])
	assert.Equal(t, strings.Repeat("b", 10), chunks[1])
}

func TestSplitMessage_HardSplitLongLine(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("x", 100)
	chunks := splitMessage(text, 40)
	require.Len(t, chunks, 3)
	assert.Equal(t, strings.Repeat("x", 40), chunks[0])
	assert.Equal(t, strings.Repeat("x", 40), chunks[1])
	assert.Equal(t, strings.Repeat("x", 20), chunks[2])
}

func TestSplitMessage_HardSplitMultibyte(t *testing.T) {
	t.Parallel()
	// 30 Cyrillic runes, 2 bytes each = 60 bytes. maxLen=25 would cut mid-rune with byte slicing.
	text := strings.Repeat("я", 30)
	chunks := splitMessage(text, 25)
	require.Len(t, chunks, 3)
	for _, c := range chunks {
		assert.True(t, utf8.ValidString(c), "chunk must remain valid UTF-8: %q", c)
	}
	assert.Equal(t, text, strings.Join(chunks, ""))
}

func TestHandleBroadcast_ConfirmChunking(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, testutil.NewDatabaseService(), testutil.NewXUIClient(), mockBot)

	handler.db.(*testutil.DatabaseService).GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		if offset == 0 {
			return []int64{111, 222}, nil
		}
		return []int64{}, nil
	}

	// 2 users, message > 4096 -> each gets 2 chunks.
	text := strings.Repeat("строка\n", 500) // ~3500 chars, bump to exceed
	text = strings.Repeat("строка\n", 600)  // ~4200 chars
	confirmBroadcast(handler, mockBot, text)

	assert.GreaterOrEqual(t, mockBot.SendCountSafe(), 4, "2 users x 2 chunks")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Рассылка завершена")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Всего: 2")
}

func TestHandleSend_NonAdminUser(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 999999,
	}
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
			t.Parallel()
			cfg := &config.Config{
				TelegramAdminID: 123456,
			}
			mockDB := testutil.NewDatabaseService()
			mockXUI := testutil.NewXUIClient()
			mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
	mockDB := testutil.NewDatabaseService()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
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
			t.Parallel()
			result := utils.EscapeMarkdown(tt.input)
			assert.Equal(t, tt.expected, result)

		})
	}
}
