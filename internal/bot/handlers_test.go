package bot

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/utils"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	testutil.InitLogger(m)
	os.Exit(m.Run())
}

func TestNewHandler(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TrafficLimitGB:   100,
		XUIHost:          "http://localhost:2053",
		XUIInboundID:     1,
		XUISubPath:       "sub",
		TelegramBotToken: "test_token",
	}

	xuiClient, err := xui.NewClient(cfg.XUIHost, "admin", "password")
	require.NoError(t, err, "Failed to create XUI client")
	mockDB := testutil.NewMockDatabaseService()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, xuiClient, NewTestBotConfig())

	require.NotNil(t, handler, "NewHandler returned nil")
	assert.Equal(t, cfg, handler.cfg, "Config not set correctly")
	assert.NotNil(t, handler.rateLimiter, "RateLimiter should not be nil")
}

func TestGenerateInviteCode(t *testing.T) {
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

func TestHandler_ConfigField(t *testing.T) {
	cfg := &config.Config{
		TelegramBotToken: "123456:test_token",
		TelegramAdminID:  999888777,
		TrafficLimitGB:   50,
		XUIHost:          "http://test.local:8080",
		XUIInboundID:     5,
		XUISubPath:       "mysub",
	}

	xuiClient, err := xui.NewClient(cfg.XUIHost, "user", "pass")
	require.NoError(t, err, "Failed to create XUI client")

	handler := &Handler{
		cfg: cfg,
		xui: xuiClient,
	}

	assert.Equal(t, cfg, handler.cfg, "Handler.cfg not set correctly")
	assert.Equal(t, int64(999888777), handler.cfg.TelegramAdminID)
	assert.Equal(t, 50, handler.cfg.TrafficLimitGB)
}

func TestHandleBroadcast_MessageTooLong(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	longMessage := make([]byte, config.MaxTelegramMessageLen+1)
	for i := range longMessage {
		longMessage[i] = 'a'
	}

	ctx := context.Background()
	update := createCommandUpdate(123456, &tgbotapi.User{ID: 123456, UserName: "admin"}, "/broadcast "+string(longMessage))
	handler.HandleBroadcast(ctx, update)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "слишком длинное")
}

func TestHandleSend_RateLimit(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockDB.GetTelegramIDByUsernameFunc = func(ctx context.Context, username string) (int64, error) {
		return 999888777, nil
	}

	ctx := context.Background()
	adminID := int64(123456)

	update := createCommandUpdate(adminID, &tgbotapi.User{ID: adminID, UserName: "admin"}, "/send @testuser Test message")
	handler.HandleSend(ctx, update)

	update2 := createCommandUpdate(adminID, &tgbotapi.User{ID: adminID, UserName: "admin"}, "/send @testuser Second message")
	handler.HandleSend(ctx, update2)

	assert.True(t, mockBot.SendCalled)
}

func TestHandleSend_NoArguments(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123456}
	mockDB := testutil.NewMockDatabaseService()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())

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
	assert.Contains(t, mockBot.LastSentText, "Использование", "Should show usage instructions")
}

func TestHandleSend_OnlyTarget(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123456}
	mockDB := testutil.NewMockDatabaseService()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())

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
	assert.Contains(t, mockBot.LastSentText, "Использование", "Should show usage instructions")
}

// sendInviteLink tests are now in keyboard_test.go

// === sendInviteLink tests ===

func TestSendInviteLink_Success(t *testing.T) {
	mockDB := testutil.NewMockDatabaseService()
	mockBot := testutil.NewMockBotAPI()
	cfg := &config.Config{
		SiteURL:         "https://vpn.site",
		TelegramAdminID: 12345,
		TrafficLimitGB:  100,
	}
	handler := &Handler{
		cfg:       cfg,
		db:        mockDB,
		bot:       mockBot,
		botConfig: NewTestBotConfig(),
		cache:     NewSubscriptionCache(100, 5*time.Minute),
	}

	mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
		return &database.Invite{Code: "TESTCODE1", ReferrerTGID: referrerTGID}, nil
	}

	ctx := context.Background()
	handler.sendInviteLink(ctx, 12345, 99)

	assert.True(t, mockBot.SendCalled, "sendInviteLink should send a message")
	assert.Contains(t, mockBot.LastSentText, "TESTCODE1", "Message should contain invite code")
	assert.Contains(t, mockBot.LastSentText, "vpn.site", "Message should contain web link")
}

