package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

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

	expectedText := fmt.Sprintf("⏳ До окончания подписки осталось 3 д\nhttps://vpn\\.example\\.com/sub/sub\\-remind\n\nЧтобы продлить подписку, откройте главное меню — нажмите /start\\.")
	assert.Equal(t, expectedText, sent[0].Text)

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

// TestSubscriptionService_RenewSubscription_ResetsRemindersSent verifies the
// reminder lifecycle invariant after a successful renewal.
func TestSubscriptionService_RenewSubscription_ResetsRemindersSent(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	ctx := context.Background()
	expiry := time.Now().UTC().Add(24 * time.Hour)
	sub := &database.Subscription{
		TelegramID:     123456,
		Username:       "renew_user",
		ClientID:       "client-renew",
		SubscriptionID: "sub-renew",
		Status:         "active",
		PlanID:         1,
		ExpiresAt:      &expiry,
		RemindersSent:  ReminderBit1Day,
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))

	product := &database.Product{PlanID: 1, Name: "renew-product", DurationDays: 30, PriceCents: 1000, Currency: "RUB"}
	svc := NewSubscriptionService(db, nil, nil, nil, &config.Config{})
	svc.SetInvalidateBySubIDFunc(func(string) {})

	order, err := svc.RenewSubscription(ctx, sub.TelegramID, product)
	require.NoError(t, err)
	require.NotNil(t, order)

	updated, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, updated.RemindersSent, "renewed subscription must reset reminders bitmask")
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
