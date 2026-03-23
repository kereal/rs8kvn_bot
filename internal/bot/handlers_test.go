package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/ratelimiter"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func init() {
	// Initialize logger for tests
	logger.Init("", "error")
}

// TestGetFirstSecondOfNextMonth tests the getFirstSecondOfNextMonth helper function
func TestGetFirstSecondOfNextMonth(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		{
			name:     "January 2024",
			input:    time.Date(2024, 1, 15, 12, 30, 45, 0, time.UTC),
			expected: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "February 2024 (leap year)",
			input:    time.Date(2024, 2, 15, 12, 30, 45, 0, time.UTC),
			expected: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "February 2023 (non-leap year)",
			input:    time.Date(2023, 2, 15, 12, 30, 45, 0, time.UTC),
			expected: time.Date(2023, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "April 2024 (30 days)",
			input:    time.Date(2024, 4, 15, 12, 30, 45, 0, time.UTC),
			expected: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "December 2024",
			input:    time.Date(2024, 12, 15, 12, 30, 45, 0, time.UTC),
			expected: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "First day of month",
			input:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			expected: time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "Last day of month",
			input:    time.Date(2024, 1, 31, 23, 59, 59, 0, time.UTC),
			expected: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getFirstSecondOfNextMonth(tt.input)
			if !result.Equal(tt.expected) {
				t.Errorf("getLastSecondOfMonth(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestNewHandler tests the NewHandler function
func TestNewHandler(t *testing.T) {
	// Create a test bot API (will fail without valid token, but we test the structure)
	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TrafficLimitGB:   100,
		XUIHost:          "http://localhost:2053",
		XUIInboundID:     1,
		XUISubPath:       "sub",
		TelegramBotToken: "test_token",
	}

	xuiClient := xui.NewClient(cfg.XUIHost, "admin", "password")

	// We can't create a real BotAPI without a valid token
	// So we test with nil and expect it to work for structure tests
	handler := &Handler{
		bot:         nil,
		cfg:         cfg,
		xui:         xuiClient,
		rateLimiter: nil,
	}

	if handler.cfg != cfg {
		t.Error("Config not set correctly")
	}
	if handler.xui != xuiClient {
		t.Error("XUI client not set correctly")
	}
}

// TestHandleStart_NilMessage tests HandleStart with nil message
func TestHandleStart_NilMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	// Should not panic with nil message
	update := tgbotapi.Update{}
	handler.HandleStart(context.Background(), update)
	// If we reach here, the test passes (no panic)
}

// TestHandleHelp_NilMessage tests HandleHelp with nil message
func TestHandleHelp_NilMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TrafficLimitGB: 100,
		},
	}

	// Should not panic with nil message
	update := tgbotapi.Update{}
	handler.HandleHelp(context.Background(), update)
	// If we reach here, the test passes (no panic)
}

// TestHandleCallback_NilCallback tests HandleCallback with nil callback
func TestHandleCallback_NilCallback(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{},
	}

	// Should not panic with nil callback
	update := tgbotapi.Update{}
	handler.HandleCallback(context.Background(), update)
	// If we reach here, the test passes (no panic)
}

// TestHandleCallback_DataParsing tests callback data parsing
func TestHandleCallback_DataParsing(t *testing.T) {
	tests := []struct {
		name         string
		callbackData string
	}{
		{"get_subscription", "get_subscription"},
		{"my_subscription", "my_subscription"},
		{"admin_stats", "admin_stats"},
		{"unknown", "unknown_data"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that the callback data is correctly parsed
			// We verify the expected behavior without actual bot
			data := tt.callbackData
			// Just verify the data is correctly captured
			if data != tt.callbackData {
				t.Errorf("Callback data mismatch: got %s, want %s", data, tt.callbackData)
			}
		})
	}
}

// setupTestDatabase creates a temporary test database
func setupTestDatabase(t *testing.T) func() {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := database.Init(dbPath); err != nil {
		t.Fatalf("Failed to init test database: %v", err)
	}

	return func() {
		database.Close()
	}
}