func TestSendInviteLink_DatabaseError(t *testing.T) {
	mockDB := testutil.NewMockDatabaseService()
	mockBot := testutil.NewMockBotAPI()
	cfg := &config.Config{
		SiteURL:         "https://vpn.site",
		TelegramAdminID: 12345,
		TrafficLimitGB:  100,
	}
	handler := NewHandler(mockBot, cfg, mockDB, testutil.NewMockXUIClient(), NewTestBotConfig())

	mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
		return nil, fmt.Errorf("database error")
	}

	ctx := context.Background()
	handler.sendInviteLink(ctx, 12345, 99)

	assert.True(t, mockBot.SendCalledSafe(), "sendInviteLink should send error message on DB error")
	assert.Contains(t, mockBot.LastSentText, "❌", "Error message should contain error emoji")
}

// === isAdmin edge cases ===

func TestHandler_IsAdmin_NegativeID(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 12345}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	assert.False(t, handler.isAdmin(-1), "isAdmin() should return false for negative ID")
	assert.False(t, handler.isAdmin(0), "isAdmin() should return false for zero ID")
}

func TestHandler_IsAdmin_ZeroAdminID(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 0}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	assert.False(t, handler.isAdmin(0), "isAdmin() should return false when admin ID is 0")
	assert.False(t, handler.isAdmin(12345), "isAdmin() should return false when admin ID is 0")
}

// === getUsername edge cases ===

func TestHandler_GetUsername_EmptyStrings(t *testing.T) {
	handler := &Handler{}

	// User with empty strings
	user := &tgbotapi.User{ID: 12345, UserName: "", FirstName: ""}
	result := handler.getUsername(user)
	assert.Equal(t, "user_12345", result, "getUsername() should use user_ID format when no names")

	// User with whitespace username
	user2 := &tgbotapi.User{ID: 67890, UserName: "  ", FirstName: "Test"}
	result2 := handler.getUsername(user2)
	// UserName is not empty (it's whitespace), so it should be returned
	assert.Equal(t, "  ", result2, "getUsername() should return username even if whitespace")
}

func TestHandler_CacheField(t *testing.T) {
	handler := &Handler{
		cache: NewSubscriptionCache(100, 5*time.Minute),
	}

	assert.NotNil(t, handler.cache, "Cache should not be nil")

	// Test cache operations
	sub := &database.Subscription{
		TelegramID: 12345,
		Username:   "testuser",
		Status:     "active",
	}
	handler.cache.Set(12345, sub)

	retrieved := handler.cache.Get(12345)
	require.NotNil(t, retrieved, "Cache should return stored subscription")
	assert.Equal(t, "testuser", retrieved.Username, "Username should match")
}

// === Rate limiter tests ===

func TestHandler_RateLimiter(t *testing.T) {
	handler := NewHandler(testutil.NewMockBotAPI(), &config.Config{}, testutil.NewMockDatabaseService(), nil, NewTestBotConfig())

	assert.NotNil(t, handler.rateLimiter, "Rate limiter should be initialized")

	// Test that rate limiter allows requests
	ctx := context.Background()
	assert.True(t, handler.rateLimiter.Wait(ctx, 12345), "Rate limiter should allow request")
}

// === Subscription cache integration ===

func TestHandler_CacheInvalidation(t *testing.T) {
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
	handler.invalidateCache(12345)

	// Verify it's gone
	assert.Nil(t, handler.cache.Get(12345), "Should not be in cache after invalidation")
}

