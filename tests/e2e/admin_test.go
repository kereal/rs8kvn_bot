package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/xui"

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

	// The client was provisioned by Create's best-effort sync, but the stateless
	// fake does not remember it. The update path now reads the panel profile
	// before writing (it must preserve admin edits), so teach the fake to
	// answer that read the way a real panel would for an existing client.
	env.xui.GetClientByEmailFunc = func(ctx context.Context, email string) (*xui.ClientRecord, error) {
		return &xui.ClientRecord{
			Client: xui.ClientConfig{
				Email:           email,
				Enable:          true,
				Reset:           30,
				TrafficReset:    "monthly",
				TrafficResetDay: 1,
			},
			InboundIDs: []int{1},
		}, nil
	}

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
		// no_args / only_target are covered by unit tests
		// TestHandleSend_NoArguments and TestHandleSend_OnlyTarget; only the
		// DB-backed lookup remains here.
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
		{
			name: "cannot_access_version",
			setupEnv: func(env *e2eTestEnv, ctx context.Context) {
				env.handler.HandleVersion(ctx, tgbotapi.Update{
					Message: &tgbotapi.Message{
						Chat:     &tgbotapi.Chat{ID: 999999},
						From:     &tgbotapi.User{ID: 999999, UserName: "notadmin"},
						Text:     "/v",
						Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 2}},
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

// TestE2E_BroadcastCrashMidChunksResumesAfterRestart моделирует падение воркера
// после доставки первого чанка многочастной рассылки (3 чанка по 4096 байт) и
// проверяет корректное возобновление после рестарта процесса: чанк 0 повторно
// не отправляется, доставка продолжается со второго чанка.
//
// Краш симулируется через реальный API БД — ровно то состояние, которое
// остаётся после смерти процесса между чанками: кампания Running, получатель
// в статусе Sending с сохранённым NextChunk=1 и протухшей арендой (прошло
// больше broadcastRecipientLease с момента падения). Рестарт идёт через
// прод-путь StartBroadcastWorker на той же БД.
func TestE2E_BroadcastCrashMidChunksResumesAfterRestart(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	userChatID := int64(500001)

	// Получатель рассылки — тот же путь, что и в остальных e2e-тестах рассылок.
	_, err := env.subService.Create(ctx, userChatID, "crashuser", "")
	require.NoError(t, err)

	// Три чанка ровно по config.MaxTelegramMessageLen байт: splitMessage режет
	// по границе maxLen, поэтому получаются ровно 3 различимых чанка.
	text := strings.Repeat("1", config.MaxTelegramMessageLen) +
		strings.Repeat("2", config.MaxTelegramMessageLen) +
		strings.Repeat("3", config.MaxTelegramMessageLen)

	b := &database.Broadcast{
		Name: "crash resume", MessageText: text, Filters: "{}",
		Status: string(database.BroadcastStatusScheduled),
	}
	require.NoError(t, env.db.CreateBroadcast(ctx, b))

	// Фаза 1 (до «краша»): кампания захвачена, аудитория снэпшотится — как при
	// обычном запуске. Время краша — 3 минуты назад, чтобы аренда получателя
	// уже протухла к моменту рестарта (broadcastRecipientLease = 2 мин).
	crashTime := time.Now().UTC().Add(-3 * time.Minute)
	claimed, err := env.db.ClaimBroadcast(ctx, b.ID, crashTime)
	require.NoError(t, err)
	require.True(t, claimed)
	total, err := env.db.SnapshotBroadcastRecipients(ctx, b.ID, database.BroadcastFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)

	// «Краш» после доставки чанка 0: получатель остаётся в Sending с
	// NextChunk=1 — ровно то, что БД хранит после падения процесса между
	// чанками. Финальный статус (Sent) не записан, аренда протухла.
	recipients, err := env.db.ClaimBroadcastRecipients(ctx, b.ID, crashTime, 10)
	require.NoError(t, err)
	require.Len(t, recipients, 1)
	require.NoError(t, env.db.UpdateBroadcastRecipientProgress(ctx, b.ID, recipients[0].ID, recipients[0].Attempts, 1, crashTime))

	// «Рестарт процесса»: новый handler, новый бот, та же БД. Worker стартует
	// через прод-путь StartBroadcastWorker.
	restartBot := testutil.NewBotAPI()
	restartHandler := bot.NewHandler(restartBot, env.cfg, env.db, env.botConfig, env.subService, "")
	runCtx, cancel := context.WithCancel(ctx)
	restartHandler.StartBroadcastWorker(runCtx)
	t.Cleanup(func() {
		cancel()
		restartHandler.WaitForBackgroundGoroutines()
	})

	require.Eventually(t, func() bool {
		cur, err := env.db.GetBroadcast(ctx, b.ID)
		return err == nil && cur.Status == string(database.BroadcastStatusCompleted)
	}, 5*time.Second, 10*time.Millisecond)

	// Чанк 0 не отправлен повторно: пользователю ушли ровно два сообщения —
	// второй и третий чанки.
	var userSends []string
	for _, msg := range restartBot.GetAllSentMessages() {
		if msg.ChatID == userChatID {
			userSends = append(userSends, msg.Text)
		}
	}
	require.Len(t, userSends, 2, "chunk 0 was already delivered — only chunks 2 and 3 must be sent")
	assert.Contains(t, userSends[0], strings.Repeat("2", 64))
	assert.Contains(t, userSends[1], strings.Repeat("3", 64))
	assert.NotContains(t, strings.Join(userSends, ""), strings.Repeat("1", 64), "already delivered chunk must not be re-sent")

	cur, err := env.db.GetBroadcast(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, string(database.BroadcastStatusCompleted), cur.Status)
	assert.Equal(t, int64(1), cur.RecipientsTotal)
	assert.Equal(t, int64(1), cur.SentCount)
	assert.Equal(t, int64(0), cur.FailedCount)
}

// TestE2E_BroadcastDraftDeliveredAfterCreate закрывает автоматическую часть
// критерия приёмки doc/specs/admin/06-broadcasts.md #2: черновик, сохранённый
// панелью в статусе scheduled, подхватывается прод-воркером
// (StartBroadcastWorker) и уходит получателю без ручных действий. Реальная
// доставка в Telegram остаётся ручной проверкой на тестовом боте.
//
// Заодно фиксирует два свойства, которые панель читает из строки рассылки:
// recipients_total публикуется снапшотом до завершения кампании, а событие
// broadcast_started в аудите несёт реальный размер аудитории, а не 0.
func TestE2E_BroadcastDraftDeliveredAfterCreate(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		err := env.db.Close()
		if err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	userChatID := int64(500101)
	_, err := env.subService.Create(ctx, userChatID, "broadcastuser", "")
	require.NoError(t, err)

	b := &database.Broadcast{
		Name:        "draft from panel",
		MessageText: "Привет из панели",
		Filters:     "{}",
		Status:      string(database.BroadcastStatusScheduled),
	}
	require.NoError(t, env.db.CreateBroadcast(ctx, b))

	runCtx, cancel := context.WithCancel(ctx)
	env.handler.StartBroadcastWorker(runCtx)
	t.Cleanup(func() {
		cancel()
		env.handler.WaitForBackgroundGoroutines()
	})

	require.Eventually(t, func() bool {
		cur, err := env.db.GetBroadcast(ctx, b.ID)
		return err == nil && cur.Status == string(database.BroadcastStatusCompleted)
	}, 5*time.Second, 10*time.Millisecond)

	var delivered []string
	for _, msg := range env.botAPI.GetAllSentMessages() {
		if msg.ChatID == userChatID {
			delivered = append(delivered, msg.Text)
		}
	}
	require.Len(t, delivered, 1, "draft must reach the recipient exactly once")
	assert.Equal(t, "Привет из панели", delivered[0])

	cur, err := env.db.GetBroadcast(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cur.RecipientsTotal)
	assert.Equal(t, int64(1), cur.SentCount)
	assert.Zero(t, cur.FailedCount)

	started, err := env.db.GetAuditLogByEvent(ctx, database.AuditEventBroadcastStarted, 10)
	require.NoError(t, err)
	require.Len(t, started, 1)
	assert.Contains(t, started[0].Details, `"recipients_total":1`,
		"broadcast_started must carry the audience size resolved by the snapshot")
}
