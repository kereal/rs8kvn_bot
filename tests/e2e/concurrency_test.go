package e2e

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestE2E_Concurrent_CreateSubscription_SameUser(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	var wg sync.WaitGroup

	results := make(chan error, 5)

	for range 5 {
		wg.Go(func() {
			_, err := env.subService.Create(ctx, env.chatID, env.username, "")
			results <- err
		})
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
		if s.TelegramID == env.chatID && s.Status == string(database.SubscriptionStatusActive) {
			active++
		}
	}

	assert.Equal(t, 1, active, "exactly one active subscription should exist for the user after concurrent creates")

	// Гонка через handler (callback create_subscription) на пользователе БЕЗ
	// подписки — иначе победитель идёт в ветку «подписка уже есть» и реальный
	// createSubscription не вызывается никогда. Счётчик врезается в SQL-вставку
	// subscriptions: он срабатывает до того, как subService.Create вернёт
	// опоздавшим идемпотентный успех, и считает настоящие вызовы создания.
	// Идемпотентные выходы вставку не трогают, поэтому ровно один прогон ==
	// ровно одно создание через handleCreateSubscription.
	const handlerChatID = int64(990001)
	var creates atomic.Int32
	sqlDB := env.db.GetDB()
	require.NoError(t, sqlDB.Callback().Create().Before("gorm:create").Register("e2e_count_subscription_creates", func(tx *gorm.DB) {
		if created, ok := tx.Statement.Dest.(*database.Subscription); ok && created.TelegramID == handlerChatID {
			creates.Add(1)
		}
	}))
	defer func() {
		_ = sqlDB.Callback().Create().Remove("e2e_count_subscription_creates")
	}()

	var handlerWG sync.WaitGroup
	for range 5 {
		handlerWG.Go(func() {
			env.handler.HandleCallback(ctx, tgbotapi.Update{
				CallbackQuery: &tgbotapi.CallbackQuery{
					Message: &tgbotapi.Message{
						Chat: &tgbotapi.Chat{ID: handlerChatID},
						From: &tgbotapi.User{
							ID:       handlerChatID,
							UserName: env.username,
						},
					},
					From: &tgbotapi.User{
						ID:       handlerChatID,
						UserName: env.username,
					},
					Data: "create_subscription",
				},
			})
		})
	}
	handlerWG.Wait()

	assert.Equal(t, int32(1), creates.Load(),
		"пять параллельных callback одному пользователю без подписки должны дать ровно один реальный вызов создания")
	assert.True(t, env.botAPI.SendCalledSafe(), "At least one message should be sent during concurrent callback handling")

	sub, err := env.db.GetByTelegramID(ctx, handlerChatID)
	require.NoError(t, err)
	assert.Equal(t, handlerChatID, sub.TelegramID)
	assert.NotEmpty(t, sub.SubscriptionID, "the winning creation must leave a readable subscription")
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

	for i := range 10 {
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

	for i := range 10 {
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

	for i := range 5 {
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