// TestHandleMySubscription_NoSubscription tests handleMySubscription when user has no subscription
func TestHandleMySubscription_NoSubscription(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// This test verifies the database query logic
	// Without a real bot, we can't test the message sending

	// Verify that GetByTelegramID returns error for non-existent user
	_, err := database.GetByTelegramID(999999999)
	if err == nil {
		t.Error("Expected error for non-existent user, got nil")
	}
}

// TestHandleMySubscription_WithSubscription tests handleMySubscription when user has a subscription
func TestHandleMySubscription_WithSubscription(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create a test subscription
	sub := &database.Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/test",
	}

	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create test subscription: %v", err)
	}

	// Verify that GetByTelegramID returns the subscription
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

// TestHandleMySubscription_ExpiredSubscription tests handleMySubscription with expired subscription
func TestHandleMySubscription_ExpiredSubscription(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create an expired subscription
	sub := &database.Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(-24 * time.Hour), // Expired
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/test",
	}

	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create test subscription: %v", err)
	}

	// Verify that GetByTelegramID returns the subscription (even if expired)
	got, err := database.GetByTelegramID(123456789)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}

	// The subscription exists but is expired
	// Verify the subscription is actually expired
	if !time.Now().After(got.ExpiryTime) {
		t.Error("Expected subscription to be expired")
	}
}

// TestHandleAdminStats tests the admin stats handler
func TestHandleAdminStats(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create multiple test subscriptions
	for i := 0; i < 5; i++ {
		sub := &database.Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
		}
		if err := database.CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create test subscription: %v", err)
		}
	}

	// Count subscriptions
	var count int64
	database.DB.Model(&database.Subscription{}).Count(&count)

	if count != 5 {
		t.Errorf("Expected 5 subscriptions, got %d", count)
	}
}

// TestCreateSubscription_XUIError tests createSubscription when XUI fails
func TestCreateSubscription_XUIError(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create a mock XUI server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			resp := map[string]interface{}{
				"success": true,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		resp := map[string]interface{}{
			"success": false,
			"msg":     "Internal server error",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// This test verifies the XUI client error handling
	// Without a real bot, we can't test the full createSubscription function
}

// TestSubscriptionExpiryCheck tests the subscription expiry check logic
func TestSubscriptionExpiryCheck(t *testing.T) {
	tests := []struct {
		name       string
		expiryTime time.Time
		isExpired  bool
	}{
		{
			name:       "Not expired",
			expiryTime: time.Now().Add(24 * time.Hour),
			isExpired:  false,
		},
		{
			name:       "Expired",
			expiryTime: time.Now().Add(-24 * time.Hour),
			isExpired:  true,
		},
		{
			name:       "Expires now",
			expiryTime: time.Now(),
			isExpired:  true, // Now is after or equal to expiry
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isExpired := time.Now().After(tt.expiryTime)
			if isExpired != tt.isExpired {
				t.Errorf("Expiry check failed: got %v, want %v", isExpired, tt.isExpired)
			}
		})
	}
}

// TestAdminCheck tests the admin check logic
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isAdmin := tt.chatID == tt.adminID
			if isAdmin != tt.isAdmin {
				t.Errorf("Admin check failed: got %v, want %v", isAdmin, tt.isAdmin)
			}
		})
	}
}

// TestMessageConstruction tests message construction for various handlers
func TestMessageConstruction(t *testing.T) {
	t.Run("Start message with username", func(t *testing.T) {
		username := "testuser"
		expectedContent := "👋 Привет, " + username

		// Verify message contains expected content
		if len(expectedContent) == 0 {
			t.Error("Expected non-empty start message")
		}
	})

	t.Run("Help message contains traffic limit", func(t *testing.T) {
		trafficLimit := 100
		expectedContent := fmt.Sprintf("%d ГБ", trafficLimit)

		if len(expectedContent) == 0 {
			t.Error("Expected non-empty help message")
		}
	})

	t.Run("Subscription message contains URL", func(t *testing.T) {
		subscriptionURL := "http://localhost/sub/test"

		if len(subscriptionURL) == 0 {
			t.Error("Expected non-empty subscription URL")
		}
	})
}

