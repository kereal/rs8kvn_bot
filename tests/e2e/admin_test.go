package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_DelCommand_Success(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)

	subID := sub.ID

	resetBotAPI(env.botAPI)

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{
				ID:       adminID,
				UserName: "admin",
			},
			Text: fmt.Sprintf("/del %d", subID),
			Entities: []tgbotapi.MessageEntity{
				{Type: "bot_command", Offset: 0, Length: 4},
			},
		},
	}
	env.handler.HandleDel(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentText, "Подписка успешно удалена")
	assert.Contains(t, env.botAPI.LastSentText, fmt.Sprintf("%d", subID))

	_, err = env.db.GetByID(ctx, subID)
	assert.Error(t, err, "Subscription should be deleted")

	// Sync-based: XUI is called via sync module, not directly in DeleteByID()
}

func TestE2E_DelCommand_ArgValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cmd     string
		wantMsg string
	}{
		{name: "no_args", cmd: "/del", wantMsg: "Использование: /del"},
		{name: "invalid_id", cmd: "/del not-a-number", wantMsg: "Неверный формат ID"},
		{name: "negative_id", cmd: "/del -1", wantMsg: "положительным числом"},
		{name: "not_found", cmd: "/del 99999", wantMsg: "Ошибка удаления подписки"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupE2EEnv(t)
			defer func() {
				err := env.db.Close()
				if err != nil {
					t.Logf("Warning: failed to close database: %v", err)
				}
			}()

			ctx := context.Background()
			adminID := env.cfg.TelegramAdminID
			resetBotAPI(env.botAPI)

			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: adminID},
					From: &tgbotapi.User{
						ID:       adminID,
						UserName: "admin",
					},
					Text:     tt.cmd,
					Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 4}},
				},
			}
			env.handler.HandleDel(ctx, update)

			assert.True(t, env.botAPI.SendCalledSafe())
			assert.Contains(t, env.botAPI.LastSentText, tt.wantMsg)
		})
	}
}

func TestE2E_DelCommand_XUIFailure(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)

	env.xui.DeleteClientFunc = func(ctx context.Context, email string) error {
		return fmt.Errorf("xui delete: connection refused")
	}

	resetBotAPI(env.botAPI)

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{
				ID:       adminID,
				UserName: "admin",
			},
			Text:     fmt.Sprintf("/del %d", sub.ID),
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 4}},
		},
	}
	env.handler.HandleDel(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe())
	// Failed external deprovisioning keeps the subscription revoked so the
	// background sync worker can retry the pending_remove operation.
	assert.Contains(t, env.botAPI.LastSentText, "Ошибка удаления подписки")

	stored, err := env.db.GetByID(ctx, sub.ID)
	require.NoError(t, err, "revoked subscription must remain for retry")
	assert.Equal(t, database.SubscriptionStatusRevoked, database.SubscriptionStatus(stored.Status))
}

func TestE2E_SetPlanCommand_Success(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)

	// Seed a premium plan and link the existing node to it.
	premiumPlan := &database.Plan{Name: "e2e-premium", DevicesLimit: 2, TrafficLimit: 1024}
	require.NoError(t, env.db.GetDB().WithContext(ctx).Create(premiumPlan).Error)
	require.NoError(t, env.db.LinkNodeToPlan(ctx, premiumPlan.Name, 1), "link node to premium plan")

	resetBotAPI(env.botAPI)

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{
				ID:       adminID,
				UserName: "admin",
			},
			Text:     fmt.Sprintf("/setplan %d %d 30", sub.ID, premiumPlan.ID),
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 7}},
		},
	}
	env.handler.HandleSetPlan(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentText, "Тариф подписки изменён")

	stored, err := env.db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, premiumPlan.ID, stored.PlanID)
	require.NotNil(t, stored.ExpiresAt)
	assert.WithinDuration(t, time.Now().Add(30*24*time.Hour), *stored.ExpiresAt, time.Minute)

	// Node bindings were reconciled and the best-effort sync pushed the new
	// plan to the panel: the shared node ends up active with the new plan.
	nodes, err := env.db.GetBySubscriptionID(ctx, sub.ID)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, database.SyncStatusActive, nodes[0].Status)
}