// === Multiple subscription creation prevention ===

func TestHandler_SubscriptionCreationLock(t *testing.T) {
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
			mockBot := testutil.NewMockBotAPI()
			handler := &Handler{bot: mockBot}

			handler.handleCreateError(context.Background(), 12345, 100, "testuser", tt.err)

			assert.True(t, mockBot.SendCalled, "should send error message")
			assert.Contains(t, mockBot.LastSentText, tt.wantContain, "message should contain expected text")
		})
	}
}

func TestHandleUpdate_CommandRouting(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TrafficLimitGB:   100,
		XUIHost:          "http://localhost:2053",
		XUIInboundID:     1,
		TelegramBotToken: "test_token",
	}

	xuiClient, err := xui.NewClient(cfg.XUIHost, "admin", "password")
	require.NoError(t, err)

	tests := []struct {
		name        string
		update      tgbotapi.Update
		wantCommand string
	}{
		{
			name: "/start command",
			update: tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 111},
					From: &tgbotapi.User{ID: 111, UserName: "user1"},
					Text: "/start",
				},
			},
			wantCommand: "start",
		},
		{
			name: "/help command",
			update: tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 111},
					From: &tgbotapi.User{ID: 111, UserName: "user1"},
					Text: "/help",
				},
			},
			wantCommand: "help",
		},
		{
			name: "/invite command",
			update: tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 111},
					From: &tgbotapi.User{ID: 111, UserName: "user1"},
					Text: "/invite",
				},
			},
			wantCommand: "invite",
		},
		{
			name: "/refstats command",
			update: tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123456789},
					From: &tgbotapi.User{ID: 123456789, UserName: "admin"},
					Text: "/refstats",
				},
			},
			wantCommand: "refstats",
		},
		{
			name: "unknown command",
			update: tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 111},
					From: &tgbotapi.User{ID: 111, UserName: "user1"},
					Text: "/unknown",
				},
			},
			wantCommand: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockBot := testutil.NewMockBotAPI()
			mockDB := testutil.NewMockDatabaseService()
			handler := NewHandler(mockBot, cfg, mockDB, xuiClient, NewTestBotConfig())

			handler.HandleUpdate(context.Background(), tt.update)

			assert.True(t, mockBot.SendCalled, "should send response")
		})
	}
}

func TestHandleUpdate_NonCommandMessage(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TrafficLimitGB:   100,
		XUIHost:          "http://localhost:2053",
		XUIInboundID:     1,
		TelegramBotToken: "test_token",
	}

	xuiClient, err := xui.NewClient(cfg.XUIHost, "admin", "password")
	require.NoError(t, err)

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	handler := NewHandler(mockBot, cfg, mockDB, xuiClient, NewTestBotConfig())

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 111},
			From: &tgbotapi.User{ID: 111, UserName: "testuser"},
			Text: "hello world",
		},
	}

	handler.HandleUpdate(context.Background(), update)

	assert.True(t, mockBot.SendCalled, "should send response for non-command message")
	assert.Contains(t, mockBot.LastSentText, "/start", "should suggest /start command")
}

func TestHandleUpdate_CallbackQuery(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TrafficLimitGB:   100,
		XUIHost:          "http://localhost:2053",
		XUIInboundID:     1,
		TelegramBotToken: "test_token",
	}

	xuiClient, err := xui.NewClient(cfg.XUIHost, "admin", "password")
	require.NoError(t, err)

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, id int64) (*database.Subscription, error) {
		return &database.Subscription{
			ID:             1,
			TelegramID:     111,
			Username:       "testuser",
			SubscriptionID: "test-sub-id",
			TrafficLimit:   100,
			ExpiryTime:     time.Now().Add(24 * time.Hour),
			Status:         "active",
		}, nil
	}
	handler := NewHandler(mockBot, cfg, mockDB, xuiClient, NewTestBotConfig())

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

	assert.True(t, mockBot.SendCalled, "should handle callback query")
}