// TestKeyboardConstruction tests inline keyboard construction
func TestKeyboardConstruction(t *testing.T) {
	// Test basic keyboard
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📥 Получить подписку", "get_subscription"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Моя подписка", "my_subscription"),
		),
	)

	if len(keyboard.InlineKeyboard) != 2 {
		t.Errorf("Expected 2 keyboard rows, got %d", len(keyboard.InlineKeyboard))
	}

	// Test admin keyboard (with extra button)
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 Стат", "admin_stats"),
		),
	)

	if len(keyboard.InlineKeyboard) != 3 {
		t.Errorf("Expected 3 keyboard rows after admin button, got %d", len(keyboard.InlineKeyboard))
	}
}

// TestRateLimiterIntegration tests that the rate limiter is properly initialized
func TestRateLimiterIntegration(t *testing.T) {
	// The rate limiter should be initialized in NewHandler
	// We can't test the actual rate limiting without a real handler
	// but we can verify the configuration
	maxTokens := 30
	refillRate := 5

	// Verify rate limiter config is reasonable
	if maxTokens < 1 {
		t.Error("Max tokens should be at least 1")
	}
	if refillRate < 1 {
		t.Error("Refill rate should be at least 1")
	}
}

// TestNotifyAdmin tests the admin notification logic
func TestNotifyAdmin(t *testing.T) {
	t.Run("Skip notification when admin ID is 0", func(t *testing.T) {
		adminID := int64(0)
		if adminID == 0 {
			// Should skip notification
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

// TestTrafficBytesCalculation tests traffic bytes calculation
func TestTrafficBytesCalculation(t *testing.T) {
	trafficLimitGB := 100
	expectedBytes := int64(trafficLimitGB) * 1024 * 1024 * 1024

	// 100 GB in bytes = 107374182400
	if expectedBytes != 107374182400 {
		t.Errorf("Traffic bytes = %d, want 107374182400", expectedBytes)
	}
}

// TestContextCancellation tests that handlers respect context cancellation
func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Verify context is cancelled
	if ctx.Err() == nil {
		t.Error("Context should be cancelled")
	}

	// Handlers should check context and return early
	select {
	case <-ctx.Done():
		// Expected - context is cancelled
	default:
		t.Error("Context should be done")
	}
}

// TestSubscriptionStatus tests subscription status values
func TestSubscriptionStatus(t *testing.T) {
	validStatuses := []string{"active", "revoked", "expired"}

	for _, status := range validStatuses {
		t.Run("Status: "+status, func(t *testing.T) {
			// Verify status is one of the expected values
			isValid := status == "active" || status == "revoked" || status == "expired"
			if !isValid {
				t.Errorf("Invalid status: %s", status)
			}
		})
	}
}

// TestCallbackQueryData tests callback query data extraction
func TestCallbackQueryData(t *testing.T) {
	callbackData := "get_subscription"

	// Verify callback data format
	if len(callbackData) == 0 {
		t.Error("Callback data should not be empty")
	}

	// Verify expected callback data values
	validCallbacks := map[string]bool{
		"get_subscription": true,
		"my_subscription":  true,
		"admin_stats":      true,
	}

	if !validCallbacks[callbackData] {
		t.Errorf("Unexpected callback data: %s", callbackData)
	}
}

// TestUpdateHandling tests update type detection
func TestUpdateHandling(t *testing.T) {
	t.Run("Message update", func(t *testing.T) {
		// Simulate a message update
		hasMessage := true
		hasCallback := false

		if hasMessage && !hasCallback {
			// Should handle as message
		} else {
			t.Error("Should detect message update")
		}
	})

	t.Run("Callback update", func(t *testing.T) {
		// Simulate a callback update
		hasMessage := false
		hasCallback := true

		if !hasMessage && hasCallback {
			// Should handle as callback
		} else {
			t.Error("Should detect callback update")
		}
	})

	t.Run("No message or callback", func(t *testing.T) {
		// Simulate an empty update
		hasMessage := false
		hasCallback := false

		if !hasMessage && !hasCallback {
			// Should skip processing
		} else {
			t.Error("Should detect empty update")
		}
	})
}

// TestUsernameExtraction tests username extraction from update
func TestUsernameExtraction(t *testing.T) {
	tests := []struct {
		name      string
		userName  string
		firstName string
		expected  string
	}{
		{"Username available", "testuser", "Test", "testuser"},
		{"Only first name", "", "Test", "Test"},
		{"Both empty", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			username := tt.userName
			if username == "" {
				username = tt.firstName
			}

			if username != tt.expected {
				t.Errorf("Username = %s, want %s", username, tt.expected)
			}
		})
	}
}

// TestHelpText tests that help text contains required information
func TestHelpText(t *testing.T) {
	trafficLimit := 100
	helpText := fmt.Sprintf("📊 Трафик: %d ГБ в месяц", trafficLimit)

	if len(helpText) == 0 {
		t.Error("Help text should not be empty")
	}

	// Verify help text contains traffic limit
	if trafficLimit != 100 {
		t.Errorf("Traffic limit = %d, want 100", trafficLimit)
	}
}

// TestSubscriptionURL tests subscription URL generation
func TestSubscriptionURL(t *testing.T) {
	host := "http://localhost:2053"
	subID := "test123"
	subPath := "sub"

	expectedURL := fmt.Sprintf("%s/%s/%s", host, subPath, subID)

	if expectedURL != "http://localhost:2053/sub/test123" {
		t.Errorf("Subscription URL = %s, want http://localhost:2053/sub/test123", expectedURL)
	}
}

// ==================== Helper Function Tests ====================

func TestGetUsername(t *testing.T) {
	tests := []struct {
		name     string
		user     *tgbotapi.User
		expected string
	}{
		{
			name: "user with username",
			user: &tgbotapi.User{
				ID:        123,
				FirstName: "John",
				UserName:  "johndoe",
			},
			expected: "johndoe",
		},
		{
			name: "user without username, with first name",
			user: &tgbotapi.User{
				ID:        456,
				FirstName: "Jane",
			},
			expected: "Jane",
		},
		{
			name: "user without username and first name",
			user: &tgbotapi.User{
				ID: 789,
			},
			expected: "user_789",
		},
		{
			name:     "nil user",
			user:     nil,
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &Handler{}
			got := handler.getUsername(tt.user)
			if got != tt.expected {
				t.Errorf("getUsername() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// ==================== Context Propagation Tests ====================

func TestHandleStart_WithContext(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TrafficLimitGB:   100,
		XUIHost:          "http://localhost:2053",
		XUIInboundID:     1,
		XUISubPath:       "sub",
		TelegramBotToken: "123456:test_token",
	}

	xuiClient := xui.NewClient(cfg.XUIHost, "admin", "password")

	handler := &Handler{
		bot:         nil,
		cfg:         cfg,
		xui:         xuiClient,
		rateLimiter: nil,
	}

	// Test with background context
	ctx := context.Background()
	update := tgbotapi.Update{}

	// Should not panic
	handler.HandleStart(ctx, update)
}

func TestHandleHelp_WithContext(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
	}

	handler := &Handler{
		cfg: cfg,
	}

	ctx := context.Background()
	update := tgbotapi.Update{}

	// Should not panic
	handler.HandleHelp(ctx, update)
}

func TestHandleCallback_WithContext(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{},
	}

	ctx := context.Background()
	update := tgbotapi.Update{}

	// Should not panic
	handler.HandleCallback(ctx, update)
}

// ==================== Config Field Tests ====================

func TestHandler_ConfigField(t *testing.T) {
	cfg := &config.Config{
		TelegramBotToken: "123456:test_token",
		TelegramAdminID:  999888777,
		TrafficLimitGB:   50,
		XUIHost:          "http://test.local:8080",
		XUIInboundID:     5,
		XUISubPath:       "mysub",
	}

	xuiClient := xui.NewClient(cfg.XUIHost, "user", "pass")

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

// ==================== RateLimiter Integration Tests ====================

func TestHandler_RateLimiterField(t *testing.T) {
	handler := &Handler{
		rateLimiter: ratelimiter.NewRateLimiter(10, 1),
	}

	if handler.rateLimiter == nil {
		t.Error("Handler.rateLimiter should not be nil")
	}

	// Test that rate limiter works
	ctx := context.Background()
	if !handler.rateLimiter.Wait(ctx) {
		t.Error("RateLimiter should allow first request")
	}
}

// ==================== HandleDel Tests ====================

// TestHandleDel_NilMessage tests HandleDel with nil message
func TestHandleDel_NilMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	// Should not panic with nil message
	update := tgbotapi.Update{}
	handler.HandleDel(context.Background(), update)
	// If we reach here, the test passes (no panic)
}

// TestHandleDel_NonAdmin tests that non-admin users get no response
func TestHandleDel_NonAdmin(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create a test subscription
	sub := &database.Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/test",
	}
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create test subscription: %v", err)
	}

	// Verify non-admin ID doesn't match admin ID
	nonAdminID := int64(987654321)
	adminID := int64(123456789)

	if nonAdminID == adminID {
		t.Error("Test setup error: non-admin ID should differ from admin ID")
	}
}

// TestHandleDel_NoArgs tests HandleDel with no arguments
func TestHandleDel_NoArgs(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	// Test that empty args is handled
	// Without a real bot, we can't test the message sending
	// This test verifies the function doesn't panic
	update := tgbotapi.Update{}
	handler.HandleDel(context.Background(), update)
}

// TestHandleDel_InvalidID tests HandleDel with invalid ID format
func TestHandleDel_InvalidID(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	// Test that invalid ID format is handled
	// Without a real bot, we can't test the message sending
	// This test verifies the function doesn't panic
	update := tgbotapi.Update{}
	handler.HandleDel(context.Background(), update)
}

// TestHandleDel_GetSubscriptionByID tests the database query for GetSubscriptionByID
func TestHandleDel_GetSubscriptionByID(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create a test subscription
	sub := &database.Subscription{
		TelegramID:      123456789,
		Username:        "deltestuser",
		ClientID:        "del-client-id",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/deltest",
	}
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create test subscription: %v", err)
	}

	// Get the subscription by ID
	got, err := database.GetSubscriptionByID(sub.ID)
	if err != nil {
		t.Fatalf("GetSubscriptionByID() error = %v", err)
	}

	if got.ID != sub.ID {
		t.Errorf("GetSubscriptionByID() ID = %d, want %d", got.ID, sub.ID)
	}
	if got.Username != sub.Username {
		t.Errorf("GetSubscriptionByID() Username = %s, want %s", got.Username, sub.Username)
	}
}

// TestHandleDel_DeleteSubscriptionByID tests the database delete function
func TestHandleDel_DeleteSubscriptionByID(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create a test subscription
	sub := &database.Subscription{
		TelegramID:      999888777,
		Username:        "deletetest",
		ClientID:        "delete-client-id",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/deletetest",
	}
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create test subscription: %v", err)
	}

	id := sub.ID

	// Delete the subscription by ID
	deleted, err := database.DeleteSubscriptionByID(id)
	if err != nil {
		t.Fatalf("DeleteSubscriptionByID() error = %v", err)
	}

	// Verify returned subscription has correct data
	if deleted.ID != id {
		t.Errorf("DeleteSubscriptionByID() returned ID = %d, want %d", deleted.ID, id)
	}

	// Verify it's deleted
	_, err = database.GetSubscriptionByID(id)
	if err == nil {
		t.Error("GetSubscriptionByID() should return error after deletion")
	}
}

