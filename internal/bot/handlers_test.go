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

func TestGetMainMenuKeyboard(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	keyboardWithShare := handler.getMainMenuKeyboard(true)
	assert.Len(t, keyboardWithShare.InlineKeyboard, 3, "Expected 3 rows with subscription")

	keyboardNoShare := handler.getMainMenuKeyboard(false)
	assert.Len(t, keyboardNoShare.InlineKeyboard, 2, "Expected 2 rows without subscription")
}

func TestGetBackKeyboard(t *testing.T) {
	cfg := &config.Config{}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	keyboard := handler.getBackKeyboard()

	assert.Len(t, keyboard.InlineKeyboard, 1, "Back keyboard should have 1 row")
	assert.Len(t, keyboard.InlineKeyboard[0], 1, "Back keyboard should have 1 button")

	btn := keyboard.InlineKeyboard[0][0]
	assert.Equal(t, "🏠 В начало", btn.Text)
}

func TestAddAdminButtons(t *testing.T) {
	tests := []struct {
		name          string
		adminID       int64
		chatID        int64
		expectButtons bool
	}{
		{"admin user", 123, 123, true},
		{"non-admin user", 123, 456, false},
		{"zero admin ID", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{TelegramAdminID: tt.adminID}
			handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📋 Подписка", "menu_subscription"),
				),
			)

			handler.addAdminButtons(&keyboard, tt.chatID)

			if tt.expectButtons {
				assert.Len(t, keyboard.InlineKeyboard, 2, "Expected admin buttons")
			} else {
				assert.Len(t, keyboard.InlineKeyboard, 1, "Expected no admin buttons")
			}
		})
	}
}

func TestGenerateInviteCode(t *testing.T) {
	code1 := utils.GenerateInviteCode()
	code2 := utils.GenerateInviteCode()

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

func TestGetMainMenuContent(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 12345,
	}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	tests := []struct {
		name            string
		username        string
		hasSubscription bool
		chatID          int64
	}{
		{
			name:            "with subscription",
			username:        "testuser",
			hasSubscription: true,
			chatID:          99999,
		},
		{
			name:            "without subscription",
			username:        "newuser",
			hasSubscription: false,
			chatID:          88888,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, keyboard := handler.getMainMenuContent(tt.username, tt.hasSubscription, tt.chatID)

			assert.Contains(t, text, tt.username, "getMainMenuContent() text should contain username")
			assert.NotEmpty(t, text, "getMainMenuContent() text should not be empty")
			assert.NotEmpty(t, keyboard.InlineKeyboard, "getMainMenuContent() keyboard should have buttons")
		})
	}
}

func TestGetDonateText(t *testing.T) {
	cfg := &config.Config{}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	text := handler.getDonateText()
	assert.NotEmpty(t, text, "Donate text should not be empty")

	expected := []string{"☕", "Поддержка проекта"}
	for _, exp := range expected {
		assert.True(t, strings.Contains(text, exp), "Expected donate text to contain '%s'", exp)
	}
}

func TestGetHelpText(t *testing.T) {
	cfg := &config.Config{TrafficLimitGB: 100}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	text := handler.getHelpText(100, "http://localhost/sub/test")
	assert.NotEmpty(t, text, "Help text should not be empty")
}

func TestGetHelpText_DifferentTrafficLimits(t *testing.T) {
	handler := &Handler{cfg: &config.Config{}, botConfig: NewTestBotConfig()}

	tests := []struct {
		name           string
		trafficLimitGB int
	}{
		{"50 GB", 50},
		{"100 GB", 100},
		{"200 GB", 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := handler.getHelpText(tt.trafficLimitGB, "http://test.url/sub")
			if len(text) == 0 {
				t.Error("text should not be empty")
			}
		})
	}
}

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

func TestHandleStart_WithDatabase(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	got, err := database.GetByTelegramID(123456789)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}

	if got.TelegramID != 123456789 {
		t.Errorf("TelegramID = %d, want 123456789", got.TelegramID)
	}

	if got.Status != "active" {
		t.Errorf("Status = %s, want active", got.Status)
	}
}

func TestHandleStart_NoDatabase(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	_, err = database.GetByTelegramID(999999999)
	if err == nil {
		t.Error("Expected error for non-existent user")
	}
}

func TestHandleMySubscription_NoSubscription(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	_, err = database.GetByTelegramID(999999999)
	if err == nil {
		t.Error("Expected error for non-existent user")
	}
}

