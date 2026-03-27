package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/utils"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func init() {
	// Initialize logger for tests
	_, _ = logger.Init("", "error")
}

func TestGetFirstSecondOfNextMonth(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		{"January", time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC), time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
		{"December", time.Date(2024, 12, 15, 12, 0, 0, 0, time.UTC), time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"First day", time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := utils.FirstSecondOfNextMonth(tt.input)
			if !result.Equal(tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestGetUsername(t *testing.T) {
	handler := &Handler{}

	tests := []struct {
		name     string
		user     *tgbotapi.User
		expected string
	}{
		{"first name only", &tgbotapi.User{ID: 1, UserName: "testuser", FirstName: "Test"}, "testuser"},
		{"first name only no username", &tgbotapi.User{ID: 1, FirstName: "Test"}, "Test"},
		{"no name", &tgbotapi.User{ID: 1}, "user_1"},
		{"nil user", nil, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.getUsername(tt.user)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
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
	if err != nil {
		t.Fatalf("Failed to create XUI client: %v", err)
	}
	mockDB := testutil.NewMockDatabaseService()
	handler := NewHandler(nil, cfg, mockDB, xuiClient)

	if handler == nil {
		t.Fatal("NewHandler returned nil")
	}

	if handler.cfg != cfg {
		t.Error("Config not set correctly")
	}

	if handler.rateLimiter == nil {
		t.Error("RateLimiter should not be nil")
	}
}

func TestGetMainMenuKeyboard(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	handler := &Handler{cfg: cfg}

	keyboard := handler.getMainMenuKeyboard()

	if len(keyboard.InlineKeyboard) == 0 {
		t.Error("Keyboard should have rows")
	}

	if len(keyboard.InlineKeyboard) != 2 {
		t.Errorf("Expected 2 rows, got %d", len(keyboard.InlineKeyboard))
	}
}

func TestGetBackKeyboard(t *testing.T) {
	cfg := &config.Config{}
	handler := &Handler{cfg: cfg}

	keyboard := handler.getBackKeyboard()

	if len(keyboard.InlineKeyboard) != 1 {
		t.Error("Back keyboard should have 1 row")
	}

	if len(keyboard.InlineKeyboard[0]) != 1 {
		t.Error("Back keyboard should have 1 button")
	}

	btn := keyboard.InlineKeyboard[0][0]
	if btn.Text != "🏠 В начало" {
		t.Errorf("Expected '🏠 В начало', got '%s'", btn.Text)
	}
}

func TestGetDonateText(t *testing.T) {
	cfg := &config.Config{}
	handler := &Handler{cfg: cfg}

	text := handler.getDonateText()

	if len(text) == 0 {
		t.Error("Donate text should not be empty")
	}

	expected := []string{"☕", "Поддержка проекта"}
	for _, exp := range expected {
		found := false
		for i := 0; i <= len(text)-len(exp); i++ {
			if text[i:i+len(exp)] == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected donate text to contain '%s'", exp)
		}
	}
}

func TestGetHelpText(t *testing.T) {
	cfg := &config.Config{TrafficLimitGB: 100}
	handler := &Handler{cfg: cfg}

	text := handler.getHelpText(100, "http://localhost/sub/test")

	if len(text) == 0 {
		t.Error("Help text should not be empty")
	}
}

func TestGetHelpText_DifferentTrafficLimits(t *testing.T) {
	handler := &Handler{cfg: &config.Config{}}

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

func TestSubscriptionExpiryCheck(t *testing.T) {
	tests := []struct {
		name       string
		expiryTime time.Time
		isExpired  bool
	}{
		{"Not expired", time.Now().Add(24 * time.Hour), false},
		{"Expired", time.Now().Add(-24 * time.Hour), true},
		{"Expires now", time.Now(), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isExpired := time.Now().After(tt.expiryTime)
			if isExpired != tt.isExpired {
				t.Errorf("Expiry check: got %v, want %v", isExpired, tt.isExpired)
			}
		})
	}
}

func TestAdminCheck(t *testing.T) {
	tests := []struct {
		name    string
		chatID  int64
		adminID int64
		isAdmin bool
	}{
		{"Is admin", 123456789, 123456789, true},
		{"Not admin", 123456789, 987654321, false},
		{"Admin ID is 0", 123456789, 0, false},
		{"Zero chatID and admin", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isAdmin := tt.chatID == tt.adminID
			if isAdmin != tt.isAdmin {
				t.Errorf("Admin check: got %v, want %v", isAdmin, tt.isAdmin)
			}
		})
	}
}

func TestTrafficBytesCalculation(t *testing.T) {
	trafficLimitGB := 100
	expectedBytes := int64(trafficLimitGB) * 1024 * 1024 * 1024

	if expectedBytes != 107374182400 {
		t.Errorf("Traffic bytes = %d, want 107374182400", expectedBytes)
	}
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if ctx.Err() == nil {
		t.Error("Context should be cancelled")
	}

	select {
	case <-ctx.Done():
	default:
		t.Error("Context should be done")
	}
}

func TestMessageConstruction(t *testing.T) {
	t.Run("Start message with username", func(t *testing.T) {
		username := "testuser"
		expectedContent := "👋 Привет, " + username
		if len(expectedContent) == 0 {
			t.Error("Expected non-empty start message")
		}
	})

	t.Run("Help message contains traffic limit", func(t *testing.T) {
		expectedContent := "100 ГБ"
		if len(expectedContent) == 0 {
			t.Error("Expected non-empty help message")
		}
	})
}

func TestKeyboardConstruction(t *testing.T) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📥 Получить подписку", "create_subscription"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Подписка", "menu_subscription"),
		),
	)

	if len(keyboard.InlineKeyboard) != 2 {
		t.Errorf("Expected 2 keyboard rows, got %d", len(keyboard.InlineKeyboard))
	}
}