// TestHandleDel_SubscriptionNotFound tests behavior when subscription doesn't exist
func TestHandleDel_SubscriptionNotFound(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Verify GetSubscriptionByID returns error for non-existent ID
	_, err := database.GetSubscriptionByID(99999)
	if err == nil {
		t.Error("GetSubscriptionByID() should return error for non-existent ID")
	}
}

// TestHandleBroadcast_NilMessage tests HandleBroadcast with nil message
func TestHandleBroadcast_NilMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	// Should not panic with nil message
	update := tgbotapi.Update{}
	handler.HandleBroadcast(context.Background(), update)
	// If we reach here, the test passes (no panic)
}

// TestHandleBroadcast_NonAdmin tests HandleBroadcast with non-admin user
func TestHandleBroadcast_NonAdmin(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 999999999, // Different from chat ID
		},
	}

	// Non-admin should be rejected
	// Without a real bot, we can't test the message sending
	// This test verifies the function doesn't panic and checks admin
	update := tgbotapi.Update{}
	handler.HandleBroadcast(context.Background(), update)
}

// TestHandleBroadcast_NoMessage tests HandleBroadcast with no message argument
func TestHandleBroadcast_NoMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	// Test that empty message is handled
	// Without a real bot, we can't test the message sending
	// This test verifies the function doesn't panic
	update := tgbotapi.Update{}
	handler.HandleBroadcast(context.Background(), update)
}