func TestE2E_SetPlanCommand_ArgValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cmd     string
		wantMsg string
	}{
		{name: "no_args", cmd: "/setplan", wantMsg: "Использование: /setplan"},
		{name: "invalid_sub_id", cmd: "/setplan abc 2", wantMsg: "Неверный ID подписки"},
		{name: "invalid_plan_id", cmd: "/setplan 5 abc", wantMsg: "Неверный ID тарифа"},
		{name: "days_too_large", cmd: "/setplan 5 3 99999", wantMsg: "Количество дней не может превышать"},
		{name: "not_found", cmd: "/setplan 99999 2", wantMsg: "Ошибка смены тарифа"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupE2EEnv(t)
			defer func() {
				err := env.db.Close()
				if err != nil {
					t.Logf("Warning: failed to close database: %v", err)
				}
			}()

			ctx := context.Background()
			adminID := env.cfg.TelegramAdminID
			resetBotAPI(env.botAPI)

			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: adminID},
					From: &tgbotapi.User{
						ID:       adminID,
						UserName: "admin",
					},
					Text:     tt.cmd,
					Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 7}},
				},
			}
			env.handler.HandleSetPlan(ctx, update)

			assert.True(t, env.botAPI.SendCalledSafe())
			assert.Contains(t, env.botAPI.LastSentText, tt.wantMsg)
		})
	}
}

// broadcastName is the fixed broadcast name used by runBroadcastFlow.
const broadcastName = "E2E рассылка"

// runBroadcastFlow drives the name -> draft -> preview -> confirm broadcast
// flow end to end.
func runBroadcastFlow(t *testing.T, env *e2eTestEnv, adminID int64, draftText string) {
	t.Helper()

	ctx := context.Background()

	env.handler.HandleBroadcast(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat:     &tgbotapi.Chat{ID: adminID},
			From:     &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text:     "/broadcast",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 10}},
		},
	})

	env.handler.HandleBroadcastDraft(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text: broadcastName,
		},
	})

	env.handler.HandleBroadcastDraft(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text: draftText,
		},
	})

	// Шаг 1: показать количество получателей.
	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Data: "broadcast_confirm",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: adminID},
				MessageID: 1,
			},
		},
	})

	// Шаг 2: подтвердить и запустить рассылку.
	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Data: "broadcast_final_confirm",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: adminID},
				MessageID: 1,
			},
		},
	})
}

func TestE2E_BroadcastCommand_Success(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	for i := range 3 {
		chatID := int64(300000 + i)
		_, err := env.subService.Create(ctx, chatID, fmt.Sprintf("user%d", i), "")
		require.NoError(t, err)
	}

	resetBotAPI(env.botAPI)

	runBroadcastFlow(t, env, adminID, "Hello everyone!")

	b := waitForBroadcastFinished(t, env)
	assert.True(t, env.botAPI.SendCalledSafe())
	assert.GreaterOrEqual(t, env.botAPI.SendCountSafe(), 3, "Should send to at least 3 users")
	assert.Contains(t, env.botAPI.LastSentTextSafe(), "Рассылка завершена")
	assert.Contains(t, env.botAPI.LastSentTextSafe(), "Рассылка #1")

	// Рассылка сохранена со счётчиками и JSON-отчётом.
	assert.Equal(t, broadcastName, b.Name)
	assert.Equal(t, string(database.BroadcastStatusCompleted), b.Status)
	assert.Equal(t, int64(3), b.RecipientsTotal)
	assert.Equal(t, int64(3), b.SentCount)
	assert.Equal(t, int64(0), b.BlockedCount)
	assert.Equal(t, int64(0), b.FailedCount)

	report, err := b.ParseDeliveryReport()
	require.NoError(t, err)
	assert.Len(t, report.Delivered, 3)
	assert.Empty(t, report.Blocked)
	assert.Empty(t, report.Errors)
}

func TestE2E_BroadcastCommand_NoArgs(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID
	resetBotAPI(env.botAPI)

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{
				ID:       adminID,
				UserName: "admin",
			},
			Text:     "/broadcast",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 10}},
		},
	}
	env.handler.HandleBroadcast(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentText, "название")
}

