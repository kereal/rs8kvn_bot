package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_TrialBind_Parameterized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setup       func(t *testing.T, ctx context.Context, env *e2eTestEnv) string
		start       string
		wantSent    bool
		wantMessage string
		checkSub    func(t *testing.T, ctx context.Context, env *e2eTestEnv)
	}{
		{
			name: "success",
			setup: func(t *testing.T, ctx context.Context, env *e2eTestEnv) string {
				trialSubID := "trial-abc-123"
				_, err := env.db.CreateTrialSubscription(ctx, "test_invite_code", trialSubID, "trial-client-id", time.Now().Add(24*time.Hour))
				require.NoError(t, err)
				return trialSubID
			},
			wantSent:    true,
			wantMessage: "Подписка активирована",
			checkSub: func(t *testing.T, ctx context.Context, env *e2eTestEnv) {
				bound, err := env.db.GetByTelegramID(ctx, env.chatID)
				require.NoError(t, err)
				assert.Equal(t, env.chatID, bound.TelegramID)
				assert.Equal(t, env.username, bound.Username)
				assert.Equal(t, uint(2), bound.PlanID)
			},
		},
		{
			name: "already_has_subscription",
			setup: func(t *testing.T, ctx context.Context, env *e2eTestEnv) string {
				existingSub := &database.Subscription{
					TelegramID:     env.chatID,
					Username:       env.username,
					ClientID:       "existing-client",
					SubscriptionID: "existing-sub",
					Status:         "active",
				}
				require.NoError(t, env.db.CreateSubscription(ctx, existingSub, ""))
				trialSubID := "trial-xyz-789"
				_, err := env.db.CreateTrialSubscription(ctx, "test_invite_code", trialSubID, "trial-client-id", time.Now().Add(24*time.Hour))
				require.NoError(t, err)
				return trialSubID
			},
			wantSent:    true,
			wantMessage: "уже есть активная подписка",
			checkSub:    nil,
		},
		{
			name: "not_found",
			setup: func(t *testing.T, ctx context.Context, env *e2eTestEnv) string {
				return "trial_nonexistent"
			},
			wantSent:    true,
			wantMessage: "Не удалось активировать",
			checkSub:    nil,
		},
		{
			name: "already_activated",
			setup: func(t *testing.T, ctx context.Context, env *e2eTestEnv) string {
				trialSubID := "trial-double-123"
				_, err := env.db.CreateTrialSubscription(ctx, "test_invite_code", trialSubID, "trial-client-id", time.Now().Add(24*time.Hour))
				require.NoError(t, err)
				env.handler.HandleStart(ctx, tgbotapi.Update{
					Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
				})
				resetBotAPI(env.botAPI)
				return trialSubID
			},
			wantSent:    true,
			wantMessage: "уже есть активная подписка",
			checkSub:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupE2EEnv(t)
			defer env.db.Close()

			ctx := context.Background()
			resetBotAPI(env.botAPI)
			trialSubID := tt.setup(t, ctx, env)

			env.handler.HandleStart(ctx, tgbotapi.Update{
				Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
			})

			assert.Equal(t, tt.wantSent, env.botAPI.SendCalledSafe())
			if tt.wantSent {
				assert.Contains(t, env.botAPI.LastSentText, tt.wantMessage)
			}
			if tt.checkSub != nil {
				tt.checkSub(t, ctx, env)
			}
		})
	}
}