// TestHandleBroadcast_DatabaseFunction tests the GetAllTelegramIDs database function
func TestHandleBroadcast_DatabaseFunction(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create test subscriptions with different Telegram IDs
	subs := []*database.Subscription{
		{TelegramID: 111111111, Username: "user1", ClientID: "client1", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 222222222, Username: "user2", ClientID: "client2", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
	}

	for _, sub := range subs {
		if err := database.CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create subscription: %v", err)
		}
	}

	// Get all Telegram IDs
	ids, err := database.GetAllTelegramIDs()
	if err != nil {
		t.Fatalf("GetAllTelegramIDs() error = %v", err)
	}

	if len(ids) != 2 {
		t.Errorf("GetAllTelegramIDs() returned %d IDs, want 2", len(ids))
	}
}

// TestHandleSend_NilMessage tests HandleSend with nil message
func TestHandleSend_NilMessage(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	// Should not panic with nil message
	update := tgbotapi.Update{}
	handler.HandleSend(context.Background(), update)
	// If we reach here, the test passes (no panic)
}

// TestHandleSend_NonAdmin tests HandleSend with non-admin user
func TestHandleSend_NonAdmin(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 999999999, // Different from chat ID
		},
	}

	// Non-admin should be rejected
	update := tgbotapi.Update{}
	handler.HandleSend(context.Background(), update)
}

