package bot

import (
	"strings"
	"testing"

	"rs8kvn_bot/internal/config"

	"github.com/stretchr/testify/assert"
)

// === getMainMenuContent tests ===

func TestGetMainMenuContent(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 12345,
	}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)}

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

func TestGetMainMenuContent_WithSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 0,
	}
	handler := &Handler{
		cfg: cfg,
	}

	text, keyboard := handler.getMainMenuContent("testuser", true, 123456789)

	assert.NotEmpty(t, text, "Expected non-empty text for user with subscription")
	assert.Contains(t, text, "testuser", "Expected text to contain username")
	assert.GreaterOrEqual(t, len(keyboard.InlineKeyboard), 2, "Expected at least 2 keyboard rows")
}

func TestGetMainMenuContent_WithoutSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 0,
	}
	handler := &Handler{
		cfg: cfg,
	}

	text, keyboard := handler.getMainMenuContent("testuser", false, 123456789)

	assert.NotEmpty(t, text, "Expected non-empty text for user without subscription")
	assert.Contains(t, text, "testuser", "Expected text to contain username")
	assert.GreaterOrEqual(t, len(keyboard.InlineKeyboard), 1, "Expected at least 1 keyboard row")

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
	assert.True(t, found, "Expected create_subscription callback in keyboard for user without subscription")
}

func TestGetMainMenuContent_AdminButtons(t *testing.T) {
	t.Parallel()

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
	assert.True(t, foundStats, "Expected admin_stats button for admin user")
	assert.True(t, foundLastReg, "Expected admin_lastreg button for admin user")
}

func TestHandler_GetMainMenuContent_SpecialCharacters(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 12345}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)}

	text, keyboard := handler.getMainMenuContent("test_user_123", true, 12345)
	assert.Contains(t, text, "test_user_123", "Text should contain username")
	assert.NotEmpty(t, keyboard.InlineKeyboard, "Keyboard should not be empty")
}

func TestHandler_GetMainMenuContent_AdminUser(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 12345}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)}

	text, keyboard := handler.getMainMenuContent("admin", true, 12345)
	assert.Contains(t, text, "admin", "Text should contain username")
	assert.GreaterOrEqual(t, len(keyboard.InlineKeyboard), 3, "Admin should see admin buttons")
}

// === getDonateText tests ===

func TestGetDonateText(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)}

	text := handler.getDonateText()
	assert.NotEmpty(t, text, "Donate text should not be empty")

	expected := []string{"☕", "Поддержка проекта"}
	for _, exp := range expected {
		assert.True(t, strings.Contains(text, exp), "Expected donate text to contain '%s'", exp)
	}
}

func TestHandler_GetDonateText_Content(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{ContactUsername: "kereal", DonateCardNumber: "2200110022334455", DonateURL: "https://www.tbank.ru/collection/abc"}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)}

	text := handler.getDonateText()

	assert.Contains(t, text, "Поддержка проекта", "Should contain header")
	assert.Contains(t, text, "2200110022334455", "Should contain card number from config")
	assert.Contains(t, text, "https://www.tbank.ru/collection/abc", "Should contain donate URL from config")
	assert.Contains(t, text, "t.me/kereal", "Should contain contact link")
}

func TestHandler_GetDonateText_EmptyConfig(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)}

	text := handler.getDonateText()
	assert.Contains(t, text, "Поддержка проекта", "Should contain header even with empty config")
	t.Logf("Donate text with empty config: %q", text)
}

// === getHelpText tests ===

func TestGetHelpText(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TrafficLimitGB: 100}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)}

	text := handler.getHelpText(100, "http://localhost/sub/test")
	assert.NotEmpty(t, text, "Help text should not be empty")
}

func TestGetHelpText_DifferentTrafficLimits(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)}

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
			assert.NotEmpty(t, text, "text should not be empty")
		})
	}
}

func TestHandler_GetHelpText_ZeroTraffic(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)}

	text := handler.getHelpText(0, "http://test.url/sub")

	assert.Contains(t, text, "0Гб", "Should contain 0 GB")
	assert.Contains(t, text, "http://test.url/sub", "Should contain subscription URL")
}

func TestHandler_GetHelpText_LargeTraffic(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)}

	text := handler.getHelpText(1000, "http://test.url/sub")

	assert.Contains(t, text, "1000Гб", "Should contain 1000 GB")
}

func TestHandler_GetHelpText_SpecialCharacters(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL)}

	subURL := "http://test.url/sub/abc123?param=value&other=test"
	text := handler.getHelpText(100, subURL)

	assert.Contains(t, text, subURL, "Should contain full subscription URL")
}
