package e2e

import (
	"context"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
)

func TestE2E_MenuHelp(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "test-client-id",
		SubscriptionID: "test-sub-id",
		Status:         "active",
	}
	createE2ESub(t, env.db, sub)

	resetBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "menu_help",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Help should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Ваша подписка готова", "Should contain subscription help text")
}

func TestE2E_MenuDonate(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "menu_donate",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Donate info should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Поддержка", "Should contain donate info")
}

func TestE2E_BackToStart(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "test-client-id",
		SubscriptionID: "test-sub-id",
		Status:         "active",
	}
	createE2ESub(t, env.db, sub)

	resetBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "back_to_start",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Main menu should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "кнопки ниже", "Should show subscription menu")
}

func TestE2E_Callback_ShareInvite(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	resetBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "share_invite",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Invite link should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "t.me", "Should contain Telegram invite link")
}

// TestE2E_Callback_QRLinks covers both QR entry points opened from the invite
// screen; the open/delete navigation contract itself is asserted by the unit
// guard TestNavigation_OpenAndBack.
func TestE2E_Callback_QRLinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
	}{
		{name: "qr_telegram", data: "qr_telegram"},
		{name: "qr_web", data: "qr_web"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupE2EEnv(t)
			defer env.db.Close()

			ctx := context.Background()

			resetBotAPI(env.botAPI)

			env.handler.HandleCallback(ctx, tgbotapi.Update{
				CallbackQuery: &tgbotapi.CallbackQuery{
					From: &tgbotapi.User{
						ID:       env.chatID,
						UserName: env.username,
					},
					Data: tt.data,
					Message: &tgbotapi.Message{
						Chat:      &tgbotapi.Chat{ID: env.chatID},
						MessageID: 100,
					},
				},
			})

			assert.True(t, env.botAPI.SendCalledSafe(), "QR code should be sent")
		})
	}
}
