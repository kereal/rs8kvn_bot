package bot

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/ratelimiter"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	testutil.InitLogger(m)
	os.Exit(m.Run())
}

func TestNewHandler(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
	}

	mockDB := testutil.NewDatabaseService()
	handler := NewHandler(testutil.NewBotAPI(), cfg, mockDB, NewTestBotConfig(), nil, "")

	require.NotNil(t, handler, "NewHandler returned nil")
	assert.Equal(t, cfg, handler.cfg, "Config not set correctly")
	assert.NotNil(t, handler.rateLimiter, "RateLimiter should not be nil")
}

func TestGenerateInviteCode(t *testing.T) {
	t.Parallel()

	code1, err := utils.GenerateInviteCode()
	require.NoError(t, err)
	code2, err := utils.GenerateInviteCode()
	require.NoError(t, err)

	assert.Len(t, code1, 8, "Expected code length 8")
	assert.NotEqual(t, code1, code2, "Expected different codes on consecutive calls")

	validChars := "0123456789abcdefghijklmnopqrstuvwxyz"
	for _, c := range code1 {
		assert.True(t, strings.ContainsRune(validChars, c), "Invalid character %c in code", c)
	}
}

// NOTE: Tests for sendInviteLink and handleBindTrial require real Telegram Bot API
// and cannot be unit tested without mocking tgbotapi.BotAPI.
// These functions are tested via integration tests with a real bot instance.
// See integration_test.go for integration tests.

func TestHandleBroadcast_MessageTooLong(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	admin := &tgbotapi.User{ID: 123456, UserName: "admin"}
	ctx := context.Background()

	// (a) A message just over one Telegram message is now accepted: the draft
	// is auto-split, so the handler must NOT reject it.
	justOver := make([]byte, config.MaxTelegramMessageLen+1)
	for i := range justOver {
		justOver[i] = 'a'
	}

	handler.HandleBroadcast(ctx, createCommandUpdate(123456, admin, "/broadcast"))
	handler.HandleBroadcastDraft(ctx, createTextUpdate(admin, "Тестовая рассылка"))
	handler.HandleBroadcastDraft(ctx, createTextUpdate(admin, string(justOver)))
	assert.True(t, handler.broadcastSessionActive(123456),
		"Handler should accept a message slightly over one Telegram message (moves to preview)")
	assert.NotContains(t, mockBot.LastSentTextSafe(), "слишком длинное",
		"Handler should not reject a message under the broadcast cap")

	// (b) A message over the hard broadcast cap is rejected.
	handler.HandleBroadcast(ctx, createCommandUpdate(123456, admin, "/broadcast"))
	handler.HandleBroadcastDraft(ctx, createTextUpdate(admin, "Тестовая рассылка"))

	tooLong := make([]byte, config.MaxTelegramMessageLen*20+1)
	for i := range tooLong {
		tooLong[i] = 'a'
	}

	handler.HandleBroadcastDraft(ctx, createTextUpdate(admin, string(tooLong)))
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "слишком длинное")
}

func TestHandleSend_RateLimit(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	mockDB.GetTelegramIDByUsernameFunc = func(ctx context.Context, username string) (int64, error) {
		return 999888777, nil
	}

	ctx := context.Background()
	adminID := int64(123456)

	update := createCommandUpdate(adminID, &tgbotapi.User{ID: adminID, UserName: "admin"}, "/send @testuser Test message")
	handler.HandleSend(ctx, update)

	update2 := createCommandUpdate(adminID, &tgbotapi.User{ID: adminID, UserName: "admin"}, "/send @testuser Second message")
	handler.HandleSend(ctx, update2)

	assert.True(t, mockBot.SendCalledSafe())
}

func TestHandleSend_NoArguments(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456},
			Text: "/send",
		},
	}

	handler.HandleSend(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Should send usage message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Использование", "Should show usage instructions")
}

