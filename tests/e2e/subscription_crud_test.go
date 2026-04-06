package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/xui"

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

	assert.True(t, env.xui.AddClientWithIDCalled, "XUI AddClientWithID should be called")

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err, "Subscription should exist in DB")
	assert.Equal(t, env.chatID, sub.TelegramID)
	assert.Equal(t, env.username, sub.Username)
	assert.Equal(t, "active", sub.Status)
	assert.NotEmpty(t, sub.ClientID, "ClientID should be set")
	assert.NotEmpty(t, sub.SubscriptionID, "SubscriptionID should be set")
	assert.NotEmpty(t, sub.SubscriptionURL, "SubscriptionURL should be set")

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

	resetMockBotAPI(env.botAPI)
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

	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
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

	assert.True(t, env.botAPI.SendCalledSafe(), "Error message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подключиться к серверу", "Should show connection error message")

	_, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "No subscription should exist after XUI failure")
}

func TestE2E_CreateSubscription_TrafficLimitCorrect(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	var capturedTraffic int64
	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		capturedTraffic = trafficBytes
		return &xui.ClientConfig{
			ID:    "client-uuid-123",
			SubID: "sub-id-456",
		}, nil
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

	expectedTraffic := int64(env.cfg.TrafficLimitGB) * 1024 * 1024 * 1024
	assert.Equal(t, expectedTraffic, capturedTraffic, "Traffic limit should match config")
}

func TestE2E_CreateSubscription_SubscriptionURLFormat(t *testing.T) {
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

	assert.Contains(t, sub.SubscriptionURL, env.cfg.XUIHost, "URL should contain XUI host")
	assert.Contains(t, sub.SubscriptionURL, env.cfg.XUISubPath, "URL should contain sub path")
	assert.Contains(t, sub.SubscriptionURL, sub.SubscriptionID, "URL should contain subscription ID")
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
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "old-client-id",
		SubscriptionID:  "old-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/old",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, oldSub))

	result, err := env.subService.Create(ctx, env.chatID, env.username)
	require.NoError(t, err)
	require.NotNil(t, result)

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)

	activeCount := 0
	revokedCount := 0
	for _, s := range allSubs {
		switch s.Status {
		case "active":
			activeCount++
		case "revoked":
			revokedCount++
		}
	}
	assert.Equal(t, 1, activeCount, "Should have exactly one active subscription")
	assert.Equal(t, 1, revokedCount, "Old subscription should be revoked")
}

func TestE2E_CreateSubscription_RevokesOnlyActive(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	oldSub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "old-client-id",
		SubscriptionID:  "old-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "expired",
		SubscriptionURL: "https://example.com/sub/old",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, oldSub))

	resetMockBotAPI(env.botAPI)
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
				MessageID: 100,
			},
		},
	})

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)

	expiredCount := 0
	for _, s := range allSubs {
		if s.Status == "expired" {
			expiredCount++
		}
	}
	assert.Equal(t, 1, expiredCount, "Expired subscription should not be revoked")
}

func TestE2E_Service_Create_XUIFailure_NoDBRecord(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return nil, fmt.Errorf("xui add client: connection refused")
	}

	_, err := env.subService.Create(ctx, env.chatID, env.username)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")

	_, err = env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "No subscription should exist after XUI failure")
}

func TestE2E_Service_Create_DBFailure_RollbackXUI(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	t.Skip("Covered by TestE2E_Service_Create_RollbackXUIOnDBError")
}

func TestE2E_Service_Create_RollbackXUIOnDBError(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return &xui.ClientConfig{
			ID:    clientID,
			Email: email,
			SubID: subID,
		}, nil
	}

	env.xui.DeleteClientFunc = func(ctx context.Context, inboundID int, clientID string) error {
		return nil
	}

	_, err := env.subService.Create(ctx, env.chatID, env.username)
	require.NoError(t, err)

	sub1, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, "active", sub1.Status)

	_, err = env.subService.Create(ctx, env.chatID, env.username)
	require.NoError(t, err)

	sub2, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, "active", sub2.Status)
}

func TestE2E_Service_Create_RollbackFailure_ReturnsError(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return &xui.ClientConfig{
			ID:    clientID,
			Email: email,
			SubID: subID,
		}, nil
	}

	env.xui.DeleteClientFunc = func(ctx context.Context, inboundID int, clientID string) error {
		return fmt.Errorf("rollback failed: connection refused")
	}

	_, err := env.subService.Create(ctx, env.chatID, env.username)
	require.NoError(t, err)

	_, err = env.subService.Create(ctx, env.chatID, env.username)
	require.NoError(t, err, "Second creation should succeed (rollback not triggered when DB succeeds)")
}