func TestE2E_BroadcastCommand_NoUsers(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	adminID := env.cfg.TelegramAdminID
	resetBotAPI(env.botAPI)

	runBroadcastFlow(t, env, adminID, "Hello")

	b := waitForBroadcastFinished(t, env)
	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Equal(t, int64(0), b.RecipientsTotal)
	assert.Contains(t, env.botAPI.LastSentTextSafe(), "Отправлено: 0")
}

func TestE2E_BroadcastCommand_SomeFailures(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	for i := range 3 {
		chatID := int64(400000 + i)
		_, err := env.subService.Create(ctx, chatID, fmt.Sprintf("user%d", i), "")
		require.NoError(t, err)
	}

	resetBotAPI(env.botAPI)

	// Drive the flow manually so the preview send (before confirm) succeeds,
	// then make the actual user sends fail.
	env.handler.HandleBroadcast(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat:     &tgbotapi.Chat{ID: adminID},
			From:     &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text:     "/broadcast",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 10}},
		},
	})
	env.handler.HandleBroadcastDraft(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text: broadcastName,
		},
	})
	env.handler.HandleBroadcastDraft(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text: "Hello",
		},
	})
	// Шаг 1: показать количество получателей (без ошибки).
	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Data: "broadcast_confirm",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: adminID},
				MessageID: 1,
			},
		},
	})

	// Шаг 2: установить ошибку и запустить рассылку.
	env.botAPI.SendError = fmt.Errorf("send failed")
	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Data: "broadcast_final_confirm",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: adminID},
				MessageID: 1,
			},
		},
	})

	_ = waitForBroadcastFinished(t, env)
	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentTextSafe(), "Рассылка завершена")
	assert.Contains(t, env.botAPI.LastSentTextSafe(), "Ошибок: 3")
}

func TestE2E_SendCommand_ByTelegramID(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	resetBotAPI(env.botAPI)

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{
				ID:       adminID,
				UserName: "admin",
			},
			Text:     fmt.Sprintf("/send %d Hello via ID!", env.chatID),
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
		},
	}
	env.handler.HandleSend(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentText, "Сообщение отправлено")
}

func TestE2E_SendCommand_ByUsername(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	resetBotAPI(env.botAPI)

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{
				ID:       adminID,
				UserName: "admin",
			},
			Text:     fmt.Sprintf("/send %s Hello via username!", env.username),
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
		},
	}
	env.handler.HandleSend(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentText, "Сообщение отправлено")
}

func TestE2E_SendCommand_ArgValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cmd     string
		wantMsg string
	}{
		{name: "no_args", cmd: "/send", wantMsg: "Использование: /send"},
		{name: "only_target_no_message", cmd: "/send 123456", wantMsg: "Использование"},
		{name: "user_not_found", cmd: "/send nonexistent_user Hello!", wantMsg: "не найден в базе"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupE2EEnv(t)
			defer func() {
				err := env.db.Close()
				if err != nil {
					t.Logf("Warning: failed to close database: %v", err)
				}
			}()

			ctx := context.Background()
			adminID := env.cfg.TelegramAdminID
			resetBotAPI(env.botAPI)

			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat:     &tgbotapi.Chat{ID: adminID},
					From:     &tgbotapi.User{ID: adminID, UserName: "admin"},
					Text:     tt.cmd,
					Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
				},
			}
			env.handler.HandleSend(ctx, update)

			assert.True(t, env.botAPI.SendCalledSafe())
			assert.Contains(t, env.botAPI.LastSentText, tt.wantMsg)
		})
	}
}

func TestE2E_SendCommand_ByTelegramID_SendError(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	resetBotAPI(env.botAPI)
	env.botAPI.SendError = fmt.Errorf("send error")

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{
				ID:       adminID,
				UserName: "admin",
			},
			Text:     fmt.Sprintf("/send %d Hello!", env.chatID),
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
		},
	}
	env.handler.HandleSend(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentText, "Ошибка отправки")
}

