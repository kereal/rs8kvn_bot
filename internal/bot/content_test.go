package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
)

// === getMainMenuContent tests ===

// newContentTestHandler builds a bare content-handler with the keyboards
// builder wired from cfg, so content tests don't repeat the Handler literal.
func newContentTestHandler(cfg *config.Config) *Handler {
	return &Handler{
		cfg:       cfg,
		botConfig: NewTestBotConfig(),
		keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL, true),
	}
}

func TestGetMainMenuContent_WithSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 0,
	}
	handler := newContentTestHandler(cfg)

	text, keyboard := handler.getMainMenuContent(context.Background(), "testuser", true, 123456789, nil)

	assert.NotEmpty(t, text, "Expected non-empty text for user with subscription")
	assert.Contains(t, text, "testuser", "Expected text to contain username")
	assert.GreaterOrEqual(t, len(keyboard.InlineKeyboard), 2, "Expected at least 2 keyboard rows")
}

func TestGetMainMenuContent_FreeSubscriptionShowsPremiumTeaser(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 0, PaymentEnabled: true}
	db := testutil.NewBotStoreFake()
	db.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 2, Name: database.FreePlanName}, nil
	}
	handler := newContentTestHandler(cfg)
	handler.db = db
	handler.paymentEnabled = true

	text, _ := handler.getMainMenuContent(context.Background(), "testuser", true, 123456789, &database.Subscription{PlanID: 2})

	assert.Contains(t, text, "💎 Premium — безлимитный трафик и больше серверов")
	assert.NotContains(t, text, "Вы — 💎 Premium")
}

func TestGetMainMenuContent_PaidSubscriptionShowsPremiumBadge(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 0}
	handler := newContentTestHandler(cfg)

	productID := uint(5)

	text, _ := handler.getMainMenuContent(context.Background(), "testuser", true, 123456789, &database.Subscription{ProductID: &productID})

	assert.Contains(t, text, "Вы — 💎 Premium")
	assert.NotContains(t, text, "💎 Premium — безлимитный трафик и больше серверов")
}

func TestGetMainMenuContent_PaidByAmountShowsPremiumBadge(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 0}
	handler := newContentTestHandler(cfg)

	text, _ := handler.getMainMenuContent(context.Background(), "testuser", true, 123456789, &database.Subscription{PricePaidCents: 25000})

	assert.Contains(t, text, "Вы — 💎 Premium")
}

func TestGetMainMenuContent_WithoutSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 0,
	}
	handler := newContentTestHandler(cfg)

	text, keyboard := handler.getMainMenuContent(context.Background(), "testuser", false, 123456789, nil)

	assert.NotEmpty(t, text, "Expected non-empty text for user without subscription")
	assert.Contains(t, text, "testuser", "Expected text to contain username")
	assert.GreaterOrEqual(t, len(keyboard.InlineKeyboard), 1, "Expected at least 1 keyboard row")

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
	handler := newContentTestHandler(cfg)

	_, keyboard := handler.getMainMenuContent(context.Background(), "admin", true, 123456789, nil)

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

// TestHandler_GetMainMenuContent_SpecialCharacters verifies usernames with
// control chars, emoji, unicode and extreme length flow through the greeting
// text unmodified.
func TestHandler_GetMainMenuContent_SpecialCharacters(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 12345}
	handler := newContentTestHandler(cfg)

	specialUsernames := []string{
		"test_user_123",
		"user\nwith\nnewlines",
		"tab\there",
		"user\x00with\x00nulls",
		"emoji🎉🔥🚀",
		"мир 世界 세계",
		strings.Repeat("a", 1000), // Very long
	}

	for _, username := range specialUsernames {
		t.Run(fmt.Sprintf("username_len_%d", len(username)), func(t *testing.T) {
			t.Parallel()

			text, keyboard := handler.getMainMenuContent(context.Background(), username, true, 12345, nil)
			assert.Contains(t, text, username)
			assert.NotEmpty(t, keyboard.InlineKeyboard, "Keyboard should not be empty")
		})
	}
}

func TestGetDonateText(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	handler := newContentTestHandler(cfg)

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
	handler := newContentTestHandler(cfg)

	text := handler.getDonateText()

	assert.Contains(t, text, "Поддержка проекта", "Should contain header")
	assert.Contains(t, text, "2200110022334455", "Should contain card number from config")
	assert.Contains(t, text, "https://www.tbank.ru/collection/abc", "Should contain donate URL from config")
	assert.Contains(t, text, "t.me/kereal", "Should contain contact link")
}
