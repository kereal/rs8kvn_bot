package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_CreateSubscription_Success(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "Should not have subscription initially")

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	// DB-first: XUI is called via sync module, not directly in Create()
	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err, "Subscription should exist in DB")
	assert.Equal(t, env.chatID, sub.TelegramID)
	assert.Equal(t, env.username, sub.Username)
	assert.Equal(t, "active", sub.Status)
	assert.NotEmpty(t, sub.ClientID, "ClientID should be set")
	assert.NotEmpty(t, sub.SubscriptionID, "SubscriptionID should be set")

	assert.True(t, env.botAPI.SendCalledSafe(), "Confirmation message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")

	assert.GreaterOrEqual(t, env.botAPI.SendCount, 2, "Should send at least 2 messages: user confirmation + admin notification")
}

func TestE2E_CreateSubscription_NoDuplicate(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	resetBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 200,
			},
		},
	})

	assert.False(t, env.xui.AddClientWithIDCalled, "XUI should not be called for existing subscription")

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, len(allSubs), "Should have exactly one subscription")
}

func TestE2E_CreateSubscription_ConcurrentProtection(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)

	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			env.handler.HandleCallback(ctx, tgbotapi.Update{
				CallbackQuery: &tgbotapi.CallbackQuery{
					From: &tgbotapi.User{
						ID:       env.chatID,
						UserName: env.username,
					},
					Data: "create_subscription",
					Message: &tgbotapi.Message{
						Chat:      &tgbotapi.Chat{ID: env.chatID},
						MessageID: 100,
					},
				},
			})
		}()
	}

	wg.Wait()

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, len(allSubs), "Should have exactly one subscription despite concurrent calls")
}

func TestE2E_CreateSubscription_XUIFailure(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.xui.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
		return nil, fmt.Errorf("connection refused")
	}

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	// DB-first: subscription is created even if XUI fails (sync will retry)
	assert.True(t, env.botAPI.SendCalledSafe(), "Confirmation message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err, "Subscription should exist in DB even with XUI failure")
	assert.Equal(t, "active", sub.Status)
}

func TestE2E_CreateSubscription_TrafficLimitCorrect(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	// DB-first: verify subscription has correct plan, traffic is set via sync
	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err, "Subscription should exist in DB")
	freePlan, err := env.db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)
	assert.Equal(t, freePlan.ID, sub.PlanID, "Subscription should have free plan")
}

func TestE2E_CreateSubscription_SubscriptionID_Set(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)

	assert.NotEmpty(t, sub.SubscriptionID, "SubscriptionID should be set")
}

func TestE2E_CreateSubscription_UsernameStored(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, env.username, sub.Username, "Username should be stored correctly")
}

func TestE2E_MultipleUsers_Isolation(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	users := []struct {
		chatID   int64
		username string
	}{
		{111, "user1"},
		{222, "user2"},
		{333, "user3"},
	}

	for _, u := range users {
		env.handler.HandleCallback(ctx, tgbotapi.Update{
			CallbackQuery: &tgbotapi.CallbackQuery{
				From: &tgbotapi.User{
					ID:       u.chatID,
					UserName: u.username,
				},
				Data: "create_subscription",
				Message: &tgbotapi.Message{
					Chat:      &tgbotapi.Chat{ID: u.chatID},
					MessageID: 100,
				},
			},
		})
	}

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, len(allSubs), "Should have 3 subscriptions")

	for _, u := range users {
		sub, err := env.db.GetByTelegramID(ctx, u.chatID)
		require.NoError(t, err)
		assert.Equal(t, u.username, sub.Username, "Username should match for user %d", u.chatID)
	}
}

func TestE2E_Subscription_ReplacesOldActive(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	oldSub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "old-client-id",
		SubscriptionID: "old-sub-id",
		Status:         "active",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, oldSub, ""))

	// Creating another subscription with the same telegram_id should fail
	// due to UNIQUE constraint
	result, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, oldSub.SubscriptionID, result.Subscription.SubscriptionID)
	assert.Equal(t, "active", result.Subscription.Status)
}

func TestE2E_CreateSubscription_RevokesOnlyActive(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	oldSub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "old-client-id",
		SubscriptionID: "old-sub-id",
		Status:         "expired",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, oldSub, ""))

	resetBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false

	// Creating another subscription with the same telegram_id should fail
	// due to UNIQUE constraint
	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)

	// Only the original expired subscription should exist
	assert.Equal(t, 1, len(allSubs), "Should have only one subscription")
	assert.Equal(t, "expired", allSubs[0].Status)
}

func TestE2E_Service_Create_XUIFailure_Parameterized(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)
	defer env.db.Close()
	ctx := context.Background()

	tests := []struct {
		name        string
		setupXUI    func()
		wantActive  bool
		checkSecond func(*testing.T)
	}{
		{
			name: "xui_failure_sub_still_created",
			setupXUI: func() {
				env.xui.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
					return nil, fmt.Errorf("connection refused")
				}
			},
			wantActive:  true,
			checkSecond: nil,
		},
		{
			name: "rollback_xui_delete_succeeds",
			setupXUI: func() {
				env.xui.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
					return &xui.ClientConfig{ID: req.ClientID, Email: req.Email, SubID: req.SubID}, nil
				}
				env.xui.DeleteClientFunc = func(ctx context.Context, email string) error { return nil }
			},
			wantActive: true,
			checkSecond: func(t *testing.T) {
				result, err := env.subService.Create(ctx, env.chatID, env.username, "")
				require.NoError(t, err)
				assert.Equal(t, "active", result.Subscription.Status)
			},
		},
		{
			name: "rollback_failure_returns_existing",
			setupXUI: func() {
				env.xui.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
					return &xui.ClientConfig{ID: req.ClientID, Email: req.Email, SubID: req.SubID}, nil
				}
				env.xui.DeleteClientFunc = func(ctx context.Context, email string) error {
					return fmt.Errorf("rollback failed: connection refused")
				}
			},
			wantActive: true,
			checkSecond: func(t *testing.T) {
				result, err := env.subService.Create(ctx, env.chatID, env.username, "")
				require.NoError(t, err)
				assert.Equal(t, env.chatID, result.Subscription.TelegramID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupE2EEnv(t)
			defer env.db.Close()

			ctx := context.Background()
			tt.setupXUI()

			sub, err := env.subService.Create(ctx, env.chatID, env.username, "")
			if tt.wantActive {
				require.NoError(t, err)
				assert.Equal(t, "active", sub.Subscription.Status)
			}

			if tt.checkSecond != nil {
				tt.checkSecond(t)
			}
		})
	}
}