func TestE2E_SendCommand_WithAtPrefix(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat:     &tgbotapi.Chat{ID: adminID},
			From:     &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text:     fmt.Sprintf("/send @%s Hello!", env.username),
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
		},
	}
	env.handler.HandleSend(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentText, "Сообщение отправлено")
}

func TestE2E_SendCommand_RateLimit(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	send := func(text string) {
		env.handler.HandleSend(ctx, tgbotapi.Update{
			Message: &tgbotapi.Message{
				Chat:     &tgbotapi.Chat{ID: adminID},
				From:     &tgbotapi.User{ID: adminID, UserName: "admin"},
				Text:     text,
				Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
			},
		})
	}

	// First send consumes the single admin token and succeeds.
	resetBotAPI(env.botAPI)
	send(fmt.Sprintf("/send %d Message 1", env.chatID))
	assert.Contains(t, env.botAPI.LastSentText, "Сообщение отправлено", "First send should succeed")

	// Second send in the same minute must be blocked by the admin rate limit.
	resetBotAPI(env.botAPI)
	send(fmt.Sprintf("/send %d Message 2", env.chatID))
	assert.Contains(t, env.botAPI.LastSentText, "Слишком много сообщений",
		"Second send should be blocked by the admin rate limit")

	// Clearing the limit lets the admin send again immediately.
	env.handler.ClearAdminSendRateLimit(adminID)
	resetBotAPI(env.botAPI)
	send(fmt.Sprintf("/send %d Message 3", env.chatID))
	assert.Contains(t, env.botAPI.LastSentText, "Сообщение отправлено",
		"Send should succeed again after clearing the rate limit")
}

func TestE2E_BroadcastCommand_PreservesFormatting(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, int64(950001), "testuser", "")
	require.NoError(t, err)

	resetBotAPI(env.botAPI)

	const draft = "Test *bold* _italic_ [link](https://example.com)"
	runBroadcastFlow(t, env, adminID, draft)

	_ = waitForBroadcastFinished(t, env)
	assert.Contains(t, env.botAPI.LastSentTextSafe(), "Рассылка завершена")

	// Formatting must be delivered as-is (MarkdownV2, not escaped with backslashes).
	var delivered bool

	for _, m := range env.botAPI.GetAllSentMessages() {
		if strings.Contains(m.Text, "*bold*") && !strings.Contains(m.Text, `\*bold\*`) {
			delivered = true
		}
	}

	assert.True(t, delivered, "delivered message should keep MarkdownV2 formatting unescaped")
}

func TestE2E_SendCommand_EscapesMarkdown(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat:     &tgbotapi.Chat{ID: adminID},
			From:     &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text:     fmt.Sprintf("/send %d *bold* and _italic_", env.chatID),
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
		},
	}
	env.handler.HandleSend(ctx, update)

	// The outbound message to the target user must have reserved MarkdownV2
	// characters escaped so plain text is not interpreted as formatting.
	var escaped string
	for _, m := range env.botAPI.GetAllSentMessages() {
		if m.ChatID == env.chatID {
			escaped = m.Text
		}
	}

	require.NotEmpty(t, escaped, "message to target user should be captured")
	assert.Contains(t, escaped, `\*bold\*`)
	assert.Contains(t, escaped, `\_italic\_`)
}

