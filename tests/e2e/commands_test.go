package e2e

import (
	"context"
	"testing"

	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestE2E_StartCommand_NoSubscription(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: env.chatID},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Text: "/start",
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Main menu should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Привет", "Should greet user")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")
}

func TestE2E_StartCommand_WithSubscription(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/test-sub-id",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: env.chatID},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Text: "/start",
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Subscription menu should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "кнопки ниже", "Should show menu with buttons")
}

func TestE2E_MySubscription(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/test-sub-id",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	resetMockBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "menu_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Subscription info should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")
	assert.Contains(t, env.botAPI.LastSentText, "https://example.com/sub/test-sub-id", "Should contain subscription URL")
}

func TestE2E_HelpCommand(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleHelp(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: env.chatID},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Text: "/help",
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Help text should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Справка", "Should contain help text")
}

func TestE2E_InviteCommand(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleInvite(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: env.chatID},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Text: "/invite",
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Invite link should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "пригласительная ссылка", "Should mention invite link")
	assert.Contains(t, env.botAPI.LastSentText, "t.me/testbot?start=share_", "Should contain telegram invite URL")
}

func TestE2E_StartCommand_AdminUser(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	cfg := &config.Config{TelegramAdminID: env.chatID}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := bot.NewHandler(mockBot, cfg, mockDB, mockXUI, &bot.BotConfig{
		Username: "testbot", ID: 123456789, FirstName: "TestBot", IsBot: true,
	}, nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	handler.HandleStart(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: env.chatID},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Text: "/start",
		},
	})

	assert.True(t, mockBot.SendCalledSafe(), "Admin should get start menu")
}