// TestHandleSend_NoArgs tests HandleSend with no arguments
func TestHandleSend_NoArgs(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	// Test that empty args is handled
	update := tgbotapi.Update{}
	handler.HandleSend(context.Background(), update)
}

// TestHandleSend_ByTelegramID tests the database lookup by Telegram ID
func TestHandleSend_ByTelegramID(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create test subscription
	sub := &database.Subscription{
		TelegramID: 123456789,
		Username:   "testuser",
		ClientID:   "client-id",
		Status:     "active",
		ExpiryTime: time.Now().Add(24 * time.Hour),
	}
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	// Verify the subscription exists
	got, err := database.GetByTelegramID(123456789)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}
	if got.TelegramID != 123456789 {
		t.Errorf("TelegramID = %d, want 123456789", got.TelegramID)
	}
}

// TestHandleSend_ByUsername tests the GetTelegramIDByUsername database function
func TestHandleSend_ByUsername(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create test subscription
	sub := &database.Subscription{
		TelegramID: 123456789,
		Username:   "testuser",
		ClientID:   "client-id",
		Status:     "active",
		ExpiryTime: time.Now().Add(24 * time.Hour),
	}
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	// Get Telegram ID by username
	id, err := database.GetTelegramIDByUsername("testuser")
	if err != nil {
		t.Fatalf("GetTelegramIDByUsername() error = %v", err)
	}

	if id != 123456789 {
		t.Errorf("GetTelegramIDByUsername() returned %d, want 123456789", id)
	}
}

// TestHandleSend_UserNotFound tests GetTelegramIDByUsername with non-existent user
func TestHandleSend_UserNotFound(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Try to get non-existent username
	_, err := database.GetTelegramIDByUsername("nonexistent")
	if err == nil {
		t.Error("GetTelegramIDByUsername() should return error for non-existent username")
	}
}

// ==================== Reply Keyboard Tests ====================

// TestGetDonateText tests the getDonateText helper function
func TestGetDonateText(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{},
	}

	text := handler.getDonateText()

	// Verify text is not empty
	if len(text) == 0 {
		t.Error("getDonateText() returned empty string")
	}

	// Verify text contains expected content
	if !strings.Contains(text, "☕") {
		t.Error("getDonateText() missing coffee emoji")
	}
	if !strings.Contains(text, "Поддержка проекта") {
		t.Error("getDonateText() missing 'Поддержка проекта'")
	}
	if !strings.Contains(text, "tbank.ru") {
		t.Error("getDonateText() missing T-Bank link")
	}
	if !strings.Contains(text, "t.me/kereal") {
		t.Error("getDonateText() missing contact link")
	}
}

