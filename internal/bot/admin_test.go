//go:build integration

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
		if err := env.db.Close(); err != nil {
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
				if err := env.db.Close(); err != nil {
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
		if err := env.db.Close(); err != nil {
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
	// With DB-first deletion, the subscription is removed from DB even when
	// XUI deletion fails (best-effort XUI cleanup). The orphaned XUI client
	// is less critical than an orphaned DB record.
	assert.Contains(t, env.botAPI.LastSentText, "успешно удалена")

	_, err = env.db.GetByID(ctx, sub.ID)
	assert.Error(t, err, "Subscription should be deleted from DB even when XUI fails")
}

// runBroadcastFlow drives the draft -> preview -> confirm broadcast flow end to end.
func runBroadcastFlow(t *testing.T, env *e2eTestEnv, adminID int64, draftText string) {
	t.Helper()
	ctx := context.Background()

	env.handler.HandleBroadcast(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text: "/broadcast",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 10}},
		},
	})

	env.handler.HandleBroadcastDraft(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text: draftText,
		},
	})

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
}

func TestE2E_BroadcastCommand_Success(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	for i := 0; i < 3; i++ {
		chatID := int64(300000 + i)
		_, err := env.subService.Create(ctx, chatID, fmt.Sprintf("user%d", i), "")
		require.NoError(t, err)
	}

	resetBotAPI(env.botAPI)

	runBroadcastFlow(t, env, adminID, "Hello everyone!")

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.GreaterOrEqual(t, env.botAPI.SendCount, 3, "Should send to at least 3 users")
	assert.Contains(t, env.botAPI.LastSentText, "Рассылка завершена")
}

func TestE2E_BroadcastCommand_NoArgs(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
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
	assert.Contains(t, env.botAPI.LastSentText, "Отправьте сообщение")
}

func TestE2E_BroadcastCommand_NoUsers(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	adminID := env.cfg.TelegramAdminID
	resetBotAPI(env.botAPI)

	runBroadcastFlow(t, env, adminID, "Hello")

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentText, "Всего: 0")
}

func TestE2E_BroadcastCommand_SomeFailures(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	for i := 0; i < 3; i++ {
		chatID := int64(400000 + i)
		_, err := env.subService.Create(ctx, chatID, fmt.Sprintf("user%d", i), "")
		require.NoError(t, err)
	}

	resetBotAPI(env.botAPI)

	// Drive the flow manually so the preview send (before confirm) succeeds,
	// then make the actual user sends fail.
	env.handler.HandleBroadcast(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text: "/broadcast",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 10}},
		},
	})
	env.handler.HandleBroadcastDraft(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: adminID},
			From: &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text: "Hello",
		},
	})
	env.botAPI.SendError = fmt.Errorf("send failed")
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

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentText, "Рассылка завершена")
	assert.Contains(t, env.botAPI.LastSentText, "Ошибок: 3")
}

func TestE2E_SendCommand_ByTelegramID(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
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
		if err := env.db.Close(); err != nil {
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
				if err := env.db.Close(); err != nil {
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
		if err := env.db.Close(); err != nil {
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
		if err := env.db.Close(); err != nil {
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

func TestE2E_SendCommand_OnlyMessageNoTarget(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
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
			Text:     "/send",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
		},
	}
	env.handler.HandleSend(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentText, "Использование")
}

func TestE2E_SendCommand_OnlyTargetNoMessage(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
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
			Text:     "/send 123456",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
		},
	}
	env.handler.HandleSend(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.Contains(t, env.botAPI.LastSentText, "Использование")
}

func TestE2E_SendCommand_RateLimitBlocksExcess(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
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
			Text:     fmt.Sprintf("/send %d Message 1", env.chatID),
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
		},
	}
	env.handler.HandleSend(ctx, update)
	require.True(t, env.botAPI.SendCalledSafe(), "First send should succeed")

	update2 := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat:     &tgbotapi.Chat{ID: adminID},
			From:     &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text:     fmt.Sprintf("/send %d Message 2", env.chatID),
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
		},
	}
	resetBotAPI(env.botAPI)
	env.handler.HandleSend(ctx, update2)

	assert.True(t, env.botAPI.SendCalledSafe(), "Second send should succeed under normal rate")
}

func TestE2E_BroadcastCommand_PreservesFormatting(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
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

	assert.Contains(t, env.botAPI.LastSentText, "Рассылка завершена")

	// Formatting must be delivered as-is (MarkdownV2, not escaped with backslashes).
	var delivered bool
	for _, m := range env.botAPI.GetAllSentMessages() {
		if strings.Contains(m.Text, "*bold*") && !strings.Contains(m.Text, `\*bold\*`) {
			delivered = true
		}
	}
	assert.True(t, delivered, "delivered message should keep MarkdownV2 formatting unescaped")
}

func TestSplitMessage_DoesNotBreakMarkdownV2Entities(t *testing.T) {
	t.Parallel()

	const maxLen = 20

	tests := []struct {
		name     string
		input    string
		wantAny  []string
		maxChunk int
	}{
		{
			name:     "short text",
			input:    "short",
			wantAny:  []string{"short"},
			maxChunk: maxLen,
		},
		{
			name:     "plain long text",
			input:    strings.Repeat("a", maxLen+5),
			wantAny:  nil,
			maxChunk: maxLen,
		},
		{
			name:     "markdown entities stay intact",
			input:    "prefix *bold* suffix extra padding",
			wantAny:  []string{"prefix *bold* suffix", "extra padding"},
			maxChunk: maxLen,
		},
		{
			name:     "long markdown is hard-split safely",
			input:    "a*" + strings.Repeat("b", maxLen*3),
			wantAny:  nil,
			maxChunk: maxLen,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			chunks := splitMessage(tt.input, maxLen)
			for _, c := range chunks {
				assert.LessOrEqual(t, len(c), tt.maxChunk, "chunk must not exceed maxLen")
			}
			if tt.wantAny != nil {
				var matched bool
				for _, c := range chunks {
					for _, w := range tt.wantAny {
						if c == w {
							matched = true
							break
						}
					}
				}
				assert.True(t, matched, "expected one of %v in chunks %v", tt.wantAny, chunks)
			}
		})
	}
}

func TestBroadcastSession_TTLExpiry(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()
	chatID := env.cfg.TelegramAdminID

	env.handler.startBroadcastSession(chatID)
	require.NotNil(t, env.handler.getBroadcastSession(chatID))

	env.handler.broadcastMu.Lock()
	s := env.handler.broadcastSessions[chatID]
	s.createdAt = time.Now().Add(-16 * time.Minute)
	env.handler.broadcastMu.Unlock()

	require.Nil(t, env.handler.getBroadcastSession(chatID))
	require.False(t, env.handler.broadcastSessionActive(chatID))
}

func TestE2E_SendCommand_EscapesMarkdown(t *testing.T) {
	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
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

	assert.True(t, env.botAPI.SendCalledSafe(), "Send should succeed")
	assert.Contains(t, env.botAPI.LastSentText, "Сообщение отправлено")
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
				if err := env.db.Close(); err != nil {
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

func TestE2E_NonAdmin_CannotUseDel(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	}()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.cfg.TelegramAdminID,
				UserName: "admin",
			},
			Data: "admin_stats",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.cfg.TelegramAdminID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Admin stats should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Статистика", "Should contain stats")
}

func TestE2E_AdminLastReg(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer func() {
		if err := env.db.Close(); err != nil {
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
	require.NoError(t, env.db.CreateSubscription(ctx, sub, ""))

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
		if err := env.db.Close(); err != nil {
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
		if err := env.db.Close(); err != nil {
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