func TestHandleSend_OnlyTarget(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456},
			Text: "/send 123",
		},
	}

	handler.HandleSend(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Should send usage message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Использование", "Should show usage instructions")
}

// sendInviteLink tests are now in keyboard_test.go

// === sendInviteLink tests ===

func TestSendInviteLink_Success(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	cfg := &config.Config{
		SiteURL:         "https://vpn.site",
		TelegramAdminID: 12345,
	}
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
		return &database.Invite{Code: "TESTCODE1", ReferrerTGID: referrerTGID}, nil
	}

	ctx := context.Background()
	handler.sendInviteLink(ctx, 12345, 99)

	assert.True(t, mockBot.SendCalledSafe(), "sendInviteLink should send a message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "TESTCODE1", "Message should contain invite code")
	assert.Contains(t, mockBot.LastSentTextSafe(), "vpn.site", "Message should contain web link")
}

func TestSendInviteLink_DatabaseError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	cfg := &config.Config{
		SiteURL:         "https://vpn.site",
		TelegramAdminID: 12345,
	}
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
		return nil, fmt.Errorf("database error")
	}

	ctx := context.Background()
	handler.sendInviteLink(ctx, 12345, 99)

	assert.True(t, mockBot.SendCalledSafe(), "sendInviteLink should send error message on DB error")
	assert.Contains(t, mockBot.LastSentTextSafe(), "❌", "Error message should contain error emoji")
}

// TestHandler_RateLimiter_EventuallyBlocks verifies the per-user limiter drains:
// a burst equal to the bucket capacity is allowed, the next request within the
// window is blocked. Calls are in-memory, so no meaningful refill happens.
func TestHandler_RateLimiter_EventuallyBlocks(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)

	for i := range config.RateLimiterMaxTokens {
		assert.True(t, handler.checkRateLimit(chatID), "request %d within capacity should be allowed", i)
	}

	assert.False(t, handler.checkRateLimit(chatID),
		"request beyond bucket capacity within the window must be blocked")
}

// === Subscription cache integration ===

func TestHandler_CacheInvalidation(t *testing.T) {
	t.Parallel()

	handler := &Handler{
		cache: NewSubscriptionCache(100, 5*time.Minute),
	}

	// Add to cache
	sub := &database.Subscription{
		TelegramID: 12345,
		Username:   "testuser",
		Status:     "active",
	}
	handler.cache.Set(12345, sub)

	// Verify it's there
	assert.NotNil(t, handler.cache.Get(12345), "Should be in cache")

	// Invalidate
	handler.invalidateCache(context.Background(), 12345)

	// Verify it's gone
	assert.Nil(t, handler.cache.Get(12345), "Should not be in cache after invalidation")
}

// === Multiple subscription creation prevention ===

func TestHandler_SubscriptionCreationLock(t *testing.T) {
	t.Parallel()

	handler := &Handler{
		inProgressSyncMap: sync.Map{},
	}

	// Simulate subscription in progress
	_, loaded := handler.inProgressSyncMap.LoadOrStore(12345, true)
	assert.False(t, loaded, "First LoadOrStore should return false (not loaded)")

	// Check that it's marked as in progress
	_, exists := handler.inProgressSyncMap.Load(12345)
	assert.True(t, exists, "Should be marked as in progress")

	// Try to add again - should return true (already loaded)
	_, loaded = handler.inProgressSyncMap.LoadOrStore(12345, true)
	assert.True(t, loaded, "Second LoadOrStore should return true (already loaded)")

	// Remove from in progress
	handler.inProgressSyncMap.Delete(12345)

	// Verify it's removed
	_, exists = handler.inProgressSyncMap.Load(12345)
	assert.False(t, exists, "Should not be in progress anymore")
}

// === handleCreateError tests ===