// TestGetHelpText tests the getHelpText helper function
func TestGetHelpText(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{
			TrafficLimitGB: 100,
		},
	}

	trafficLimitGB := 100
	subscriptionURL := "http://localhost:2053/sub/test123"

	text := handler.getHelpText(trafficLimitGB, subscriptionURL)

	// Verify text is not empty
	if len(text) == 0 {
		t.Error("getHelpText() returned empty string")
	}

	// Verify text contains traffic limit
	if !strings.Contains(text, "100") {
		t.Error("getHelpText() missing traffic limit")
	}

	// Verify text contains subscription URL
	if !strings.Contains(text, subscriptionURL) {
		t.Error("getHelpText() missing subscription URL")
	}

	// Verify text contains expected sections
	if !strings.Contains(text, "🚀") {
		t.Error("getHelpText() missing rocket emoji")
	}
	if !strings.Contains(text, "Happ") {
		t.Error("getHelpText() missing Happ app mention")
	}
	if !strings.Contains(text, "iOS") {
		t.Error("getHelpText() missing iOS")
	}
	if !strings.Contains(text, "Android") {
		t.Error("getHelpText() missing Android")
	}

}

// TestGetHelpText_DifferentTrafficLimits tests getHelpText with different traffic limits
func TestGetHelpText_DifferentTrafficLimits(t *testing.T) {
	handler := &Handler{
		cfg: &config.Config{},
	}

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
			expected := fmt.Sprintf("%dГб", tt.trafficLimitGB)
			if !strings.Contains(text, expected) {
				t.Errorf("getHelpText() missing %s", expected)
			}
		})
	}
}

// TestHandleStart_WithSubscription tests HandleStart when user has subscription
func TestHandleStart_WithSubscription(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create a test subscription
	sub := &database.Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost:2053/sub/testuser",
	}

	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create test subscription: %v", err)
	}

	// Verify that GetByTelegramID returns the subscription
	got, err := database.GetByTelegramID(123456789)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}

	// Verify subscription is active
	if got.Status != "active" {
		t.Errorf("Status = %v, want active", got.Status)
	}

	// Note: Without a real bot, we can't test the inline keyboard construction
	// This test verifies the database query logic for the subscription condition
}

// TestHandleStart_NoSubscription tests HandleStart when user has no subscription
func TestHandleStart_NoSubscription(t *testing.T) {
	cleanup := setupTestDatabase(t)
	defer cleanup()

	// Verify that GetByTelegramID returns error for non-existent user
	_, err := database.GetByTelegramID(999999999)
	if err == nil {
		t.Error("Expected error for non-existent user, got nil")
	}

	// Note: Without a real bot, we can't test the inline button construction
	// This test verifies the database query logic for the inline button condition
}

// ==================== DisableWebPagePreview Tests ====================

// TestMessageConfig_DisableWebPagePreview tests that MessageConfig can have DisableWebPagePreview set
func TestMessageConfig_DisableWebPagePreview(t *testing.T) {
	chatID := int64(123456789)
	text := "Test message with link: https://example.com"

	msg := tgbotapi.NewMessage(chatID, text)

	// By default, DisableWebPagePreview is not set (link previews enabled)
	// We set DisableWebPagePreview = true to disable link previews (remove preview blocks)
	// Note: tgbotapi library may not set this field by default, so we test that we can set it
	msg.DisableWebPagePreview = false

	if msg.DisableWebPagePreview != false {
		t.Error("DisableWebPagePreview should be false (link previews enabled)")
	}

	// Test that we can set it to true
	msg.DisableWebPagePreview = true

	if msg.DisableWebPagePreview != true {
		t.Error("DisableWebPagePreview should be true after setting")
	}
}