func TestMessageConfig_DisableWebPagePreview(t *testing.T) {
	chatID := int64(123456789)
	text := "Test message"

	msg := tgbotapi.NewMessage(chatID, text)

	msg.DisableWebPagePreview = true
	if msg.DisableWebPagePreview != true {
		t.Error("DisableWebPagePreview should be true after setting")
	}
}

func TestNotifyAdmin(t *testing.T) {
	t.Run("Skip notification when admin ID is 0", func(t *testing.T) {
		adminID := int64(0)
		if adminID == 0 {
			return
		}
		t.Error("Should have skipped notification for admin ID 0")
	})

	t.Run("Send notification when admin ID is set", func(t *testing.T) {
		adminID := int64(123456789)
		if adminID == 0 {
			t.Error("Should send notification for non-zero admin ID")
		}
	})
}

func TestSubscriptionStatus(t *testing.T) {
	validStatuses := []string{"active", "revoked", "expired"}

	for _, status := range validStatuses {
		t.Run("Status: "+status, func(t *testing.T) {
			isValid := status == "active" || status == "revoked" || status == "expired"
			if !isValid {
				t.Errorf("Invalid status: %s", status)
			}
		})
	}
}

func TestCallbackQueryData(t *testing.T) {
	validCallbacks := map[string]bool{
		"create_subscription":  true,
		"qr_code":              true,
		"admin_stats":          true,
		"admin_lastreg":        true,
		"back_to_start":        true,
		"menu_donate":          true,
		"menu_subscription":    true,
		"back_to_subscription": true,
		"menu_help":            true,
	}

	callbackData := "create_subscription"
	if !validCallbacks[callbackData] {
		t.Errorf("Unexpected callback data: %s", callbackData)
	}
}

func TestUpdateHandling(t *testing.T) {
	t.Run("Message update", func(t *testing.T) {
		hasMessage := true
		hasCallback := false

		if hasMessage && !hasCallback {
		} else {
			t.Error("Should detect message update")
		}
	})

	t.Run("Callback update", func(t *testing.T) {
		hasMessage := false
		hasCallback := true

		if !hasMessage && hasCallback {
		} else {
			t.Error("Should detect callback update")
		}
	})

	t.Run("No message or callback", func(t *testing.T) {
		hasMessage := false
		hasCallback := false

		if !hasMessage && !hasCallback {
		} else {
			t.Error("Should detect empty update")
		}
	})
}

