package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rs8kvn_bot/internal/logger"
)

func TestMain(m *testing.M) {
	_, _ = logger.Init("", "error")
	os.Exit(m.Run())
}

// ==================== Model Method Tests ====================

func TestSubscription_IsExpired(t *testing.T) {
	tests := []struct {
		name       string
		expiryTime time.Time
		want       bool
	}{
		{"expired", time.Now().Add(-1 * time.Hour), true},
		{"active", time.Now().Add(1 * time.Hour), false},
		{"expires now", time.Now(), true},
		{"expires in future", time.Now().Add(24 * time.Hour), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &Subscription{ExpiryTime: tt.expiryTime}
			assert.Equal(t, tt.want, sub.IsExpired())
		})
	}
}

func TestSubscription_IsActive(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		expiryTime time.Time
		want       bool
	}{
		{"active and not expired", "active", time.Now().Add(1 * time.Hour), true},
		{"active but expired", "active", time.Now().Add(-1 * time.Hour), false},
		{"revoked", "revoked", time.Now().Add(1 * time.Hour), false},
		{"expired status", "expired", time.Now().Add(1 * time.Hour), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &Subscription{Status: tt.status, ExpiryTime: tt.expiryTime}
			assert.Equal(t, tt.want, sub.IsActive())
		})
	}
}

func TestSubscription_TableName(t *testing.T) {
	assert.Equal(t, "subscriptions", Subscription{}.TableName())
}

func TestInvite_TableName(t *testing.T) {
	assert.Equal(t, "invites", Invite{}.TableName())
}

func TestTrialRequest_TableName(t *testing.T) {
	assert.Equal(t, "trial_requests", TrialRequest{}.TableName())
}

// ==================== Service Lifecycle Tests ====================

func TestNewService(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NotNil(t, svc.db)

	svc.Close()
}

func TestNewService_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "test.db")

	svc, err := NewService(dbPath)
	require.NoError(t, err)
	defer svc.Close()

	_, err = os.Stat(filepath.Dir(dbPath))
	assert.NoError(t, err)
}

func TestNewService_InvalidPath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "file.txt")

	require.NoError(t, os.WriteFile(dbPath, []byte("file"), 0644))

	_, err := NewService(dbPath)
	assert.Error(t, err)
}

func TestService_Close(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)
	require.NoError(t, err)

	assert.NoError(t, svc.Close())
}

func TestService_Close_AlreadyClosed(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)
	require.NoError(t, err)

	svc.Close()
	assert.NoError(t, svc.Close())
}

func TestService_Ping(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)
	require.NoError(t, err)
	defer svc.Close()

	assert.NoError(t, svc.Ping(context.Background()))
}

func TestService_GetPoolStats(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)
	require.NoError(t, err)
	defer svc.Close()

	stats, err := svc.GetPoolStats()
	require.NoError(t, err)
	assert.NotNil(t, stats)
	assert.GreaterOrEqual(t, stats.MaxOpen, 0)
}

// ==================== Service Subscription CRUD Tests ====================

func TestService_GetByTelegramID(t *testing.T) {
	svc := newTestService(t)

	sub := createTestSubscription(t, svc, 12345, "testuser", "client-1")

	retrieved, err := svc.GetByTelegramID(context.Background(), 12345)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, retrieved.ID)
	assert.Equal(t, "testuser", retrieved.Username)
	assert.Equal(t, "active", retrieved.Status)
}

func TestService_GetByTelegramID_NotFound(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.GetByTelegramID(context.Background(), 999999)
	assert.Error(t, err)
}

func TestService_GetByTelegramID_ReturnsActiveOnly(t *testing.T) {
	svc := newTestService(t)

	// Create revoked subscription
	svc.db.Create(&Subscription{
		TelegramID:      12345,
		Username:        "revoked_user",
		ClientID:        "client-revoked",
		Status:          "revoked",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/revoked",
	})

	// Create active subscription
	activeSub := &Subscription{
		TelegramID:      12345,
		Username:        "active_user",
		ClientID:        "client-active",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/active",
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), activeSub))

	retrieved, err := svc.GetByTelegramID(context.Background(), 12345)
	require.NoError(t, err)
	assert.Equal(t, "client-active", retrieved.ClientID)
	assert.Equal(t, "active", retrieved.Status)
}

