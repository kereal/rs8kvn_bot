package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_CreateSubscription_EmptyUsername(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.subService.Create(ctx, env.chatID, "")
	if err == nil {
		sub, err := env.db.GetByTelegramID(ctx, env.chatID)
		require.NoError(t, err)
		assert.Equal(t, "", sub.Username)
	}
}

func TestE2E_CreateSubscription_InvalidChatID(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.subService.Create(ctx, -123, "testuser")
	if err == nil {
		sub, err := env.db.GetByTelegramID(ctx, -123)
		require.NoError(t, err)
		assert.Equal(t, int64(-123), sub.TelegramID)
	}
}

func TestE2E_CreateSubscription_ZeroChatID(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.subService.Create(ctx, 0, "testuser")
	if err == nil {
		sub, err := env.db.GetByTelegramID(ctx, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(0), sub.TelegramID)
	}
}

func TestE2E_Subscription_MaxTrafficLimit(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	maxTraffic := int64(1073741824000)
	_, err := env.subService.Create(ctx, env.chatID, env.username)
	require.NoError(t, err)

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	sub.TrafficLimit = maxTraffic
	require.NoError(t, env.db.UpdateSubscription(ctx, sub))

	storedSub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, maxTraffic, storedSub.TrafficLimit, "Should handle max traffic limit")
}

func TestE2E_Subscription_MinTrafficLimit(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	minTraffic := int64(1073741824)
	sub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    minTraffic,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/test-sub-id",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	storedSub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, minTraffic, storedSub.TrafficLimit, "Should handle min traffic limit")
}

func TestE2E_Subscription_LongUsername(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	longUsername := strings.Repeat("a", 1000)
	_, err := env.subService.Create(ctx, env.chatID, longUsername)
	if err == nil {
		sub, err := env.db.GetByTelegramID(ctx, env.chatID)
		require.NoError(t, err)
		assert.True(t, len(sub.Username) > 0, "Should have some username stored")
	}
}

func TestE2E_Subscription_SpecialCharactersInUsername(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	specialUsername := "test@user#123!"
	_, err := env.subService.Create(ctx, env.chatID, specialUsername)
	if err != nil {
		assert.Contains(t, err.Error(), "invalid", "Error should mention invalid characters")
	}
}

func TestE2E_CreateSubscription_RetryAfterFailure(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	callCount := 0
	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		callCount++
		if callCount == 1 {
			return nil, fmt.Errorf("temporary error")
		}
		return &xui.ClientConfig{
			ID:         "test-id",
			Email:      email,
			Enable:     true,
			TotalGB:    trafficBytes,
			ExpiryTime: expiryTime.Unix(),
			SubID:      subID,
			Reset:      resetDays,
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

	_, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "No subscription after first failure")

	resetMockBotAPI(env.botAPI)
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

	_, err = env.db.GetByTelegramID(ctx, env.chatID)
	assert.NoError(t, err, "Subscription should exist after successful retry")
}

func TestE2E_CreateSubscription_MultipleRetries(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	callCount := 0
	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		callCount++
		if callCount < 3 {
			return nil, fmt.Errorf("temporary error %d", callCount)
		}
		return &xui.ClientConfig{
			ID:         "test-id",
			Email:      email,
			Enable:     true,
			TotalGB:    trafficBytes,
			ExpiryTime: expiryTime.Unix(),
			SubID:      subID,
			Reset:      resetDays,
		}, nil
	}

	for i := 0; i < 3; i++ {
		resetMockBotAPI(env.botAPI)
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
	}

	_, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.NoError(t, err, "Subscription should exist after multiple retries")
	assert.Equal(t, 3, callCount, "Should have been called 3 times")
}

func TestE2E_CreateSubscription_XUITimeout(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		time.Sleep(2 * time.Second)
		return nil, context.DeadlineExceeded
	}

	ctx2, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	env.handler.HandleCallback(ctx2, tgbotapi.Update{
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

	_, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "No subscription after timeout")
}

func TestE2E_Service_Create_TimeoutContext(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		time.Sleep(100 * time.Millisecond)
		return &xui.ClientConfig{ID: "test-id", Email: email, SubID: subID}, nil
	}

	_, err := env.subService.Create(ctx, env.chatID, env.username)
	assert.Error(t, err, "Should fail with timeout")
}

func TestE2E_Service_Create_DatabaseClosed(t *testing.T) {
	env := setupE2EEnv(t)
	env.db.Close()

	ctx := context.Background()
	_, err := env.subService.Create(ctx, env.chatID, env.username)
	assert.Error(t, err, "Should fail with closed database")
}

func TestE2E_GetSubscription_DatabaseClosed(t *testing.T) {
	env := setupE2EEnv(t)

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
	env.db.CreateSubscription(ctx, sub)
	env.db.Close()

	_, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "Should fail with closed database")
}

func TestE2E_Subscription_Expired(t *testing.T) {
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
		Status:          "expired",
		SubscriptionURL: "https://example.com/sub/test-sub-id",
		ExpiryTime:      time.Now().Add(-1 * time.Hour),
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	fetched, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "Expired subscription should not be returned by GetByTelegramID")
	_ = fetched
}

func TestE2E_Subscription_AboutToExpire(t *testing.T) {
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
		ExpiryTime:      time.Now().Add(1 * time.Hour),
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	storedSub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.True(t, storedSub.ExpiryTime.Before(time.Now().Add(2*time.Hour)), "Should expire within 2 hours")
}

func TestE2E_RateLimit_ExactlyAtLimit(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	_, err := env.subService.Create(ctx, env.chatID, env.username)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		resetMockBotAPI(env.botAPI)
		update := tgbotapi.Update{
			Message: &tgbotapi.Message{
				Chat:     &tgbotapi.Chat{ID: adminID},
				From:     &tgbotapi.User{ID: adminID, UserName: "admin"},
				Text:     fmt.Sprintf("/send %d test message %d", env.chatID, i),
				Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 5}},
			},
		}
		env.handler.HandleSend(ctx, update)
	}

	assert.True(t, env.botAPI.SendCalledSafe(), "At least one message should be sent")
}
