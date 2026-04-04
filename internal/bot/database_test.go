package bot

import (
	"context"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleStart_WithDatabase(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, db.CreateSubscription(ctx, sub), "Failed to create subscription")

	got, err := db.GetByTelegramID(ctx, 123456789)
	require.NoError(t, err, "GetByTelegramID() error")

	assert.Equal(t, int64(123456789), got.TelegramID)
	assert.Equal(t, "active", got.Status)
}

func TestHandleStart_NoDatabase(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	_, err = db.GetByTelegramID(ctx, 999999999)
	assert.Error(t, err, "Expected error for non-existent user")
}

func TestHandleMySubscription_NoSubscription(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	_, err = db.GetByTelegramID(ctx, 999999999)
	assert.Error(t, err, "Expected error for non-existent user")
}

func TestHandleMySubscription_WithSubscription(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, db.CreateSubscription(ctx, sub), "Failed to create subscription")

	got, err := db.GetByTelegramID(ctx, 123456789)
	require.NoError(t, err, "GetByTelegramID() error")

	assert.Equal(t, sub.TelegramID, got.TelegramID)
	assert.Equal(t, "active", got.Status)
}

func TestHandleMySubscription_ExpiredSubscription(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(-24*time.Hour))
	require.NoError(t, db.CreateSubscription(ctx, sub), "Failed to create subscription")

	got, err := db.GetByTelegramID(ctx, 123456789)
	require.NoError(t, err, "GetByTelegramID() error")

	assert.True(t, time.Now().After(got.ExpiryTime), "Expected subscription to be expired")
}

func TestHandleAdminStats(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	for i := 0; i < 5; i++ {
		sub := testutil.CreateTestSubscription(int64(100000000+i), "user", "active", time.Now().Add(24*time.Hour))
		require.NoError(t, db.CreateSubscription(ctx, sub), "Failed to create subscription")
	}

	count, err := db.CountAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(5), count)
}

func TestHandleDel_GetSubscriptionByID(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	sub := testutil.CreateTestSubscription(123456789, "deltestuser", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, db.CreateSubscription(ctx, sub), "Failed to create subscription")

	got, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err, "GetByID() error")
	assert.Equal(t, sub.ID, got.ID)
}

func TestHandleDel_DeleteSubscriptionByID(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	sub := testutil.CreateTestSubscription(999888777, "deletetest", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, db.CreateSubscription(ctx, sub), "Failed to create subscription")

	id := sub.ID

	deleted, err := db.DeleteSubscriptionByID(ctx, id)
	require.NoError(t, err, "DeleteSubscriptionByID() error")
	assert.Equal(t, id, deleted.ID, "DeleteSubscriptionByID() returned wrong ID")

	_, err = db.GetByID(ctx, id)
	assert.Error(t, err, "GetByID() should return error after deletion")
}

func TestHandleDel_SubscriptionNotFound(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	_, err = db.GetByID(ctx, 99999)
	assert.Error(t, err, "GetByID() should return error for non-existent ID")
}

func TestHandleBroadcast_DatabaseFunction(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	subs := []*database.Subscription{
		{TelegramID: 111111111, Username: "user1", ClientID: "client1", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 222222222, Username: "user2", ClientID: "client2", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
	}

	for _, sub := range subs {
		require.NoError(t, db.CreateSubscription(ctx, sub), "Failed to create subscription")
	}

	ids, err := db.GetAllTelegramIDs(ctx)
	require.NoError(t, err, "GetAllTelegramIDs() error")
	assert.Len(t, ids, 2)
}

func TestHandleSend_ByTelegramID(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, db.CreateSubscription(ctx, sub), "Failed to create subscription")

	got, err := db.GetByTelegramID(ctx, 123456789)
	require.NoError(t, err, "GetByTelegramID() error")
	assert.Equal(t, int64(123456789), got.TelegramID)
}

func TestHandleSend_ByUsername(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, db.CreateSubscription(ctx, sub), "Failed to create subscription")

	id, err := db.GetTelegramIDByUsername(ctx, "testuser")
	require.NoError(t, err, "GetTelegramIDByUsername() error")
	assert.Equal(t, int64(123456789), id)
}

func TestHandleSend_UserNotFound(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	_, err = db.GetTelegramIDByUsername(ctx, "nonexistent")
	assert.Error(t, err, "GetTelegramIDByUsername() should return error for non-existent username")
}

func TestGetLatestSubscriptions(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	for i := 0; i < 15; i++ {
		sub := testutil.CreateTestSubscription(int64(100000000+i), "user", "active", time.Now().Add(24*time.Hour))
		require.NoError(t, db.CreateSubscription(ctx, sub), "Failed to create subscription")
		time.Sleep(time.Millisecond * 10)
	}

	subs, err := db.GetLatestSubscriptions(ctx, 10)
	require.NoError(t, err, "GetLatestSubscriptions() error")
	assert.Len(t, subs, 10)
}

func TestGetLatestSubscriptions_Empty(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	subs, err := db.GetLatestSubscriptions(ctx, 10)
	require.NoError(t, err, "GetLatestSubscriptions() error")
	assert.Empty(t, subs)
}

func TestGetLatestSubscriptions_OnlyActive(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	sub1 := testutil.CreateTestSubscription(100000001, "active_user", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, db.CreateSubscription(ctx, sub1), "Failed to create active subscription")

	sub2 := testutil.CreateTestSubscription(100000002, "revoked_user", "revoked", time.Now().Add(24*time.Hour))
	require.NoError(t, db.CreateSubscription(ctx, sub2), "Failed to create revoked subscription")

	subs, err := db.GetLatestSubscriptions(ctx, 10)
	require.NoError(t, err, "GetLatestSubscriptions() error")
	assert.Len(t, subs, 1)
}

func TestGetAllTelegramIDs(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	subs := []*database.Subscription{
		{TelegramID: 111111111, Username: "user1", ClientID: "client1", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 222222222, Username: "user2", ClientID: "client2", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 333333333, Username: "user3", ClientID: "client3", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 111111111, Username: "user1_alt", ClientID: "client4", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
	}

	for _, sub := range subs {
		require.NoError(t, db.CreateSubscription(ctx, sub), "Failed to create subscription")
	}

	ids, err := db.GetAllTelegramIDs(ctx)
	require.NoError(t, err, "GetAllTelegramIDs() error")
	assert.Len(t, ids, 3)
}

func TestGetAllTelegramIDs_Empty(t *testing.T) {
	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	ids, err := db.GetAllTelegramIDs(ctx)
	require.NoError(t, err, "GetAllTelegramIDs() error")
	assert.Empty(t, ids)
}