func TestService_GetByTelegramID_MultipleUsers(t *testing.T) {
	svc := newTestService(t)

	users := []struct {
		telegramID int64
		username   string
	}{
		{111111111, "user1"},
		{222222222, "user2"},
		{333333333, "user3"},
	}

	for _, u := range users {
		createTestSubscription(t, svc, u.telegramID, u.username, fmt.Sprintf("client-%s", u.username))
	}

	for _, u := range users {
		got, err := svc.GetByTelegramID(context.Background(), u.telegramID)
		require.NoError(t, err)
		assert.Equal(t, u.username, got.Username)
	}
}

func TestService_CreateSubscription(t *testing.T) {
	svc := newTestService(t)

	sub := &Subscription{
		TelegramID:      54321,
		Username:        "newuser",
		ClientID:        "client-456",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/xyz",
	}

	require.NoError(t, svc.CreateSubscription(context.Background(), sub))

	retrieved, err := svc.GetByTelegramID(context.Background(), 54321)
	require.NoError(t, err)
	assert.Equal(t, "client-456", retrieved.ClientID)
}

func TestService_CreateSubscription_RevokesOldSubscription(t *testing.T) {
	svc := newTestService(t)

	telegramID := int64(123456789)

	// Create first subscription
	oldSub := &Subscription{
		TelegramID:      telegramID,
		Username:        "olduser",
		ClientID:        "old-client",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/old",
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), oldSub))

	// Create new subscription
	newSub := &Subscription{
		TelegramID:      telegramID,
		Username:        "newuser",
		ClientID:        "new-client",
		Status:          "active",
		ExpiryTime:      time.Now().Add(48 * time.Hour),
		SubscriptionURL: "http://localhost/sub/new",
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), newSub))

	// Verify old subscription was revoked
	var oldSubCheck Subscription
	require.NoError(t, svc.db.Where("client_id = ?", "old-client").First(&oldSubCheck).Error)
	assert.Equal(t, "revoked", oldSubCheck.Status)

	// Verify new subscription is active
	var newSubCheck Subscription
	require.NoError(t, svc.db.Where("client_id = ?", "new-client").First(&newSubCheck).Error)
	assert.Equal(t, "active", newSubCheck.Status)
}

func TestService_CreateSubscription_MultipleRevokes(t *testing.T) {
	svc := newTestService(t)

	telegramID := int64(123456789)

	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      telegramID,
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(time.Duration(i+1) * 24 * time.Hour),
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
			CreatedAt:       time.Now().Add(-time.Duration(3-i) * time.Minute),
		}
		require.NoError(t, svc.CreateSubscription(context.Background(), sub), "iteration %d", i)
	}

	var activeCount int64
	svc.db.Model(&Subscription{}).Where("telegram_id = ? AND status = ?", telegramID, "active").Count(&activeCount)
	assert.Equal(t, int64(1), activeCount)

	var revokedCount int64
	svc.db.Model(&Subscription{}).Where("telegram_id = ? AND status = ?", telegramID, "revoked").Count(&revokedCount)
	assert.Equal(t, int64(2), revokedCount)
}

func TestService_CreateSubscription_AllFields(t *testing.T) {
	svc := newTestService(t)

	now := time.Now()
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      now.Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/test",
	}

	require.NoError(t, svc.CreateSubscription(context.Background(), sub))

	var retrieved Subscription
	require.NoError(t, svc.db.First(&retrieved, sub.ID).Error)

	assert.Equal(t, sub.TelegramID, retrieved.TelegramID)
	assert.Equal(t, sub.Username, retrieved.Username)
	assert.Equal(t, sub.ClientID, retrieved.ClientID)
	assert.Equal(t, sub.SubscriptionID, retrieved.SubscriptionID)
	assert.Equal(t, sub.InboundID, retrieved.InboundID)
	assert.Equal(t, sub.TrafficLimit, retrieved.TrafficLimit)
	assert.Equal(t, sub.Status, retrieved.Status)
	assert.Equal(t, sub.SubscriptionURL, retrieved.SubscriptionURL)
}