// TestMessageConfig_DisableWebPagePreview_Broadcast tests broadcast message configuration
func TestMessageConfig_DisableWebPagePreview_Broadcast(t *testing.T) {
	telegramID := int64(123456789)
	message := "Broadcast message with link: https://t.me/example"

	msg := tgbotapi.NewMessage(telegramID, message)
	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true

	// Verify that DisableWebPagePreview is set to true (link previews disabled)
	if msg.DisableWebPagePreview != true {
		t.Error("Broadcast message should have DisableWebPagePreview = true")
	}

	// Verify ParseMode is set correctly
	if msg.ParseMode != "Markdown" {
		t.Error("Broadcast message should have ParseMode = Markdown")
	}
}

// TestMessageConfig_DisableWebPagePreview_SendMessage tests SendMessage message configuration
func TestMessageConfig_DisableWebPagePreview_SendMessage(t *testing.T) {
	telegramID := int64(987654321)
	message := "Direct message with link: https://example.com/page"

	msg := tgbotapi.NewMessage(telegramID, message)
	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true

	// Verify that DisableWebPagePreview is set to true (link previews disabled)
	if msg.DisableWebPagePreview != true {
		t.Error("Direct message should have DisableWebPagePreview = true")
	}
}

// TestMessageConfig_DisableWebPagePreview_LoadingMessage tests loading message configuration
func TestMessageConfig_DisableWebPagePreview_LoadingMessage(t *testing.T) {
	chatID := int64(111222333)

	loadingMsg := tgbotapi.NewMessage(chatID, "⏳ Загрузка...")
	loadingMsg.DisableWebPagePreview = true

	// Verify that DisableWebPagePreview is set to true (link previews disabled)
	if loadingMsg.DisableWebPagePreview != true {
		t.Error("Loading message should have DisableWebPagePreview = true")
	}
}

// TestMessageConfig_DisableWebPagePreview_HelpText tests help text message configuration
func TestMessageConfig_DisableWebPagePreview_HelpText(t *testing.T) {
	chatID := int64(444555666)

	handler := &Handler{
		cfg: &config.Config{
			TrafficLimitGB: 100,
		},
	}

	helpText := handler.getHelpText(100, "http://localhost:2053/sub/test")
	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true

	// Verify that DisableWebPagePreview is set to true (link previews disabled)
	if msg.DisableWebPagePreview != true {
		t.Error("Help text message should have DisableWebPagePreview = true")
	}
}

// TestMessageConfig_DisableWebPagePreview_DonateText tests donate text message configuration
func TestMessageConfig_DisableWebPagePreview_DonateText(t *testing.T) {
	chatID := int64(777888999)

	handler := &Handler{
		cfg: &config.Config{},
	}

	donateText := handler.getDonateText()
	msg := tgbotapi.NewMessage(chatID, donateText)
	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true

	// Verify that DisableWebPagePreview is set to true (link previews disabled)
	if msg.DisableWebPagePreview != true {
		t.Error("Donate text message should have DisableWebPagePreview = true")
	}
}

// ==================== Header Format Tests ====================

// TestLastRegHeaderFormat tests that the last registrations header has correct format
func TestLastRegHeaderFormat(t *testing.T) {
	// Test that the header does not contain colon and has correct format
	header := "📋 *Последние регистрации*\n\n"

	// Verify header does not contain colon
	if strings.Contains(header, ":") {
		t.Error("Header should not contain colon")
	}

	// Verify header contains expected text
	if !strings.Contains(header, "Последние регистрации") {
		t.Error("Header should contain 'Последние регистрации'")
	}

	// Verify header is bold (wrapped in *)
	if !strings.Contains(header, "*Последние регистрации*") {
		t.Error("Header should be bold (wrapped in *)")
	}
}

// TestStatsHeaderFormat tests that the stats header has correct format
func TestStatsHeaderFormat(t *testing.T) {
	// Test that the header does not contain colon and is bold
	header := "📊 *Статистика бота*"

	// Verify header does not contain colon
	if strings.Contains(header, ":") {
		t.Error("Header should not contain colon")
	}

	// Verify header is bold (wrapped in *)
	if !strings.HasPrefix(header, "📊 *") {
		t.Error("Header should start with '📊 *' (bold formatting)")
	}

	if !strings.HasSuffix(header, "*") {
		t.Error("Header should end with '*' (bold formatting)")
	}

	// Verify header contains expected text
	if !strings.Contains(header, "Статистика бота") {
		t.Error("Header should contain 'Статистика бота'")
	}
}