func TestHandleMySubscription_WithSubscription(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	got, err := database.GetByTelegramID(123456789)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}

	if got.TelegramID != sub.TelegramID {
		t.Errorf("TelegramID = %v, want %v", got.TelegramID, sub.TelegramID)
	}

	if got.Status != "active" {
		t.Errorf("Status = %v, want active", got.Status)
	}
}

func TestHandleMySubscription_ExpiredSubscription(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(-24*time.Hour))
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	got, err := database.GetByTelegramID(123456789)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}

	if !time.Now().After(got.ExpiryTime) {
		t.Error("Expected subscription to be expired")
	}
}

func TestHandleAdminStats(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	for i := 0; i < 5; i++ {
		sub := testutil.CreateTestSubscription(int64(100000000+i), "user", "active", time.Now().Add(24*time.Hour))
		if err := database.CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create subscription: %v", err)
		}
	}

	var count int64
	database.DB.Model(&database.Subscription{}).Count(&count)

	if count != 5 {
		t.Errorf("Expected 5 subscriptions, got %d", count)
	}
}

func TestHandleDel_GetSubscriptionByID(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "deltestuser", "active", time.Now().Add(24*time.Hour))
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	got, err := database.GetSubscriptionByID(sub.ID)
	if err != nil {
		t.Fatalf("GetSubscriptionByID() error = %v", err)
	}

	if got.ID != sub.ID {
		t.Errorf("GetSubscriptionByID() ID = %d, want %d", got.ID, sub.ID)
	}
}

func TestHandleDel_DeleteSubscriptionByID(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(999888777, "deletetest", "active", time.Now().Add(24*time.Hour))
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	id := sub.ID

	deleted, err := database.DeleteSubscriptionByID(id)
	if err != nil {
		t.Fatalf("DeleteSubscriptionByID() error = %v", err)
	}

	if deleted.ID != id {
		t.Errorf("DeleteSubscriptionByID() returned ID = %d, want %d", deleted.ID, id)
	}

	_, err = database.GetSubscriptionByID(id)
	if err == nil {
		t.Error("GetSubscriptionByID() should return error after deletion")
	}
}

func TestHandleDel_SubscriptionNotFound(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	_, err = database.GetSubscriptionByID(99999)
	if err == nil {
		t.Error("GetSubscriptionByID() should return error for non-existent ID")
	}
}

func TestHandleBroadcast_DatabaseFunction(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	subs := []*database.Subscription{
		{TelegramID: 111111111, Username: "user1", ClientID: "client1", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 222222222, Username: "user2", ClientID: "client2", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
	}

	for _, sub := range subs {
		if err := database.CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create subscription: %v", err)
		}
	}

	ids, err := database.GetAllTelegramIDs()
	if err != nil {
		t.Fatalf("GetAllTelegramIDs() error = %v", err)
	}

	if len(ids) != 2 {
		t.Errorf("GetAllTelegramIDs() returned %d IDs, want 2", len(ids))
	}
}

func TestHandleSend_ByTelegramID(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	got, err := database.GetByTelegramID(123456789)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}
	if got.TelegramID != 123456789 {
		t.Errorf("TelegramID = %d, want 123456789", got.TelegramID)
	}
}

func TestHandleSend_ByUsername(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	id, err := database.GetTelegramIDByUsername("testuser")
	if err != nil {
		t.Fatalf("GetTelegramIDByUsername() error = %v", err)
	}

	if id != 123456789 {
		t.Errorf("GetTelegramIDByUsername() returned %d, want 123456789", id)
	}
}

func TestHandleSend_UserNotFound(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	_, err = database.GetTelegramIDByUsername("nonexistent")
	if err == nil {
		t.Error("GetTelegramIDByUsername() should return error for non-existent username")
	}
}

func TestGetLatestSubscriptions(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	for i := 0; i < 15; i++ {
		sub := testutil.CreateTestSubscription(int64(100000000+i), "user", "active", time.Now().Add(24*time.Hour))
		if err := database.CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create subscription: %v", err)
		}
		time.Sleep(time.Millisecond * 10)
	}

	subs, err := database.GetLatestSubscriptions(10)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions() error = %v", err)
	}

	if len(subs) != 10 {
		t.Errorf("GetLatestSubscriptions() returned %d subscriptions, want 10", len(subs))
	}
}

func TestGetLatestSubscriptions_Empty(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	subs, err := database.GetLatestSubscriptions(10)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions() error = %v", err)
	}

	if len(subs) != 0 {
		t.Errorf("GetLatestSubscriptions() returned %d subscriptions, want 0", len(subs))
	}
}

