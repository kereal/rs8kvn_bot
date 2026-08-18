package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubscriptionService_SendExpiryReminder_SendsMessageAndMarksBit verifies the
// reminder contract: with a real bot attached, the message is sent and the DB
// update records the sent bit atomically.
func TestSubscriptionService_SendExpiryReminder_SendsMessageAndMarksBit(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	expiry := time.Now().UTC().Add(72*time.Hour + 10*time.Minute)
	sub := &database.Subscription{
		TelegramID:     123456,
		Username:       "remind_user",
		ClientID:       "client-remind",
		SubscriptionID: "sub-remind",
		Status:         "active",
		PlanID:         1,
		ExpiresAt:      &expiry,
		RemindersSent:  ReminderBit1Day,
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))

	cfg := &config.Config{GlobalSubURL: "https://vpn.example.com/sub/"}
	svc := NewSubscriptionService(db, nil, nil, nil, cfg)
	bot := testutil.NewBotAPI()
	svc.SetBot(bot)

	err = svc.SendExpiryReminder(ctx, sub, ExpiryReminderWindows()[0])
	require.NoError(t, err)

	updated, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, ReminderBit1Day|ReminderBit3Days, updated.RemindersSent)

	sent := bot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Equal(t, sub.TelegramID, sent[0].ChatID)

	assert.Contains(t, sent[0].Text, "Premium заканчивается через 3 дн")
	assert.Contains(t, sent[0].Text, "Безлимитный трафик")
	assert.Contains(t, sent[0].Text, "Больше серверов и вариантов подключения")
	assert.Contains(t, sent[0].Text, "Продлите Premium заранее")

	message, ok := bot.LastChattableSafe().(tgbotapi.MessageConfig)
	require.True(t, ok)
	require.NotNil(t, message.ReplyMarkup)
	keyboard, ok := message.ReplyMarkup.(*tgbotapi.InlineKeyboardMarkup)
	require.True(t, ok)
	require.Len(t, keyboard.InlineKeyboard, 1)
	require.Len(t, keyboard.InlineKeyboard[0], 1)
	assert.Equal(t, "💎 Продлить Premium", keyboard.InlineKeyboard[0][0].Text)
	require.NotNil(t, keyboard.InlineKeyboard[0][0].CallbackData)
	assert.Equal(t, "buy_premium_list", *keyboard.InlineKeyboard[0][0].CallbackData)

	// A second call for the same expiry and bit must be a no-op after the atomic claim.
	err = svc.SendExpiryReminder(ctx, sub, ExpiryReminderWindows()[0])
	require.NoError(t, err)
	assert.Len(t, bot.GetAllSentMessages(), 1)
}

// TestSubscriptionService_SendExpiryReminder_NilBotNoop verifies the early-return
// contract when the service has no bot configured.
func TestSubscriptionService_SendExpiryReminder_NilBotNoop(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()

	expiry := time.Now().UTC().Add(24 * time.Hour)
	sub := &database.Subscription{
		TelegramID:     123456,
		Username:       "remind_nil",
		ClientID:       "client-remind-nil",
		SubscriptionID: "sub-remind-nil",
		Status:         "active",
		PlanID:         1,
		ExpiresAt:      &expiry,
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))

	cfg := &config.Config{GlobalSubURL: "https://vpn.example.com/sub/"}
	svc := NewSubscriptionService(db, nil, nil, nil, cfg)
	// svc.SetBot is intentionally not called.

	err = svc.SendExpiryReminder(ctx, sub, ExpiryReminderWindows()[1])
	require.NoError(t, err)

	updated, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, updated.RemindersSent, "DB must not be touched when bot is nil")
}

// TestSubscriptionService_SendExpiryReminder_ClaimErrorPropagates verifies that
// a claim failure is returned before any Telegram message is sent.
func TestSubscriptionService_SendExpiryReminder_ClaimErrorPropagates(t *testing.T) {
	t.Parallel()

	db := &testutil.DatabaseService{}
	ctx := context.Background()
	expiry := time.Now().UTC().Add(3 * time.Hour)
	sub := &database.Subscription{
		ID:             777,
		TelegramID:     123456,
		Username:       "remind_db_err",
		ClientID:       "client-remind-db-err",
		SubscriptionID: "sub-remind-db-err",
		Status:         "active",
		PlanID:         1,
		ExpiresAt:      &expiry,
	}
	claimErr := errors.New("db write failed")
	db.ClaimReminderFunc = func(ctx context.Context, id uint, bit int, expiresAt time.Time) (bool, error) {
		return false, claimErr
	}

	svc := NewSubscriptionService(db, nil, nil, nil, &config.Config{GlobalSubURL: "https://vpn.example.com/sub/"})
	bot := testutil.NewBotAPI()
	svc.SetBot(bot)

	err := svc.SendExpiryReminder(ctx, sub, ExpiryReminderWindows()[2])
	require.ErrorIs(t, err, claimErr)
	assert.Empty(t, bot.GetAllSentMessages())
}

// TestSubscriptionService_SendExpiryReminder_HoursOnlyText verifies the hours-only
// reminder text when daysLeft == 0.
func TestSubscriptionService_SendExpiryReminder_HoursOnlyText(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	ctx := context.Background()
	expiry := time.Now().UTC().Add(3 * time.Hour)
	sub := &database.Subscription{TelegramID: 888, Username: "hour_user", ClientID: "client-hour", SubscriptionID: "sub-hour", Status: "active", PlanID: 1, ExpiresAt: &expiry}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))

	svc := NewSubscriptionService(db, nil, nil, nil, &config.Config{GlobalSubURL: "https://vpn.example.com/sub/"})
	bot := testutil.NewBotAPI()
	svc.SetBot(bot)

	err = svc.SendExpiryReminder(ctx, sub, ExpiryReminderWindows()[2])
	require.NoError(t, err)

	sent := bot.GetAllSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, " ч")
}
