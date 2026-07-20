

package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_Concurrent_CreateSubscription_SameUser(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 5)

	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := env.subService.Create(ctx, env.chatID, env.username, "")
			results <- err
		}()
	}

	wg.Wait()
	close(results)

	successCount := 0
	errorCount := 0
	for err := range results {
		if err == nil {
			successCount++
		} else {
			errorCount++
		}
	}

	assert.GreaterOrEqual(t, successCount, 1, "At least one should succeed")

	// After all concurrent creates, the user must have exactly one active
	// subscription (idempotency), regardless of how many calls raced.
	all, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	active := 0
	for _, s := range all {
		if s.TelegramID == env.chatID && s.Status == "active" {
			active++
		}
	}
	assert.Equal(t, 1, active, "exactly one active subscription should exist for the user after concurrent creates")
}

func TestE2E_Concurrent_CreateSubscription_DifferentUsers(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	var wg sync.WaitGroup
	results := make(chan struct {
		chatID int64
		err    error
	}, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			chatID := int64(500000 + idx)
			username := fmt.Sprintf("user_%d", idx)
			_, err := env.subService.Create(ctx, chatID, username, "")
			results <- struct {
				chatID int64
				err    error
			}{chatID, err}
		}(i)
	}

	wg.Wait()
	close(results)

	successCount := 0
	for r := range results {
		if r.err == nil {
			successCount++
		}
	}

	assert.Equal(t, 10, successCount, "All concurrent creations should succeed for different users")

	for i := 0; i < 10; i++ {
		chatID := int64(500000 + i)
		sub, err := env.db.GetByTelegramID(ctx, chatID)
		require.NoError(t, err, "User %d subscription should exist", chatID)
		assert.Equal(t, chatID, sub.TelegramID)
		assert.Equal(t, "active", sub.Status)
	}
}

func TestE2E_Concurrent_TrialBind_SameTrial(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	trialSubID := "concurrent_trial_bind"
	_, err := env.db.CreateTrialSubscription(ctx, "test_invite", trialSubID, "test-client-id", time.Now().Add(24*time.Hour))
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make(chan error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			chatID := int64(600000 + idx)
			username := fmt.Sprintf("user_%d", idx)
			_, err := env.db.BindTrialSubscription(ctx, trialSubID, chatID, username)
			results <- err
		}(i)
	}

	wg.Wait()
	close(results)

	successCount := 0
	for err := range results {
		if err == nil {
			successCount++
		}
	}

	assert.Equal(t, 1, successCount, "Only one bind should succeed due to atomic WHERE telegram_id < 0")

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	boundCount := 0
	for _, sub := range allSubs {
		if sub.SubscriptionID == trialSubID && sub.TelegramID > 0 {
			boundCount++
		}
	}
	assert.Equal(t, 1, boundCount, "Exactly one trial subscription should be bound")
}

func TestE2E_Concurrent_Handler_CreateSubscription(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env.handler.HandleCallback(ctx, tgbotapi.Update{
				CallbackQuery: &tgbotapi.CallbackQuery{
					Message: &tgbotapi.Message{
						Chat: &tgbotapi.Chat{ID: env.chatID},
						From: &tgbotapi.User{
							ID:       env.chatID,
							UserName: env.username,
						},
					},
					From: &tgbotapi.User{
						ID:       env.chatID,
						UserName: env.username,
					},
					Data: "create_subscription",
				},
			})
		}()
	}

	wg.Wait()

	assert.True(t, env.botAPI.SendCalledSafe(), "At least one message should be sent during concurrent callback handling")

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, env.chatID, sub.TelegramID)
}

func TestE2E_Concurrent_GetSubscription_SameUser(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make(chan *database.Subscription, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub, err := env.db.GetByTelegramID(ctx, env.chatID)
			if err == nil {
				results <- sub
			}
		}()
	}

	wg.Wait()
	close(results)

	count := 0
	for sub := range results {
		count++
		assert.Equal(t, env.chatID, sub.TelegramID)
	}
	assert.Equal(t, 10, count, "All concurrent reads should succeed")
}