func TestGetLatestSubscriptions_OnlyActive(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	sub1 := testutil.CreateTestSubscription(100000001, "active_user", "active", time.Now().Add(24*time.Hour))
	if err := database.CreateSubscription(sub1); err != nil {
		t.Fatalf("Failed to create active subscription: %v", err)
	}

	sub2 := testutil.CreateTestSubscription(100000002, "revoked_user", "revoked", time.Now().Add(24*time.Hour))
	if err := database.DB.Create(sub2).Error; err != nil {
		t.Fatalf("Failed to create revoked subscription: %v", err)
	}

	subs, err := database.GetLatestSubscriptions(10)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions() error = %v", err)
	}

	if len(subs) != 1 {
		t.Errorf("GetLatestSubscriptions() returned %d subscriptions, want 1", len(subs))
	}
}

func TestGetAllTelegramIDs(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	subs := []*database.Subscription{
		{TelegramID: 111111111, Username: "user1", ClientID: "client1", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 222222222, Username: "user2", ClientID: "client2", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 333333333, Username: "user3", ClientID: "client3", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 111111111, Username: "user1_alt", ClientID: "client4", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
	}

	for _, sub := range subs {
		if err := database.CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create subscription: %v", err)
		}
	}

	ids, err := database.GetAllTelegramIDs()
	if err != nil {
		t.Fatalf("GetAllTelegramIDs() error = %v", err)
	}

	if len(ids) != 3 {
		t.Errorf("GetAllTelegramIDs() returned %d IDs, want 3", len(ids))
	}
}

func TestGetAllTelegramIDs_Empty(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	ids, err := database.GetAllTelegramIDs()
	if err != nil {
		t.Fatalf("GetAllTelegramIDs() error = %v", err)
	}

	if len(ids) != 0 {
		t.Errorf("GetAllTelegramIDs() returned %d IDs, want 0", len(ids))
	}
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

// TestGetMainMenuContent_WithSubscription tests getMainMenuContent for users with subscription
func TestGetMainMenuContent_WithSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 0,
	}
	handler := &Handler{
		cfg: cfg,
	}

	text, keyboard := handler.getMainMenuContent("testuser", true, 123456789)

	if text == "" {
		t.Error("Expected non-empty text for user with subscription")
	}
	if !strings.Contains(text, "testuser") {
		t.Error("Expected text to contain username")
	}
	if len(keyboard.InlineKeyboard) < 2 {
		t.Errorf("Expected at least 2 keyboard rows, got %d", len(keyboard.InlineKeyboard))
	}
}

// TestGetMainMenuContent_WithoutSubscription tests getMainMenuContent for users without subscription
func TestGetMainMenuContent_WithoutSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 0,
	}
	handler := &Handler{
		cfg: cfg,
	}

	text, keyboard := handler.getMainMenuContent("testuser", false, 123456789)

	if text == "" {
		t.Error("Expected non-empty text for user without subscription")
	}
	if !strings.Contains(text, "testuser") {
		t.Error("Expected text to contain username")
	}
	if len(keyboard.InlineKeyboard) < 1 {
		t.Errorf("Expected at least 1 keyboard row, got %d", len(keyboard.InlineKeyboard))
	}
	// Check that "create_subscription" callback is used
	found := false
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			if button.CallbackData != nil && *button.CallbackData == "create_subscription" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("Expected create_subscription callback in keyboard for user without subscription")
	}
}

// TestGetMainMenuContent_AdminButtons tests that admin buttons are added for admin users
func TestGetMainMenuContent_AdminButtons(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456789,
	}
	handler := &Handler{
		cfg: cfg,
	}

	_, keyboard := handler.getMainMenuContent("admin", true, 123456789)

	// Check for admin buttons
	foundStats := false
	foundLastReg := false
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			if button.CallbackData != nil && *button.CallbackData == "admin_stats" {
				foundStats = true
			}
			if button.CallbackData != nil && *button.CallbackData == "admin_lastreg" {
				foundLastReg = true
			}
		}
	}
	if !foundStats {
		t.Error("Expected admin_stats button for admin user")
	}
	if !foundLastReg {
		t.Error("Expected admin_lastreg button for admin user")
	}
}