func TestE2E_NonAdmin_AccessControl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		setupEnv     func(*e2eTestEnv, context.Context)
		wantNotSent  bool
		sentContains string
	}{
		{
			name: "cannot_use_del",
			setupEnv: func(env *e2eTestEnv, ctx context.Context) {
				env.handler.HandleDel(ctx, tgbotapi.Update{
					Message: &tgbotapi.Message{
						Chat:     &tgbotapi.Chat{ID: 999999},
						From:     &tgbotapi.User{ID: 999999, UserName: "notadmin"},
						Text:     "/del 1",
						Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 4}},
					},
				})
			},
			wantNotSent: true,
		},
		{
			name: "cannot_use_broadcast",
			setupEnv: func(env *e2eTestEnv, ctx context.Context) {
				env.handler.HandleBroadcast(ctx, tgbotapi.Update{
					Message: &tgbotapi.Message{
						Chat:     &tgbotapi.Chat{ID: 999999},
						From:     &tgbotapi.User{ID: 999999, UserName: "notadmin"},
						Text:     "/broadcast Hello",
						Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 10}},
					},
				})
			},
			wantNotSent: true,
		},
		{
			name: "cannot_use_send",
			setupEnv: func(env *e2eTestEnv, ctx context.Context) {
				env.handler.HandleSend(ctx, tgbotapi.Update{
					Message: &tgbotapi.Message{
						Chat:     &tgbotapi.Chat{ID: 999999},
						From:     &tgbotapi.User{ID: 999999, UserName: "notadmin"},
						Text:     "/send 123456789 Hello",
						Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
					},
				})
			},
			wantNotSent: true,
		},
		{
			name: "cannot_access_refstats",
			setupEnv: func(env *e2eTestEnv, ctx context.Context) {
				env.handler.HandleRefstats(ctx, tgbotapi.Update{
					Message: &tgbotapi.Message{
						Chat:     &tgbotapi.Chat{ID: 999999},
						From:     &tgbotapi.User{ID: 999999, UserName: "notadmin"},
						Text:     "/refstats",
						Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 9}},
					},
				})
			},
			wantNotSent:  false,
			sentContains: "только администратору",
		},
		{
			name: "cannot_access_admin_stats",
			setupEnv: func(env *e2eTestEnv, ctx context.Context) {
				env.handler.HandleCallback(ctx, tgbotapi.Update{
					CallbackQuery: &tgbotapi.CallbackQuery{
						From: &tgbotapi.User{ID: 999999, UserName: "notadmin"},
						Data: "admin_stats",
						Message: &tgbotapi.Message{
							Chat:      &tgbotapi.Chat{ID: 999999},
							MessageID: 100,
						},
					},
				})
			},
			wantNotSent: true,
		},
		{
			name: "cannot_access_admin_lastreg",
			setupEnv: func(env *e2eTestEnv, ctx context.Context) {
				env.handler.HandleCallback(ctx, tgbotapi.Update{
					CallbackQuery: &tgbotapi.CallbackQuery{
						From: &tgbotapi.User{ID: 999999, UserName: "notadmin"},
						Data: "admin_lastreg",
						Message: &tgbotapi.Message{
							Chat:      &tgbotapi.Chat{ID: 999999},
							MessageID: 100,
						},
					},
				})
			},
			wantNotSent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupE2EEnv(t)
			defer func() {
				err := env.db.Close()
				if err != nil {
					t.Logf("Warning: failed to close database: %v", err)
				}
			}()

			ctx := context.Background()

			resetBotAPI(env.botAPI)
			tt.setupEnv(env, ctx)

			if tt.wantNotSent {
				assert.False(t, env.botAPI.SendCalledSafe())
			} else {
				assert.True(t, env.botAPI.SendCalledSafe())
				assert.Contains(t, env.botAPI.LastSentText, tt.sentContains)
			}
		})
	}
}

func TestE2E_AdminLastReg(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "test-client-id",
		SubscriptionID: "test-sub-id",
		Status:         "active",
		CreatedAt:      time.Now(),
	}
	createE2ESub(t, env.db, sub)

	resetBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.cfg.TelegramAdminID,
				UserName: "admin",
			},
			Data: "admin_lastreg",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.cfg.TelegramAdminID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Last registrations should be sent")
	assert.Contains(t, env.botAPI.LastSentText, env.username, "Should show registered user")
}

func TestE2E_VersionCommand_Admin(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID
	resetBotAPI(env.botAPI)

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{
				ID:       adminID,
				UserName: "admin",
			},
			Text:     "/v",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 2}},
		},
	}
	env.handler.HandleVersion(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe(), "Admin should receive version info")
}

func TestE2E_VersionCommand_NonAdmin(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	nonAdminID := int64(999999)

	resetBotAPI(env.botAPI)

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: nonAdminID},
			From: &tgbotapi.User{
				ID:       nonAdminID,
				UserName: "notadmin",
			},
			Text:     "/v",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 2}},
		},
	}
	env.handler.HandleVersion(ctx, update)

	assert.False(t, env.botAPI.SendCalledSafe(), "Non-admin should not receive version info")
}
