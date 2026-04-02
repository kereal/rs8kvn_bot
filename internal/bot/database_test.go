package bot

import (
	"testing"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleStart_WithDatabase(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	if err := database.CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	got, err := database.GetByTelegramID(123456789)
	require.NoError(t, err, "GetByTelegramID() error")

	assert.Equal(t, int64(123456789), got.TelegramID)
	assert.Equal(t, "active", got.Status)
}

func TestHandleStart_NoDatabase(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	_, err = database.GetByTelegramID(999999999)
	assert.Error(t, err, "Expected error for non-existent user")
}

func TestHandleMySubscription_NoSubscription(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	_, err = database.GetByTelegramID(999999999)
	assert.Error(t, err, "Expected error for non-existent user")
}

func TestHandleMySubscription_WithSubscription(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, database.CreateSubscription(sub), "Failed to create subscription")

	got, err := database.GetByTelegramID(123456789)
	require.NoError(t, err, "GetByTelegramID() error")

	assert.Equal(t, sub.TelegramID, got.TelegramID)
	assert.Equal(t, "active", got.Status)
}

func TestHandleMySubscription_ExpiredSubscription(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(-24*time.Hour))
	require.NoError(t, database.CreateSubscription(sub), "Failed to create subscription")

	got, err := database.GetByTelegramID(123456789)
	require.NoError(t, err, "GetByTelegramID() error")

	assert.True(t, time.Now().After(got.ExpiryTime), "Expected subscription to be expired")
}

func TestHandleAdminStats(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer tdb.Cleanup()

	for i := 0; i < 5; i++ {
		sub := testutil.CreateTestSubscription(int64(100000000+i), "user", "active", time.Now().Add(24*time.Hour))
		require.NoError(t, database.CreateSubscription(sub), "Failed to create subscription")
	}

	var count int64
	database.DB.Model(&database.Subscription{}).Count(&count)
	assert.Equal(t, int64(5), count)
}

func TestHandleDel_GetSubscriptionByID(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "deltestuser", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, database.CreateSubscription(sub), "Failed to create subscription")

	got, err := database.GetSubscriptionByID(sub.ID)
	require.NoError(t, err, "GetSubscriptionByID() error")
	assert.Equal(t, sub.ID, got.ID)
}

func TestHandleDel_DeleteSubscriptionByID(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(999888777, "deletetest", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, database.CreateSubscription(sub), "Failed to create subscription")

	id := sub.ID

	deleted, err := database.DeleteSubscriptionByID(id)
	require.NoError(t, err, "DeleteSubscriptionByID() error")
	assert.Equal(t, id, deleted.ID, "DeleteSubscriptionByID() returned wrong ID")

	_, err = database.GetSubscriptionByID(id)
	assert.Error(t, err, "GetSubscriptionByID() should return error after deletion")
}

func TestHandleDel_SubscriptionNotFound(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	_, err = database.GetSubscriptionByID(99999)
	assert.Error(t, err, "GetSubscriptionByID() should return error for non-existent ID")
}

func TestHandleBroadcast_DatabaseFunction(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	subs := []*database.Subscription{
		{TelegramID: 111111111, Username: "user1", ClientID: "client1", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 222222222, Username: "user2", ClientID: "client2", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
	}

	for _, sub := range subs {
		require.NoError(t, database.CreateSubscription(sub), "Failed to create subscription")
	}

	ids, err := database.GetAllTelegramIDs()
	require.NoError(t, err, "GetAllTelegramIDs() error")
	assert.Len(t, ids, 2)
}

func TestHandleSend_ByTelegramID(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, database.CreateSubscription(sub), "Failed to create subscription")

	got, err := database.GetByTelegramID(123456789)
	require.NoError(t, err, "GetByTelegramID() error")
	assert.Equal(t, int64(123456789), got.TelegramID)
}

func TestHandleSend_ByUsername(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	sub := testutil.CreateTestSubscription(123456789, "testuser", "active", time.Now().Add(24*time.Hour))
	require.NoError(t, database.CreateSubscription(sub), "Failed to create subscription")

	id, err := database.GetTelegramIDByUsername("testuser")
	require.NoError(t, err, "GetTelegramIDByUsername() error")
	assert.Equal(t, int64(123456789), id)
}

func TestHandleSend_UserNotFound(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	_, err = database.GetTelegramIDByUsername("nonexistent")
	assert.Error(t, err, "GetTelegramIDByUsername() should return error for non-existent username")
}

func TestGetLatestSubscriptions(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	for i := 0; i < 15; i++ {
		sub := testutil.CreateTestSubscription(int64(100000000+i), "user", "active", time.Now().Add(24*time.Hour))
		require.NoError(t, database.CreateSubscription(sub), "Failed to create subscription")
		time.Sleep(time.Millisecond * 10)
	}

	subs, err := database.GetLatestSubscriptions(10)
	require.NoError(t, err, "GetLatestSubscriptions() error")
	assert.Len(t, subs, 10)
}

func TestGetLatestSubscriptions_Empty(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	subs, err := database.GetLatestSubscriptions(10)
	require.NoError(t, err, "GetLatestSubscriptions() error")
	assert.Empty(t, subs)
}

func TestGetLatestSubscriptions_OnlyActive(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	sub1 := testutil.CreateTestSubscription(100000001, "active_user", "active", time.Now().Add(24*time.Hour))
	if err := database.CreateSubscription(sub1); err != nil {
		t.Fatalf("Failed to create active subscription: %v", err)
	}

	sub2 := testutil.CreateTestSubscription(100000002, "revoked_user", "revoked", time.Now().Add(24*time.Hour))
	if err := database.DB.Create(sub2).Error; err != nil {
		t.Fatalf("Failed to create revoked subscription: %v", err)
	}

	subs, err := database.GetLatestSubscriptions(10)
	require.NoError(t, err, "GetLatestSubscriptions() error")
	assert.Len(t, subs, 1)
}

func TestGetAllTelegramIDs(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	subs := []*database.Subscription{
		{TelegramID: 111111111, Username: "user1", ClientID: "client1", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 222222222, Username: "user2", ClientID: "client2", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 333333333, Username: "user3", ClientID: "client3", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 111111111, Username: "user1_alt", ClientID: "client4", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
	}

	for _, sub := range subs {
		require.NoError(t, database.CreateSubscription(sub), "Failed to create subscription")
	}

	ids, err := database.GetAllTelegramIDs()
	require.NoError(t, err, "GetAllTelegramIDs() error")
	assert.Len(t, ids, 3)
}

func TestGetAllTelegramIDs_Empty(t *testing.T) {
	tdb, err := testutil.NewTestDatabase(t)
	require.NoError(t, err, "Failed to create test database")
	defer tdb.Cleanup()

	ids, err := database.GetAllTelegramIDs()
	require.NoError(t, err, "GetAllTelegramIDs() error")
	assert.Empty(t, ids)
}