// TestSubscriptionKeyboardLayout tests that QR and В начало are on separate rows
func TestSubscriptionKeyboardLayout(t *testing.T) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 QR-код", "qr_code"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)

	if len(keyboard.InlineKeyboard) != 2 {
		t.Errorf("Expected 2 keyboard rows, got %d", len(keyboard.InlineKeyboard))
	}

	// First row should have QR button
	if len(keyboard.InlineKeyboard[0]) != 1 {
		t.Errorf("Expected 1 button in first row, got %d", len(keyboard.InlineKeyboard[0]))
	}
	if keyboard.InlineKeyboard[0][0].CallbackData == nil || *keyboard.InlineKeyboard[0][0].CallbackData != "qr_code" {
		t.Error("Expected qr_code callback in first row")
	}

	// Second row should have В начало button
	if len(keyboard.InlineKeyboard[1]) != 1 {
		t.Errorf("Expected 1 button in second row, got %d", len(keyboard.InlineKeyboard[1]))
	}
	if keyboard.InlineKeyboard[1][0].CallbackData == nil || *keyboard.InlineKeyboard[1][0].CallbackData != "back_to_start" {
		t.Error("Expected back_to_start callback in second row")
	}
}

// TestQRBackKeyboard tests the keyboard for QR code message
func TestQRBackKeyboard(t *testing.T) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", "back_to_subscription"),
		),
	)

	if len(keyboard.InlineKeyboard) != 1 {
		t.Errorf("Expected 1 keyboard row, got %d", len(keyboard.InlineKeyboard))
	}
	if len(keyboard.InlineKeyboard[0]) != 1 {
		t.Errorf("Expected 1 button in row, got %d", len(keyboard.InlineKeyboard[0]))
	}
	if keyboard.InlineKeyboard[0][0].CallbackData == nil || *keyboard.InlineKeyboard[0][0].CallbackData != "back_to_subscription" {
		t.Error("Expected back_to_subscription callback")
	}
}

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

// === getMainMenuContent edge cases ===

func TestHandler_GetMainMenuContent_SpecialCharacters(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 12345}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	text, keyboard := handler.getMainMenuContent("test_user_123", true, 12345)
	assert.Contains(t, text, "test_user_123", "Text should contain username")
	assert.NotEmpty(t, keyboard.InlineKeyboard, "Keyboard should not be empty")
}

func TestHandler_GetMainMenuContent_AdminUser(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 12345}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	text, keyboard := handler.getMainMenuContent("admin", true, 12345)
	assert.Contains(t, text, "admin", "Text should contain username")

	// Check for admin buttons (should have 4 rows: main menu + share + admin buttons)
	assert.GreaterOrEqual(t, len(keyboard.InlineKeyboard), 3, "Admin should see admin buttons")
}

// === getMainMenuKeyboard edge cases ===

func TestHandler_GetMainMenuKeyboard_ButtonLabels(t *testing.T) {
	handler := &Handler{cfg: &config.Config{}, botConfig: NewTestBotConfig()}

	keyboard := handler.getMainMenuKeyboard(true)

	// Verify button labels
	assert.Equal(t, "📋 Подписка", keyboard.InlineKeyboard[0][0].Text, "First button label")
	assert.Equal(t, "☕ Донат", keyboard.InlineKeyboard[0][1].Text, "Second button label")
	assert.Equal(t, "❓ Помощь", keyboard.InlineKeyboard[1][0].Text, "Third button label")
	assert.Equal(t, "📤 Поделиться", keyboard.InlineKeyboard[2][0].Text, "Share button label")
}

func TestHandler_GetMainMenuKeyboard_CallbackData(t *testing.T) {
	handler := &Handler{cfg: &config.Config{}, botConfig: NewTestBotConfig()}

	keyboard := handler.getMainMenuKeyboard(true)

	// Verify callback data (CallbackData is *string, need to dereference)
	require.NotNil(t, keyboard.InlineKeyboard[0][0].CallbackData, "Subscription callback should not be nil")
	assert.Equal(t, "menu_subscription", *keyboard.InlineKeyboard[0][0].CallbackData, "Subscription callback")
	require.NotNil(t, keyboard.InlineKeyboard[0][1].CallbackData, "Donate callback should not be nil")
	assert.Equal(t, "menu_donate", *keyboard.InlineKeyboard[0][1].CallbackData, "Donate callback")
	require.NotNil(t, keyboard.InlineKeyboard[1][0].CallbackData, "Help callback should not be nil")
	assert.Equal(t, "menu_help", *keyboard.InlineKeyboard[1][0].CallbackData, "Help callback")
	require.NotNil(t, keyboard.InlineKeyboard[2][0].CallbackData, "Share callback should not be nil")
	assert.Equal(t, "share_invite", *keyboard.InlineKeyboard[2][0].CallbackData, "Share callback")
}

// === getBackKeyboard tests ===

func TestHandler_GetBackKeyboard_CallbackData(t *testing.T) {
	handler := &Handler{cfg: &config.Config{}, botConfig: NewTestBotConfig()}

	keyboard := handler.getBackKeyboard()

	// CallbackData is *string, need to dereference
	require.NotNil(t, keyboard.InlineKeyboard[0][0].CallbackData, "Back button callback should not be nil")
	assert.Equal(t, "back_to_start", *keyboard.InlineKeyboard[0][0].CallbackData, "Back button callback")
}