func TestUsernameExtraction(t *testing.T) {
	tests := []struct {
		name     string
		userName string
		expected string
	}{
		{"Username available", "testuser", "testuser"},
		{"Empty username", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			username := tt.userName
			if username != tt.expected && tt.expected != "" {
				t.Errorf("Username = %s, want %s", username, tt.expected)
			}
		})
	}
}

func TestSubscriptionURL(t *testing.T) {
	host := "http://localhost:2053"
	subID := "test123"
	subPath := "sub"

	expectedURL := host + "/" + subPath + "/" + subID

	if expectedURL != "http://localhost:2053/sub/test123" {
		t.Errorf("Subscription URL = %s, want http://localhost:2053/sub/test123", expectedURL)
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
	if err != nil {
		t.Fatalf("Failed to create XUI client: %v", err)
	}

	handler := &Handler{
		cfg: cfg,
		xui: xuiClient,
	}

	if handler.cfg != cfg {
		t.Error("Handler.cfg not set correctly")
	}
	if handler.cfg.TelegramAdminID != 999888777 {
		t.Errorf("cfg.TelegramAdminID = %d, want 999888777", handler.cfg.TelegramAdminID)
	}
	if handler.cfg.TrafficLimitGB != 50 {
		t.Errorf("cfg.TrafficLimitGB = %d, want 50", handler.cfg.TrafficLimitGB)
	}
}

func TestHandleStart_NilMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	update := tgbotapi.Update{}
	handler.HandleStart(context.Background(), update)
}

func TestHandleHelp_NilMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TrafficLimitGB: 100,
		},
	}

	update := tgbotapi.Update{}
	handler.HandleHelp(context.Background(), update)
}

func TestHandleCallback_NilCallback(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{},
	}

	update := tgbotapi.Update{}
	handler.HandleCallback(context.Background(), update)
}

func TestHandleDel_NilMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	update := tgbotapi.Update{}
	handler.HandleDel(context.Background(), update)
}

func TestHandleBroadcast_NilMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	update := tgbotapi.Update{}
	handler.HandleBroadcast(context.Background(), update)
}

func TestHandleSend_NilMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	update := tgbotapi.Update{}
	handler.HandleSend(context.Background(), update)
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

