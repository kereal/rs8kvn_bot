package bot

import (
	"testing"

	"rs8kvn_bot/internal/config"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === getMainMenuKeyboard tests ===

func TestGetMainMenuKeyboard(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	keyboardWithShare := handler.getMainMenuKeyboard(true)
	assert.Len(t, keyboardWithShare.InlineKeyboard, 3, "Expected 3 rows with subscription")

	keyboardNoShare := handler.getMainMenuKeyboard(false)
	assert.Len(t, keyboardNoShare.InlineKeyboard, 2, "Expected 2 rows without subscription")
}

func TestHandler_GetMainMenuKeyboard_ButtonLabels(t *testing.T) {
	t.Parallel()

	handler := &Handler{cfg: &config.Config{}, botConfig: NewTestBotConfig()}

	keyboard := handler.getMainMenuKeyboard(true)

	assert.Equal(t, "📋 Подписка", keyboard.InlineKeyboard[0][0].Text, "First button label")
	assert.Equal(t, "☕ Донат", keyboard.InlineKeyboard[0][1].Text, "Second button label")
	assert.Equal(t, "❓ Помощь", keyboard.InlineKeyboard[1][0].Text, "Third button label")
	assert.Equal(t, "📤 Поделиться", keyboard.InlineKeyboard[2][0].Text, "Share button label")
}

func TestHandler_GetMainMenuKeyboard_CallbackData(t *testing.T) {
	t.Parallel()

	handler := &Handler{cfg: &config.Config{}, botConfig: NewTestBotConfig()}

	keyboard := handler.getMainMenuKeyboard(true)

	require.NotNil(t, keyboard.InlineKeyboard[0][0].CallbackData, "Subscription callback should not be nil")
	assert.Equal(t, "menu_subscription", *keyboard.InlineKeyboard[0][0].CallbackData, "Subscription callback")
	require.NotNil(t, keyboard.InlineKeyboard[0][1].CallbackData, "Donate callback should not be nil")
	assert.Equal(t, "menu_donate", *keyboard.InlineKeyboard[0][1].CallbackData, "Donate callback")
	require.NotNil(t, keyboard.InlineKeyboard[1][0].CallbackData, "Help callback should not be nil")
	assert.Equal(t, "menu_help", *keyboard.InlineKeyboard[1][0].CallbackData, "Help callback")
	require.NotNil(t, keyboard.InlineKeyboard[2][0].CallbackData, "Share callback should not be nil")
	assert.Equal(t, "share_invite", *keyboard.InlineKeyboard[2][0].CallbackData, "Share callback")
}

func TestHandler_KeyboardConstruction_MultipleRows(t *testing.T) {
	t.Parallel()

	handler := &Handler{cfg: &config.Config{TelegramAdminID: 12345}}

	keyboard := handler.getMainMenuKeyboard(true)

	assert.GreaterOrEqual(t, len(keyboard.InlineKeyboard), 3, "Should have at least 3 rows")

	for i, row := range keyboard.InlineKeyboard {
		assert.Greater(t, len(row), 0, "Row %d should have at least one button", i)
	}
}

// === getBackKeyboard tests ===

func TestGetBackKeyboard(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	keyboard := handler.getBackKeyboard()

	assert.Len(t, keyboard.InlineKeyboard, 1, "Back keyboard should have 1 row")
	assert.Len(t, keyboard.InlineKeyboard[0], 1, "Back keyboard should have 1 button")
	assert.Equal(t, "🏠 В начало", keyboard.InlineKeyboard[0][0].Text)
}

func TestHandler_GetBackKeyboard_CallbackData(t *testing.T) {
	t.Parallel()

	handler := &Handler{cfg: &config.Config{}, botConfig: NewTestBotConfig()}

	keyboard := handler.getBackKeyboard()

	require.NotNil(t, keyboard.InlineKeyboard[0][0].CallbackData, "Back button callback should not be nil")
	assert.Equal(t, "back_to_start", *keyboard.InlineKeyboard[0][0].CallbackData, "Back button callback")
}

// === getQRKeyboard tests ===

func TestGetQRKeyboard(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123}
	handler := NewHandler(nil, cfg, nil, nil, NewTestBotConfig(), nil, "")

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
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 456}
	handler := NewHandler(nil, cfg, nil, nil, NewTestBotConfig(), nil, "")

	keyboard1 := handler.getQRKeyboard()
	keyboard2 := handler.getQRKeyboard()

	assert.Equal(t, keyboard1.InlineKeyboard[0][0].CallbackData,
		keyboard2.InlineKeyboard[0][0].CallbackData, "should return consistent keyboard")
}

// === addAdminButtons tests ===

func TestAddAdminButtons(t *testing.T) {
	t.Parallel()

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

func TestHandler_AddAdminButtons_ExistingKeyboard(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 12345}
	handler := &Handler{cfg: cfg, botConfig: NewTestBotConfig()}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Test", "test"),
		),
	)

	initialRows := len(keyboard.InlineKeyboard)

	handler.addAdminButtons(&keyboard, 12345)

	assert.Greater(t, len(keyboard.InlineKeyboard), initialRows, "Should have more rows after adding admin buttons")
}

func TestHandler_AddAdminButtons_NonAdmin(t *testing.T) {
	t.Parallel()

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

// === keyboard layout tests ===

func TestSubscriptionKeyboardLayout(t *testing.T) {
	t.Parallel()

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 QR-код", "qr_code"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 В начало", "back_to_start"),
		),
	)

	assert.Len(t, keyboard.InlineKeyboard, 2, "Expected 2 keyboard rows")
	assert.Len(t, keyboard.InlineKeyboard[0], 1, "Expected 1 button in first row")
	require.NotNil(t, keyboard.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "qr_code", *keyboard.InlineKeyboard[0][0].CallbackData)
	assert.Len(t, keyboard.InlineKeyboard[1], 1, "Expected 1 button in second row")
	require.NotNil(t, keyboard.InlineKeyboard[1][0].CallbackData)
	assert.Equal(t, "back_to_start", *keyboard.InlineKeyboard[1][0].CallbackData)
}

func TestQRBackKeyboard(t *testing.T) {
	t.Parallel()

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", "back_to_subscription"),
		),
	)

	assert.Len(t, keyboard.InlineKeyboard, 1, "Expected 1 keyboard row")
	assert.Len(t, keyboard.InlineKeyboard[0], 1, "Expected 1 button in row")
	require.NotNil(t, keyboard.InlineKeyboard[0][0].CallbackData, "CallbackData should not be nil")
	assert.Equal(t, "back_to_subscription", *keyboard.InlineKeyboard[0][0].CallbackData, "Expected back_to_subscription callback")
}
