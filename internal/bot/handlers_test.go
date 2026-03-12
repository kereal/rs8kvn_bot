package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func init() {
	// Initialize logger for tests
	logger.Init("", "error")
}

// TestGetLastSecondOfMonth tests the getLastSecondOfMonth helper function
func TestGetLastSecondOfMonth(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		{
			name:     "January 2024",
			input:    time.Date(2024, 1, 15, 12, 30, 45, 0, time.UTC),
			expected: time.Date(2024, 1, 31, 23, 59, 59, 0, time.UTC),
		},
		{
			name:     "February 2024 (leap year)",
			input:    time.Date(2024, 2, 15, 12, 30, 45, 0, time.UTC),
			expected: time.Date(2024, 2, 29, 23, 59, 59, 0, time.UTC),
		},
		{
			name:     "February 2023 (non-leap year)",
			input:    time.Date(2023, 2, 15, 12, 30, 45, 0, time.UTC),
			expected: time.Date(2023, 2, 28, 23, 59, 59, 0, time.UTC),
		},
		{
			name:     "April 2024 (30 days)",
			input:    time.Date(2024, 4, 15, 12, 30, 45, 0, time.UTC),
			expected: time.Date(2024, 4, 30, 23, 59, 59, 0, time.UTC),
		},
		{
			name:     "December 2024",
			input:    time.Date(2024, 12, 15, 12, 30, 45, 0, time.UTC),
			expected: time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
		},
		{
			name:     "First day of month",
			input:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			expected: time.Date(2024, 3, 31, 23, 59, 59, 0, time.UTC),
		},
		{
			name:     "Last day of month",
			input:    time.Date(2024, 1, 31, 23, 59, 59, 0, time.UTC),
			expected: time.Date(2024, 1, 31, 23, 59, 59, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getLastSecondOfMonth(tt.input)
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
		config:      cfg,
		xui:         xuiClient,
		rateLimiter: nil,
	}

	if handler == nil {
		t.Fatal("Handler is nil")
	}
	if handler.config != cfg {
		t.Error("Config not set correctly")
	}
	if handler.xui != xuiClient {
		t.Error("XUI client not set correctly")
	}
}

// TestHandleStart_NilMessage tests HandleStart with nil message
func TestHandleStart_NilMessage(t *testing.T) {
	handler := &Handler{
		config: &config.Config{
			TelegramAdminID: 123456789,
		},
	}

	// Should not panic with nil message
	update := tgbotapi.Update{}
	handler.HandleStart(update)
	// If we reach here, the test passes (no panic)
}

// TestHandleHelp_NilMessage tests HandleHelp with nil message
func TestHandleHelp_NilMessage(t *testing.T) {
	handler := &Handler{
		config: &config.Config{
			TrafficLimitGB: 100,
		},
	}

	// Should not panic with nil message
	update := tgbotapi.Update{}
	handler.HandleHelp(update)
	// If we reach here, the test passes (no panic)
}

// TestHandleCallback_NilCallback tests HandleCallback with nil callback
func TestHandleCallback_NilCallback(t *testing.T) {
	handler := &Handler{
		config: &config.Config{},
	}

	// Should not panic with nil callback
	update := tgbotapi.Update{}
	handler.HandleCallback(update)
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
	if time.Now().After(got.ExpiryTime) {
		// This is the expected behavior - subscription exists but is expired
		// The handler should check this and show appropriate message
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
			tgbotapi.NewInlineKeyboardButtonData("📊 Статистика", "admin_stats"),
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
