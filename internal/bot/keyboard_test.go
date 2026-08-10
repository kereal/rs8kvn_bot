package bot

import (
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// §7.5 UI tests for the new payment-entry callbacks.

func TestGetMainMenuKeyboard_PaymentShown(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123, DonateEnabled: true, PaymentEnabled: true}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig(), keyboards: NewKeyboardBuilder("testbot", "", "", "", "", true), paymentEnabled: true}

	keyboardWithShare := handler.getMainMenuKeyboard(true)
	assert.Len(t, keyboardWithShare.InlineKeyboard, 4, "Expected 4 rows: subscription menu, help/documents, payment, share")

	keyboardNoShare := handler.getMainMenuKeyboard(false)
	assert.Len(t, keyboardNoShare.InlineKeyboard, 2, "No payment button without active subscription")
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

	assert.Equal(t, "📋 Подписка", keyboard.InlineKeyboard[0][0].Text)
	assert.Equal(t, "☕ Донат", keyboard.InlineKeyboard[0][1].Text)
	assert.Equal(t, "❓ Помощь", keyboard.InlineKeyboard[1][0].Text)
	assert.Equal(t, "📑 Документы", keyboard.InlineKeyboard[1][1].Text)
	assert.Equal(t, "💎 Купить Premium", keyboard.InlineKeyboard[2][0].Text, "§7.1 label")
	assert.Equal(t, "📤 Поделиться", keyboard.InlineKeyboard[3][0].Text)
}

func TestHandler_GetMainMenuKeyboard_CallbackData(t *testing.T) {
	t.Parallel()

	handler := &Handler{cfg: &config.Config{PaymentEnabled: true}, botConfig: NewTestBotConfig(), paymentEnabled: true}
	handler.keyboards = NewKeyboardBuilder("", "", "", "", "", true)

	keyboard := handler.getMainMenuKeyboard(true)

	require.NotNil(t, keyboard.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "menu_subscription", *keyboard.InlineKeyboard[0][0].CallbackData)
	require.NotNil(t, keyboard.InlineKeyboard[0][1].CallbackData)
	assert.Equal(t, "menu_donate", *keyboard.InlineKeyboard[0][1].CallbackData)
	require.NotNil(t, keyboard.InlineKeyboard[1][0].CallbackData)
	assert.Equal(t, "menu_help", *keyboard.InlineKeyboard[1][0].CallbackData)
	require.NotNil(t, keyboard.InlineKeyboard[1][1].CallbackData)
	assert.Equal(t, "menu_documents", *keyboard.InlineKeyboard[1][1].CallbackData)
	require.NotNil(t, keyboard.InlineKeyboard[2][0].CallbackData)
	assert.Equal(t, "buy_premium_list", *keyboard.InlineKeyboard[2][0].CallbackData, "§7.1 callback")
	require.NotNil(t, keyboard.InlineKeyboard[3][0].CallbackData)
	assert.Equal(t, "share_invite", *keyboard.InlineKeyboard[3][0].CallbackData)
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
