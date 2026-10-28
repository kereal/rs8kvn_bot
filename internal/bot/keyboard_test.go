package bot

import (
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// §7.5 UI tests for the new payment-entry callbacks.

func TestGetMainMenuKeyboard_PaymentShown(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123, DonateEnabled: true, PaymentEnabled: true}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", "", "", "", "", true), paymentEnabled: true}

	hasPayment := func(kb tgbotapi.InlineKeyboardMarkup) bool {
		for _, row := range kb.InlineKeyboard {
			for _, btn := range row {
				if btn.CallbackData != nil && *btn.CallbackData == "buy_premium_list" {
					return true
				}
			}
		}
		return false
	}

	hasShare := func(kb tgbotapi.InlineKeyboardMarkup) bool {
		for _, row := range kb.InlineKeyboard {
			for _, btn := range row {
				if btn.CallbackData != nil && *btn.CallbackData == "share_invite" {
					return true
				}
			}
		}
		return false
	}

	keyboardWithShare := handler.getMainMenuKeyboard(true)
	assert.True(t, hasPayment(keyboardWithShare), "payment button should be present when PaymentEnabled")
	assert.True(t, hasShare(keyboardWithShare), "share button should be present with active subscription")

	keyboardNoShare := handler.getMainMenuKeyboard(false)
	assert.False(t, hasPayment(keyboardNoShare), "payment entry requires an active subscription")
	assert.False(t, hasShare(keyboardNoShare), "share button must be hidden without active subscription")
}

func TestGetMainMenuKeyboard_PaymentHidden(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123, DonateEnabled: true, PaymentEnabled: false}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", "", "", "", "", true), paymentEnabled: false}

	keyboard := handler.getMainMenuKeyboard(true)
	for _, row := range keyboard.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData != nil {
				assert.NotEqual(t, "buy_premium_list", *btn.CallbackData, "no payment entry when PaymentEnabled=false")
				assert.NotEqual(t, "menu_payment", *btn.CallbackData, "no legacy menu_payment callback")
			}
		}
	}
}

func TestHandler_GetMainMenuKeyboard_ButtonLabels(t *testing.T) {
	t.Parallel()

	handler := &Handler{cfg: &config.Config{PaymentEnabled: true}, botConfig: NewTestBotConfig(), paymentEnabled: true}
	handler.keyboards = NewKeyboardBuilder("", "", "", "", "", true)

	keyboard := handler.getMainMenuKeyboard(true)

	allTexts := make([]string, 0)
	for _, row := range keyboard.InlineKeyboard {
		for _, btn := range row {
			allTexts = append(allTexts, btn.Text)
		}
	}
	joined := strings.Join(allTexts, " ")

	// Check markers instead of exact rows/strings so the test survives menu
	// reordering or a new row being added.
	for _, marker := range []string{"Подписка", "Донат", "Помощь", "Документы", "Premium", "Поделиться"} {
		assert.Contains(t, joined, marker, "main menu should contain button label %q", marker)
	}
}

func TestHandler_GetMainMenuKeyboard_CallbackData(t *testing.T) {
	t.Parallel()

	handler := &Handler{cfg: &config.Config{PaymentEnabled: true}, botConfig: NewTestBotConfig(), paymentEnabled: true}
	handler.keyboards = NewKeyboardBuilder("", "", "", "", "", true)

	keyboard := handler.getMainMenuKeyboard(true)

	callbacks := make([]string, 0)
	for _, row := range keyboard.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData != nil {
				callbacks = append(callbacks, *btn.CallbackData)
			}
		}
	}

	// Check markers instead of exact positions: every route must be reachable
	// from the main menu regardless of lay-out order.
	for _, want := range []string{"menu_subscription", "menu_donate", "menu_help", "menu_documents", "buy_premium_list", "share_invite"} {
		assert.Contains(t, callbacks, want, "main menu should expose callback %q", want)
	}
}

func TestHandler_BuyProductList_Callback(t *testing.T) {
	t.Parallel()

	kb := NewKeyboardBuilder("testbot", "", "", "", "", true)
	products := newTestProductList()
	keyboard := kb.BuyProductList(products)

	require.NotEmpty(t, keyboard.InlineKeyboard)

	for _, row := range keyboard.InlineKeyboard[:len(keyboard.InlineKeyboard)-1] {
		require.NotNil(t, row[0].CallbackData)
		assert.Contains(t, *row[0].CallbackData, "buy_product_", "product rows use buy_product_{id} callbacks")
	}

	last := keyboard.InlineKeyboard[len(keyboard.InlineKeyboard)-1][0]
	require.NotNil(t, last.CallbackData)
	assert.Equal(t, "back_to_start", *last.CallbackData, "last row is back navigation")
}

func TestKeyboardBuilder_BuyProductConfirm_UsesCardIcon(t *testing.T) {
	t.Parallel()

	kb := NewKeyboardBuilder("testbot", "", "", "", "", true)

	tests := []struct {
		name       string
		priceCents int64
		wantText   string
	}{
		{name: "whole rubles drop kopecks", priceCents: 19900, wantText: "💳 Оплатить 199₽"},
		{name: "fractional rubles keep kopecks", priceCents: 19999, wantText: "💳 Оплатить 199.99₽"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			keyboard := kb.BuyProductConfirm(&database.Product{PriceCents: tt.priceCents}, "https://pay.example")

			require.NotEmpty(t, keyboard.InlineKeyboard)
			assert.Equal(t, tt.wantText, keyboard.InlineKeyboard[0][0].Text)
		})
	}
}

func TestKeyboardBuilder_BuyProductList_FormatsPrice(t *testing.T) {
	t.Parallel()

	kb := NewKeyboardBuilder("testbot", "", "", "", "", true)
	products := []database.Product{
		{ID: 1, Name: "Месяц", PriceCents: 19900, IsActive: true},
		{ID: 2, Name: "Год", PriceCents: 19999, IsActive: true},
	}
	keyboard := kb.BuyProductList(products)

	require.Len(t, keyboard.InlineKeyboard, 3)
	assert.Equal(t, "Месяц — 199₽", keyboard.InlineKeyboard[0][0].Text)
	assert.Equal(t, "Год — 199.99₽", keyboard.InlineKeyboard[1][0].Text)
}

func newTestProductList() []database.Product {
	return []database.Product{
		{ID: 1, Name: "Месяц", PriceCents: 19900, IsActive: true},
		{ID: 2, Name: "Год", PriceCents: 199900, IsActive: true},
	}
}

func TestHandler_KeyboardConstruction_MultipleRows(t *testing.T) {
	t.Parallel()

	handler := &Handler{cfg: &config.Config{TelegramAdminID: 12345}, paymentEnabled: true}

	keyboard := handler.getMainMenuKeyboard(true)

	assert.GreaterOrEqual(t, len(keyboard.InlineKeyboard), 3, "Should have at least 3 rows")

	for i, row := range keyboard.InlineKeyboard {
		assert.Greater(t, len(row), 0, "Row %d should have at least one button", i)
	}
}

func TestHandler_MainMenu_DonateHidden(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123, DonateEnabled: false}
	h := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", cfg.ContactUsername, cfg.DonateCardNumber, cfg.DonateURL, cfg.SiteURL, false)}

	kb := h.getMainMenuKeyboard(false)

	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData != nil {
				assert.NotEqual(t, "menu_donate", *btn.CallbackData, "Donate button must be hidden when DonateEnabled=false")
			}
		}
	}
}