func TestHandleCreateError_AllErrorTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantContain string
	}{
		{"connection refused", fmt.Errorf("connection refused"), "Не удается подключиться к серверу"},
		{"request timeout", fmt.Errorf("request timeout"), "Не удается подключиться к серверу"},
		{"authentication failed", fmt.Errorf("authentication failed"), "Ошибка авторизации на сервере"},
		{"unauthorized access", fmt.Errorf("unauthorized access"), "Ошибка авторизации на сервере"},
		{"context canceled", fmt.Errorf("context canceled"), "Запрос был прерван"},
		{"no such host", fmt.Errorf("no such host"), "Ошибка подключения к серверу"},
		{"dial tcp", fmt.Errorf("dial tcp 127.0.0.1:2053"), "Ошибка подключения к серверу"},
		{"certificate verify", fmt.Errorf("certificate verify failed"), "Ошибка SSL/TLS сертификата"},
		{"TLS handshake", fmt.Errorf("TLS handshake failed"), "Ошибка SSL/TLS сертификата"},
		{"inbound not found", fmt.Errorf("inbound not found"), "Ошибка сервера при создании подписки"},
		{"client already exists", fmt.Errorf("client already exists"), "Ошибка сервера при создании подписки"},
		{"generic error", fmt.Errorf("some unknown error"), "Ошибка при создании подписки"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockBot := testutil.NewBotAPI()
			handler := &Handler{
				bot:    mockBot,
				sender: NewMessageSender(mockBot, ratelimiter.NewPerUserRateLimiter(float64(config.RateLimiterMaxTokens), float64(config.RateLimiterRefillRate))),
			}

			handler.handleCreateError(context.Background(), 12345, 100, "testuser", tt.err)

			assert.True(t, mockBot.SendCalledSafe(), "should send error message")
			assert.Contains(t, mockBot.LastSentTextSafe(), tt.wantContain, "message should contain expected text")
		})
	}
}

func TestHandleUpdate_CommandRouting(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
		SiteURL:          "https://vpn.site",
	}

	tests := []struct {
		name    string
		update  tgbotapi.Update
		wantText string
	}{
		{
			name: "/start command",
			update: tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 111},
					From: &tgbotapi.User{ID: 111, UserName: "user1"},
					Text: "/start",
					Entities: []tgbotapi.MessageEntity{
						{Type: "bot_command", Offset: 0, Length: 6},
					},
				},
			},
			wantText: "подписк",
		},
		{
			name: "/help command",
			update: tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 111},
					From: &tgbotapi.User{ID: 111, UserName: "user1"},
					Text: "/help",
					Entities: []tgbotapi.MessageEntity{
						{Type: "bot_command", Offset: 0, Length: 5},
					},
				},
			},
			wantText: "Справка",
		},
		{
			name: "/invite command",
			update: tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 111},
					From: &tgbotapi.User{ID: 111, UserName: "user1"},
					Text: "/invite",
					Entities: []tgbotapi.MessageEntity{
						{Type: "bot_command", Offset: 0, Length: 7},
					},
				},
			},
			wantText: "пригласительная",
		},
		{
			name: "/refstats command (admin)",
			update: tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123456789},
					From: &tgbotapi.User{ID: 123456789, UserName: "admin"},
					Text: "/refstats",
					Entities: []tgbotapi.MessageEntity{
						{Type: "bot_command", Offset: 0, Length: 9},
					},
				},
			},
			wantText: "Статистика рефералов",
		},
		{
			name: "unknown command",
			update: tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 111},
					From: &tgbotapi.User{ID: 111, UserName: "user1"},
					Text: "/unknown",
					Entities: []tgbotapi.MessageEntity{
						{Type: "bot_command", Offset: 0, Length: 8},
					},
				},
			},
			wantText: "Неизвестная команда",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockBot := testutil.NewBotAPI()
			mockDB := testutil.NewDatabaseService()

			// /invite needs GetOrCreateInvite
			if tt.name == "/invite command" {
				mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
					return &database.Invite{Code: code, ReferrerTGID: referrerTGID}, nil
				}
			}

			handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

			handler.HandleUpdate(context.Background(), tt.update)

			assert.True(t, mockBot.SendCalledSafe(), "should send response for %s", tt.name)
			assert.Contains(t, mockBot.LastSentTextSafe(), tt.wantText,
				"wrong handler dispatched for %s (want command %q)", tt.name, tt.wantText)
		})
	}
}