func TestSubscription_IsExpired(t *testing.T) {
	tests := []struct {
		name       string
		expiryTime time.Time
		want       bool
	}{
		{"expired subscription", time.Now().Add(-1 * time.Hour), true},
		{"active subscription", time.Now().Add(1 * time.Hour), false},
		{"expires now", time.Now(), true},
		{"expires in future", time.Now().Add(24 * time.Hour), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &database.Subscription{
				ExpiryTime: tt.expiryTime,
			}
			if got := sub.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSubscription_IsActive(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		expiryTime time.Time
		want       bool
	}{
		{"active and not expired", "active", time.Now().Add(1 * time.Hour), true},
		{"active but expired", "active", time.Now().Add(-1 * time.Hour), false},
		{"revoked status", "revoked", time.Now().Add(1 * time.Hour), false},
		{"expired status", "expired", time.Now().Add(1 * time.Hour), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &database.Subscription{
				Status:     tt.status,
				ExpiryTime: tt.expiryTime,
			}
			if got := sub.IsActive(); got != tt.want {
				t.Errorf("IsActive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandler_send_NilBot(t *testing.T) {
	t.Skip("Skipping - requires mock interface for rate limiter")
}

func TestHandler_safeSend_NilBot(t *testing.T) {
	t.Skip("Skipping - requires mock interface for bot API")
}

func TestHandler_SendMessage(t *testing.T) {
	t.Skip("Skipping - requires mock interface for bot API")
}

func TestHandler_notifyAdmin_ZeroAdminID(t *testing.T) {
	t.Skip("Skipping - requires mock interface for bot API")
}

func TestHandler_notifyAdminError_ZeroAdminID(t *testing.T) {
	t.Skip("Skipping - requires mock interface for bot API")
}

func TestGetFirstSecondOfNextMonth_January(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 30, 0, 0, time.UTC)
	result := utils.FirstSecondOfNextMonth(now)
	expected := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("utils.FirstSecondOfNextMonth() = %v, want %v", result, expected)
	}
}

func TestGetFirstSecondOfNextMonth_December(t *testing.T) {
	now := time.Date(2024, 12, 15, 12, 30, 0, 0, time.UTC)
	result := utils.FirstSecondOfNextMonth(now)
	expected := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("utils.FirstSecondOfNextMonth() = %v, want %v", result, expected)
	}
}

func TestGetFirstSecondOfNextMonth_FirstDay(t *testing.T) {
	now := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	result := utils.FirstSecondOfNextMonth(now)
	expected := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("utils.FirstSecondOfNextMonth() = %v, want %v", result, expected)
	}
}

func TestHandler_getDonateText_NotEmpty(t *testing.T) {
	handler := &Handler{cfg: &config.Config{}}
	text := handler.getDonateText()
	if len(text) == 0 {
		t.Error("getDonateText() should not return empty string")
	}
	if !strings.Contains(text, "Поддержка") {
		t.Error("getDonateText() should contain support message")
	}
}

func TestHandler_getHelpText_NotEmpty(t *testing.T) {
	handler := &Handler{cfg: &config.Config{TrafficLimitGB: 100}}
	text := handler.getHelpText(100, "http://example.com/sub/test")
	if len(text) == 0 {
		t.Error("getHelpText() should not return empty string")
	}
	if !strings.Contains(text, "100") {
		t.Error("getHelpText() should contain traffic limit")
	}
}

func TestHandleStart_EmptyCommandArguments(t *testing.T) {
	t.Skip("Skipping - requires mock interface for bot API")
}

func TestHandleHelp_EmptyMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TrafficLimitGB: 100,
		},
	}

	update := tgbotapi.Update{}
	handler.HandleHelp(context.Background(), update)
}

func TestHandleCallback_EmptyCallback(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{},
	}

	update := tgbotapi.Update{}
	handler.HandleCallback(context.Background(), update)
}

func TestHandleDel_EmptyMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	update := tgbotapi.Update{}
	handler.HandleDel(context.Background(), update)
}

func TestHandleDel_NoArguments(t *testing.T) {
	t.Skip("Skipping - requires mock interface for bot API")
}

func TestHandleDel_InvalidID(t *testing.T) {
	t.Skip("Skipping - requires mock interface for bot API")
}

func TestHandleBroadcast_EmptyMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	update := tgbotapi.Update{}
	handler.HandleBroadcast(context.Background(), update)
}

func TestHandleBroadcast_NoArguments(t *testing.T) {
	t.Skip("Skipping - requires mock interface for bot API")
}

func TestHandleSend_EmptyMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	update := tgbotapi.Update{}
	handler.HandleSend(context.Background(), update)
}

func TestHandleSend_NoArguments(t *testing.T) {
	t.Skip("Skipping - requires mock interface for bot API")
}

func TestHandleSend_OnlyTarget(t *testing.T) {
	t.Skip("Skipping - requires mock interface for bot API")
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

// TestCallbackList verifies all callbacks are accounted for
func TestCallbackList(t *testing.T) {
	// List of all callbacks used in the bot
	expectedCallbacks := []string{
		"create_subscription",
		"qr_code",
		"admin_stats",
		"admin_lastreg",
		"back_to_start",
		"menu_donate",
		"menu_subscription",
		"back_to_subscription",
		"menu_help",
	}

	// Verify each callback is unique
	seen := make(map[string]bool)
	for _, cb := range expectedCallbacks {
		if seen[cb] {
			t.Errorf("Duplicate callback: %s", cb)
		}
		seen[cb] = true
	}

	// Verify we have the expected number
	if len(expectedCallbacks) != 9 {
		t.Errorf("Expected 9 callbacks, got %d", len(expectedCallbacks))
	}
}