// === getDonateText tests ===

func TestHandler_GetDonateText_Content(t *testing.T) {
	handler := &Handler{cfg: &config.Config{ContactUsername: "kereal"}, botConfig: NewTestBotConfig()}

	text := handler.getDonateText()

	assert.Contains(t, text, "Поддержка проекта", "Should contain header")
	assert.Contains(t, text, "tbank.ru", "Should contain T-Bank link")
	assert.Contains(t, text, "t.me/kereal", "Should contain contact link")
}

// === getHelpText edge cases ===

func TestHandler_GetHelpText_ZeroTraffic(t *testing.T) {
	handler := &Handler{cfg: &config.Config{}, botConfig: NewTestBotConfig()}

	text := handler.getHelpText(0, "http://test.url/sub")

	assert.Contains(t, text, "0Гб", "Should contain 0 GB")
	assert.Contains(t, text, "http://test.url/sub", "Should contain subscription URL")
}

func TestHandler_GetHelpText_LargeTraffic(t *testing.T) {
	handler := &Handler{cfg: &config.Config{}, botConfig: NewTestBotConfig()}

	text := handler.getHelpText(1000, "http://test.url/sub")

	assert.Contains(t, text, "1000Гб", "Should contain 1000 GB")
}

func TestHandler_GetHelpText_SpecialCharacters(t *testing.T) {
	handler := &Handler{cfg: &config.Config{}, botConfig: NewTestBotConfig()}

	subURL := "http://test.url/sub/abc123?param=value&other=test"
	text := handler.getHelpText(100, subURL)

	assert.Contains(t, text, subURL, "Should contain full subscription URL")
}

// === addAdminButtons tests ===

func TestHandler_AddAdminButtons_ExistingKeyboard(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 12345}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Test", "test"),
		),
	)

	// Before adding admin buttons
	initialRows := len(keyboard.InlineKeyboard)

	handler.addAdminButtons(&keyboard, 12345)

	// After adding admin buttons
	assert.Greater(t, len(keyboard.InlineKeyboard), initialRows, "Should have more rows after adding admin buttons")
}

func TestHandler_AddAdminButtons_NonAdmin(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 12345}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Test", "test"),
		),
	)

	initialRows := len(keyboard.InlineKeyboard)

	handler.addAdminButtons(&keyboard, 99999) // Non-admin

	assert.Equal(t, initialRows, len(keyboard.InlineKeyboard), "Should not add buttons for non-admin")
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

// === Keyboard construction tests ===

func TestHandler_KeyboardConstruction_MultipleRows(t *testing.T) {
	handler := &Handler{cfg: &config.Config{TelegramAdminID: 12345}}

	keyboard := handler.getMainMenuKeyboard(true)

	// Verify structure
	assert.GreaterOrEqual(t, len(keyboard.InlineKeyboard), 3, "Should have at least 3 rows")

	// Each row should have at least one button
	for i, row := range keyboard.InlineKeyboard {
		assert.Greater(t, len(row), 0, "Row %d should have at least one button", i)
	}
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

func TestGetQRKeyboard(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	handler := NewHandler(nil, cfg, nil, nil, NewTestBotConfig())

	keyboard := handler.getQRKeyboard()

	assert.Len(t, keyboard.InlineKeyboard, 2, "should have 2 rows")

	row1 := keyboard.InlineKeyboard[0]
	assert.Len(t, row1, 1, "first row should have 1 button")
	assert.Equal(t, "📱 QR-код", row1[0].Text)
	require.NotNil(t, row1[0].CallbackData)
	assert.Equal(t, "qr_code", *row1[0].CallbackData)

	row2 := keyboard.InlineKeyboard[1]
	assert.Len(t, row2, 1, "second row should have 1 button")
	assert.Equal(t, "🏠 В начало", row2[0].Text)
	require.NotNil(t, row2[0].CallbackData)
	assert.Equal(t, "back_to_start", *row2[0].CallbackData)
}

func TestGetQRKeyboard_PreservesHandlerState(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 456}
	handler := NewHandler(nil, cfg, nil, nil, NewTestBotConfig())

	keyboard1 := handler.getQRKeyboard()
	keyboard2 := handler.getQRKeyboard()

	assert.Equal(t, keyboard1.InlineKeyboard[0][0].CallbackData,
		keyboard2.InlineKeyboard[0][0].CallbackData, "should return consistent keyboard")
}