func TestHandleUpdate_AdminCommandsBlockedForNonAdmin(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
	}

	commandUpdate := func(chatID int64, text string) tgbotapi.Update {
		return tgbotapi.Update{
			Message: &tgbotapi.Message{
				Chat: &tgbotapi.Chat{ID: chatID},
				From: &tgbotapi.User{ID: chatID, UserName: "user1"},
				Text: text,
				Entities: []tgbotapi.MessageEntity{
					{Type: "bot_command", Offset: 0, Length: len(text)},
				},
			},
		}
	}

	// /refstats: non-admin gets an explicit denial, never stats.
	t.Run("refstats denied", func(t *testing.T) {
		t.Parallel()

		mockBot := testutil.NewBotAPI()
		handler := NewHandler(mockBot, cfg, testutil.NewDatabaseService(), NewTestBotConfig(), nil, "")

		handler.HandleUpdate(context.Background(), commandUpdate(111, "/refstats"))

		assert.True(t, mockBot.SendCalledSafe(), "non-admin /refstats should send a denial")
		assert.Contains(t, mockBot.LastSentTextSafe(), "только администратору",
			"non-admin must not receive referral stats")
	})

	// /send and /broadcast: silently ignored for non-admins (no user-visible action).
	for _, cmd := range []string{"/send", "/broadcast"} {
		t.Run(cmd+" silently ignored", func(t *testing.T) {
			t.Parallel()

			mockBot := testutil.NewBotAPI()
			handler := NewHandler(mockBot, cfg, testutil.NewDatabaseService(), NewTestBotConfig(), nil, "")

			handler.HandleUpdate(context.Background(), commandUpdate(111, cmd))

			assert.False(t, mockBot.SendCalledSafe(), "non-admin %s must not be served", cmd)
		})
	}
}

func TestHandleUpdate_NonCommandMessage(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
	}

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 111},
			From: &tgbotapi.User{ID: 111, UserName: "testuser"},
			Text: "hello world",
		},
	}

	handler.HandleUpdate(context.Background(), update)

	assert.True(t, mockBot.SendCalledSafe(), "should send response for non-command message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "/start", "should suggest /start command")
}

func TestHandleUpdate_CallbackQuery(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
	}

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, id int64) (*database.Subscription, error) {
		return &database.Subscription{
			ID:             1,
			TelegramID:     111,
			Username:       "testuser",
			SubscriptionID: "test-sub-id",
			ExpiresAt:      testutil.PtrTime(time.Now().Add(24 * time.Hour)),
			Status:         "active",
		}, nil
	}
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "callback_123",
			From: &tgbotapi.User{ID: 111, UserName: "testuser"},
			Message: &tgbotapi.Message{
				Chat: &tgbotapi.Chat{ID: 111},
			},
			Data: "create_subscription",
		},
	}

	handler.HandleUpdate(context.Background(), update)

	assert.True(t, mockBot.SendCalledSafe(), "should handle callback query")
}

// TestHandleUpdateMalformedStartDoesNotPanic verifies that HandleUpdate handles
// a malformed /start update without panicking.
func TestHandleUpdateMalformedStartDoesNotPanic(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
	}
	mockDB := testutil.NewDatabaseService()
	handler := NewHandler(testutil.NewBotAPI(), cfg, mockDB, NewTestBotConfig(), nil, "")

	// /start with nil From → HandleStart returns "nil from" error.
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Text: "/start",
			Chat: &tgbotapi.Chat{ID: 999},
			Entities: []tgbotapi.MessageEntity{
				{Type: "bot_command", Offset: 0, Length: 6},
			},
			// From is nil → triggers error path.
		},
	}

	handler.HandleUpdate(context.Background(), update)
}