func TestService_CreateSubscription_Timestamps(t *testing.T) {
	svc := newTestService(t)

	before := time.Now()
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/test",
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), sub))
	after := time.Now()

	assert.True(t, sub.CreatedAt.After(before) || sub.CreatedAt.Equal(before))
	assert.True(t, sub.CreatedAt.Before(after) || sub.CreatedAt.Equal(after))
	assert.True(t, sub.UpdatedAt.After(before) || sub.UpdatedAt.Equal(before))
	assert.True(t, sub.UpdatedAt.Before(after) || sub.UpdatedAt.Equal(after))
}

func TestService_UpdateSubscription(t *testing.T) {
	svc := newTestService(t)

	sub := &Subscription{
		TelegramID:      99999,
		Username:        "updateuser",
		ClientID:        "client-789",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/update",
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), sub))

	sub.Username = "updateduser"
	sub.TrafficLimit = 214748364800

	require.NoError(t, svc.UpdateSubscription(context.Background(), sub))

	retrieved, err := svc.GetByTelegramID(context.Background(), 99999)
	require.NoError(t, err)
	assert.Equal(t, "updateduser", retrieved.Username)
	assert.Equal(t, int64(214748364800), retrieved.TrafficLimit)
}

func TestService_UpdateSubscription_NotFound(t *testing.T) {
	svc := newTestService(t)

	sub := &Subscription{
		ID:         99999,
		TelegramID: 99999,
		Username:   "nonexistent",
		ClientID:   "nonexistent",
		Status:     "active",
	}

	err := svc.UpdateSubscription(context.Background(), sub)
	assert.NoError(t, err)
}

func TestService_DeleteSubscription(t *testing.T) {
	svc := newTestService(t)

	sub := createTestSubscription(t, svc, 77777, "deleteuser", "client-delete")

	require.NoError(t, svc.DeleteSubscription(context.Background(), 77777))

	_, err := svc.GetByTelegramID(context.Background(), 77777)
	assert.Error(t, err)

	// Verify it's soft deleted
	var deletedSub Subscription
	require.NoError(t, svc.db.Unscoped().Where("telegram_id = ?", sub.TelegramID).First(&deletedSub).Error)
	assert.True(t, deletedSub.DeletedAt.Valid)
}

func TestService_DeleteSubscription_NotFound(t *testing.T) {
	svc := newTestService(t)

	err := svc.DeleteSubscription(context.Background(), 999999)
	assert.NoError(t, err)
}

func TestService_SoftDelete(t *testing.T) {
	svc := newTestService(t)

	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/test",
	}
	require.NoError(t, svc.db.Create(sub).Error)

	// Soft delete
	require.NoError(t, svc.db.Delete(sub).Error)

	// Verify DeletedAt is set
	var deletedSub Subscription
	require.NoError(t, svc.db.Unscoped().First(&deletedSub, sub.ID).Error)
	assert.True(t, deletedSub.DeletedAt.Valid)

	// Normal query should not find the deleted subscription
	var normalSub Subscription
	err := svc.db.First(&normalSub, sub.ID).Error
	assert.Error(t, err)
}

// ==================== Service GetByID Tests ====================

func TestService_GetByID(t *testing.T) {
	svc := newTestService(t)

	sub := createTestSubscription(t, svc, 12345, "testuser", "client-1")

	retrieved, err := svc.GetByID(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, retrieved.ID)
	assert.Equal(t, sub.TelegramID, retrieved.TelegramID)
	assert.Equal(t, "testuser", retrieved.Username)
	assert.Equal(t, "client-1", retrieved.ClientID)
}

func TestService_GetByID_NotFound(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.GetByID(context.Background(), 99999)
	assert.Error(t, err)
}

// ==================== Service DeleteSubscriptionByID Tests ====================

func TestService_DeleteSubscriptionByID(t *testing.T) {
	svc := newTestService(t)

	sub := createTestSubscription(t, svc, 54321, "deleteuser", "client-delete")

	deleted, err := svc.DeleteSubscriptionByID(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, deleted.ID)
	assert.Equal(t, sub.TelegramID, deleted.TelegramID)

	// Verify it's hard deleted
	var count int64
	svc.db.Model(&Subscription{}).Unscoped().Where("id = ?", sub.ID).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestService_DeleteSubscriptionByID_NotFound(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.DeleteSubscriptionByID(context.Background(), 99999)
	assert.Error(t, err)
}

// ==================== Service GetLatestSubscriptions Tests ====================

func TestService_GetLatestSubscriptions(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 5; i++ {
		sub := &Subscription{
			TelegramID:      int64(200000000 + i),
			Username:        fmt.Sprintf("service_user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-%d", i),
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
			CreatedAt:       time.Now().Add(-time.Duration(5-i) * time.Minute),
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	subs, err := svc.GetLatestSubscriptions(context.Background(), 3)
	require.NoError(t, err)
	assert.Len(t, subs, 3)
	assert.Equal(t, "service_user4", subs[0].Username)
}

func TestService_GetLatestSubscriptions_Empty(t *testing.T) {
	svc := newTestService(t)

	subs, err := svc.GetLatestSubscriptions(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, subs, 0)
}

func TestService_GetLatestSubscriptions_OnlyActive(t *testing.T) {
	svc := newTestService(t)

	activeSub := &Subscription{
		TelegramID:      100000001,
		Username:        "active_user",
		ClientID:        "client-active",
		SubscriptionID:  "sub-active",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/active",
	}
	require.NoError(t, svc.db.Create(activeSub).Error)

	revokedSub := &Subscription{
		TelegramID:      100000002,
		Username:        "revoked_user",
		ClientID:        "client-revoked",
		SubscriptionID:  "sub-revoked",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "revoked",
		SubscriptionURL: "http://localhost/sub/revoked",
	}
	require.NoError(t, svc.db.Create(revokedSub).Error)

	subs, err := svc.GetLatestSubscriptions(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, subs, 1)
	assert.Equal(t, "active_user", subs[0].Username)
}

func TestService_GetLatestSubscriptions_LimitZero(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 5; i++ {
		createTestSubscription(t, svc, int64(100000000+i), fmt.Sprintf("user%d", i), fmt.Sprintf("client-%d", i))
	}

	subs, err := svc.GetLatestSubscriptions(context.Background(), 0)
	require.NoError(t, err)
	assert.Len(t, subs, 0)
}

func TestService_GetLatestSubscriptions_LimitOne(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 5; i++ {
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-%d", i),
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
			CreatedAt:       time.Now().Add(-time.Duration(5-i) * time.Minute),
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	subs, err := svc.GetLatestSubscriptions(context.Background(), 1)
	require.NoError(t, err)
	assert.Len(t, subs, 1)
	assert.Equal(t, "user4", subs[0].Username)
}

func TestService_GetLatestSubscriptions_LimitGreaterThanTotal(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-%d", i),
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
			CreatedAt:       time.Now().Add(-time.Duration(3-i) * time.Minute),
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	subs, err := svc.GetLatestSubscriptions(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, subs, 3)
}

func TestService_GetLatestSubscriptions_SpecialCharacters(t *testing.T) {
	svc := newTestService(t)

	specialUsernames := []string{"user_name", "user-name", "user.name", "user123", "User_Case"}

	for i, username := range specialUsernames {
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        username,
			ClientID:        fmt.Sprintf("client-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-%d", i),
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
			CreatedAt:       time.Now().Add(-time.Duration(len(specialUsernames)-i) * time.Minute),
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	subs, err := svc.GetLatestSubscriptions(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, subs, len(specialUsernames))

	foundUsernames := make(map[string]bool)
	for _, sub := range subs {
		foundUsernames[sub.Username] = true
	}

	for _, username := range specialUsernames {
		assert.True(t, foundUsernames[username], "Username %s not found", username)
	}
}

func TestService_GetLatestSubscriptions_OrderingConsistency(t *testing.T) {
	svc := newTestService(t)

	baseTime := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("ordered_user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-%d", i),
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
			CreatedAt:       baseTime.Add(time.Duration(i) * time.Hour),
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	subs, err := svc.GetLatestSubscriptions(context.Background(), 10)
	require.NoError(t, err)

	expectedOrder := []string{"ordered_user4", "ordered_user3", "ordered_user2", "ordered_user1", "ordered_user0"}

	for i, expected := range expectedOrder {
		if i >= len(subs) {
			break
		}
		assert.Equal(t, expected, subs[i].Username, "Position %d", i)
	}
}

func TestService_GetLatestSubscriptions_MixedStatuses(t *testing.T) {
	svc := newTestService(t)

	statuses := []string{"active", "revoked", "expired", "active", "active"}

	for i, status := range statuses {
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("status_user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-%d", i),
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          status,
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
			CreatedAt:       time.Now().Add(-time.Duration(len(statuses)-i) * time.Minute),
		}
		if status == "active" {
			require.NoError(t, svc.db.Create(sub).Error)
		} else {
			require.NoError(t, svc.db.Create(sub).Error)
		}
	}

	subs, err := svc.GetLatestSubscriptions(context.Background(), 10)
	require.NoError(t, err)

	expectedActive := 0
	for _, status := range statuses {
		if status == "active" {
			expectedActive++
		}
	}

	assert.Len(t, subs, expectedActive)

	for _, sub := range subs {
		assert.Equal(t, "active", sub.Status)
	}
}

// ==================== Service GetAllSubscriptions Tests ====================

func TestService_GetAllSubscriptions(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 5; i++ {
		sub := &Subscription{
			TelegramID:      int64(10000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-%d", i),
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://test.url/sub/%d", i),
		}
		require.NoError(t, svc.CreateSubscription(context.Background(), sub))
	}

	subs, err := svc.GetAllSubscriptions(context.Background())
	require.NoError(t, err)
	assert.Len(t, subs, 5)
}

func TestService_GetAllSubscriptions_Empty(t *testing.T) {
	svc := newTestService(t)

	subs, err := svc.GetAllSubscriptions(context.Background())
	require.NoError(t, err)
	assert.Len(t, subs, 0)
}

// ==================== Service Count Tests ====================

func TestService_CountActiveSubscriptions(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      int64(20000 + i),
			Username:        fmt.Sprintf("active%d", i),
			ClientID:        fmt.Sprintf("client-active-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-active-%d", i),
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://test.url/sub/active/%d", i),
		}
		require.NoError(t, svc.CreateSubscription(context.Background(), sub))
	}

	// Create expired subscription (status=active but expiry in past)
	expiredSub := &Subscription{
		TelegramID:      29999,
		Username:        "expired",
		ClientID:        "client-expired",
		SubscriptionID:  "sub-expired",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(-1 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/expired",
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), expiredSub))

	count, err := svc.CountActiveSubscriptions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(4), count)
}

func TestService_CountExpiredSubscriptions(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 2; i++ {
		sub := &Subscription{
			TelegramID:      int64(30000 + i),
			Username:        fmt.Sprintf("expired%d", i),
			ClientID:        fmt.Sprintf("client-expired-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-expired-%d", i),
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(-1 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://test.url/sub/expired/%d", i),
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	activeSub := &Subscription{
		TelegramID:      39999,
		Username:        "active",
		ClientID:        "client-active",
		SubscriptionID:  "sub-active-final",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(1 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/active",
	}
	require.NoError(t, svc.db.Create(activeSub).Error)

	count, err := svc.CountExpiredSubscriptions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestService_CountAllSubscriptions(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      int64(40000 + i),
			Username:        fmt.Sprintf("countuser%d", i),
			ClientID:        fmt.Sprintf("client-count-%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/count/%d", i),
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	count, err := svc.CountAllSubscriptions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

// ==================== Service GetAllTelegramIDs Tests ====================

func TestService_GetAllTelegramIDs(t *testing.T) {
	svc := newTestService(t)

	subs := []*Subscription{
		{TelegramID: 111111111, Username: "user1", ClientID: "client1", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 222222222, Username: "user2", ClientID: "client2", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 333333333, Username: "user3", ClientID: "client3", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 111111111, Username: "user1_alt", ClientID: "client4", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
	}

	for _, sub := range subs {
		require.NoError(t, svc.db.Create(sub).Error)
	}

	ids, err := svc.GetAllTelegramIDs(context.Background())
	require.NoError(t, err)
	assert.Len(t, ids, 3)

	idMap := make(map[int64]bool)
	for _, id := range ids {
		idMap[id] = true
	}

	for _, expectedID := range []int64{111111111, 222222222, 333333333} {
		assert.True(t, idMap[expectedID], "TelegramID %d not found", expectedID)
	}
}

func TestService_GetAllTelegramIDs_Empty(t *testing.T) {
	svc := newTestService(t)

	ids, err := svc.GetAllTelegramIDs(context.Background())
	require.NoError(t, err)
	assert.Len(t, ids, 0)
}

func TestService_GetAllTelegramIDs_Duplicates(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      111111111,
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	ids, err := svc.GetAllTelegramIDs(context.Background())
	require.NoError(t, err)
	assert.Len(t, ids, 1)
}

// ==================== Service GetTelegramIDByUsername Tests ====================

func TestService_GetTelegramIDByUsername(t *testing.T) {
	svc := newTestService(t)

	sub := &Subscription{
		TelegramID: 123456789,
		Username:   "testuser",
		ClientID:   "client-id",
		Status:     "active",
		ExpiryTime: time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, svc.db.Create(sub).Error)

	id, err := svc.GetTelegramIDByUsername(context.Background(), "testuser")
	require.NoError(t, err)
	assert.Equal(t, int64(123456789), id)
}

func TestService_GetTelegramIDByUsername_NotFound(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.GetTelegramIDByUsername(context.Background(), "nonexistent")
	assert.Error(t, err)
}

// ==================== Service GetTelegramIDsBatch Tests ====================

func TestService_GetTelegramIDsBatch(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 10; i++ {
		sub := &Subscription{
			TelegramID:      int64(50000 + i),
			Username:        fmt.Sprintf("batchuser%d", i),
			ClientID:        fmt.Sprintf("client-batch-%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/batch/%d", i),
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	ids, err := svc.GetTelegramIDsBatch(context.Background(), 0, 5)
	require.NoError(t, err)
	assert.Len(t, ids, 5)

	ids2, err := svc.GetTelegramIDsBatch(context.Background(), 5, 5)
	require.NoError(t, err)
	assert.Len(t, ids2, 5)
}

func TestService_GetTelegramIDsBatch_OffsetBeyondTotal(t *testing.T) {
	svc := newTestService(t)

	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      int64(60000 + i),
			Username:        fmt.Sprintf("offsetuser%d", i),
			ClientID:        fmt.Sprintf("client-offset-%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/offset/%d", i),
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	ids, err := svc.GetTelegramIDsBatch(context.Background(), 10, 5)
	require.NoError(t, err)
	assert.Len(t, ids, 0)
}

// ==================== Invite Tests ====================

func TestService_GetOrCreateInvite(t *testing.T) {
	svc := newTestService(t)

	invite, err := svc.GetOrCreateInvite(context.Background(), 12345, "TESTCODE123")
	require.NoError(t, err)
	assert.Equal(t, "TESTCODE123", invite.Code)
	assert.Equal(t, int64(12345), invite.ReferrerTGID)

	// Second call should return existing invite
	invite2, err := svc.GetOrCreateInvite(context.Background(), 12345, "DIFFERENTCODE")
	require.NoError(t, err)
	assert.Equal(t, "TESTCODE123", invite2.Code)
}

func TestService_GetInviteByCode(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.GetOrCreateInvite(context.Background(), 54321, "GETCODE456")
	require.NoError(t, err)

	retrieved, err := svc.GetInviteByCode(context.Background(), "GETCODE456")
	require.NoError(t, err)
	assert.Equal(t, "GETCODE456", retrieved.Code)
	assert.Equal(t, int64(54321), retrieved.ReferrerTGID)
}

func TestService_GetInviteByCode_NotFound(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.GetInviteByCode(context.Background(), "NONEXISTENT")
	assert.Error(t, err)
}

func TestService_GetReferralCount(t *testing.T) {
	svc := newTestService(t)

	referrerID := int64(22222)
	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      int64(70000 + i),
			Username:        fmt.Sprintf("referral%d", i),
			ClientID:        fmt.Sprintf("client-ref-%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/ref/%d", i),
			ReferredBy:      referrerID,
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	count, err := svc.GetReferralCount(context.Background(), referrerID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

func TestService_GetReferralCount_None(t *testing.T) {
	svc := newTestService(t)

	count, err := svc.GetReferralCount(context.Background(), 99999)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestService_GetAllReferralCounts(t *testing.T) {
	svc := newTestService(t)

	referrerID := int64(33333)
	for i := 0; i < 2; i++ {
		sub := &Subscription{
			TelegramID:      int64(80000 + i),
			Username:        fmt.Sprintf("refuser%d", i),
			ClientID:        fmt.Sprintf("client-refall-%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: fmt.Sprintf("http://localhost/refall/%d", i),
			ReferredBy:      referrerID,
		}
		require.NoError(t, svc.db.Create(sub).Error)
	}

	counts, err := svc.GetAllReferralCounts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), counts[referrerID])
}

// ==================== Trial Tests ====================

func TestService_CreateTrialRequest(t *testing.T) {
	svc := newTestService(t)

	require.NoError(t, svc.CreateTrialRequest(context.Background(), "192.168.1.1"))

	var count int64
	svc.db.Model(&TrialRequest{}).Where("ip = ?", "192.168.1.1").Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestService_CountTrialRequestsByIPLastHour(t *testing.T) {
	svc := newTestService(t)

	ip := "10.0.0.1"
	for i := 0; i < 3; i++ {
		require.NoError(t, svc.CreateTrialRequest(context.Background(), ip))
	}

	count, err := svc.CountTrialRequestsByIPLastHour(context.Background(), ip)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestService_CountTrialRequestsByIPLastHour_None(t *testing.T) {
	svc := newTestService(t)

	count, err := svc.CountTrialRequestsByIPLastHour(context.Background(), "10.0.0.99")
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestService_CleanupExpiredTrials(t *testing.T) {
	svc := newTestService(t)

	// Create old trial subscription on inbound 1
	oldTrial := &Subscription{
		TelegramID:      0,
		ClientID:        "old-trial-client",
		SubscriptionID:  "old-trial-sub",
		InboundID:       1,
		IsTrial:         true,
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/old-trial",
		CreatedAt:       time.Now().Add(-48 * time.Hour),
	}
	require.NoError(t, svc.db.Create(oldTrial).Error)

	// Create recent trial subscription
	recentTrial := &Subscription{
		TelegramID:      0,
		ClientID:        "recent-trial-client",
		SubscriptionID:  "recent-trial-sub",
		InboundID:       1,
		IsTrial:         true,
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/recent-trial",
		CreatedAt:       time.Now().Add(-1 * time.Hour),
	}
	require.NoError(t, svc.db.Create(recentTrial).Error)

	// Create old trial request
	oldRequest := &TrialRequest{
		IP:        "10.0.0.1",
		CreatedAt: time.Now().Add(-48 * time.Hour),
	}
	require.NoError(t, svc.db.Create(oldRequest).Error)

	// Create recent trial request
	recentRequest := &TrialRequest{
		IP:        "10.0.0.2",
		CreatedAt: time.Now().Add(-1 * time.Hour),
	}
	require.NoError(t, svc.db.Create(recentRequest).Error)

	mockXUI := &mockXUIClient{}

	deleted, err := svc.CleanupExpiredTrials(context.Background(), 24, mockXUI)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// Old trial should be deleted
	var oldCount int64
	svc.db.Unscoped().Model(&Subscription{}).Where("subscription_id = ?", "old-trial-sub").Count(&oldCount)
	assert.Equal(t, int64(0), oldCount)

	// Recent trial should still exist
	var recentCount int64
	svc.db.Model(&Subscription{}).Where("subscription_id = ?", "recent-trial-sub").Count(&recentCount)
	assert.Equal(t, int64(1), recentCount)

	// Old trial request should be deleted
	var oldReqCount int64
	svc.db.Model(&TrialRequest{}).Where("ip = ?", "10.0.0.1").Count(&oldReqCount)
	assert.Equal(t, int64(0), oldReqCount)

	// Recent trial request should still exist
	var recentReqCount int64
	svc.db.Model(&TrialRequest{}).Where("ip = ?", "10.0.0.2").Count(&recentReqCount)
	assert.Equal(t, int64(1), recentReqCount)

	// Verify DeleteClient was called with correct inboundID from subscription record
	require.Len(t, mockXUI.deleteCalls, 1)
	assert.Equal(t, 1, mockXUI.deleteCalls[0].inboundID)
	assert.Equal(t, "old-trial-client", mockXUI.deleteCalls[0].clientID)
}

func TestService_CleanupExpiredTrials_UsesSubInboundID(t *testing.T) {
	svc := newTestService(t)

	// Create old trial on inbound 5
	oldTrial := &Subscription{
		TelegramID:      0,
		ClientID:        "multi-inbound-client",
		SubscriptionID:  "multi-inbound-sub",
		InboundID:       5,
		IsTrial:         true,
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/multi-inbound",
		CreatedAt:       time.Now().Add(-48 * time.Hour),
	}
	require.NoError(t, svc.db.Create(oldTrial).Error)

	mockXUI := &mockXUIClient{}

	_, err := svc.CleanupExpiredTrials(context.Background(), 24, mockXUI)
	require.NoError(t, err)

	// Verify DeleteClient was called with the subscription's InboundID (5), not a hardcoded value
	require.Len(t, mockXUI.deleteCalls, 1)
	assert.Equal(t, 5, mockXUI.deleteCalls[0].inboundID)
	assert.Equal(t, "multi-inbound-client", mockXUI.deleteCalls[0].clientID)
}

// mockXUIClient implements the minimal interface needed for CleanupExpiredTrials
type mockXUIClient struct {
	deleteCalls []deleteCall
}

type deleteCall struct {
	inboundID int
	clientID  string
}

func (m *mockXUIClient) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
	m.deleteCalls = append(m.deleteCalls, deleteCall{inboundID: inboundID, clientID: clientID})
	return nil
}

// ==================== Helper Functions ====================

func newTestService(t *testing.T) *Service {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { svc.Close() })

	return svc
}

func createTestSubscription(t *testing.T, svc *Service, telegramID int64, username, clientID string) *Subscription {
	t.Helper()
	sub := &Subscription{
		TelegramID:      telegramID,
		Username:        username,
		ClientID:        clientID,
		SubscriptionID:  fmt.Sprintf("sub-%s", clientID),
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: fmt.Sprintf("http://localhost/sub/%s", clientID),
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), sub))
	return sub
}
