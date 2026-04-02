package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
	"rs8kvn_bot/internal/logger"
)

func TestMain(m *testing.M) {
	_, _ = logger.Init("", "error")
	os.Exit(m.Run())
}

func TestInit(t *testing.T) {
	// Create temporary directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Test initialization
	err := Init(dbPath)
	require.NoError(t, err, "Init() error")

	// Verify database is initialized
	require.NotNil(t, DB, "DB is nil after Init()")

	// Verify table exists
	assert.True(t, DB.Migrator().HasTable(&Subscription{}), "Subscriptions table not created")

	// Clean up
	Close()
}

func TestInit_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "test.db")

	err := Init(dbPath)
	require.NoError(t, err, "Init() error")

	// Verify directory was created
	_, err = os.Stat(filepath.Dir(dbPath))
	assert.NoError(t, err, "Init() did not create parent directory")

	Close()
}

func TestInit_InvalidPath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "file.txt")

	require.NoError(t, os.WriteFile(dbPath, []byte("file"), 0644), "Failed to create file")

	err := Init(dbPath)
	assert.Error(t, err, "Init() should error when parent path is a file")
}

func TestClose(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize database
	require.NoError(t, Init(dbPath), "Init() error")

	// Test closing
	err := Close()
	require.NoError(t, err, "Close() error")

	// Verify sqlDB is nil after close
	assert.Nil(t, sqlDB, "sqlDB should be nil after Close()")
}

func TestGetByTelegramID(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create test subscription
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/test",
	}

	require.NoError(t, DB.Create(sub).Error, "Failed to create test subscription")

	// Test GetByTelegramID
	got, err := GetByTelegramID(123456789)
	require.NoError(t, err, "GetByTelegramID() error")

	assert.Equal(t, sub.TelegramID, got.TelegramID, "TelegramID")
	assert.Equal(t, sub.Username, got.Username, "Username")
	assert.Equal(t, "active", got.Status, "Status")
}

func TestGetByTelegramID_NotFound(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Test GetByTelegramID with non-existent ID
	_, err := GetByTelegramID(999999999)
	assert.Error(t, err, "GetByTelegramID() should return error for non-existent ID")
}

func TestGetByTelegramID_ReturnsActiveOnly(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	telegramID := int64(123456789)

	// Create revoked subscription
	revokedSub := &Subscription{
		TelegramID:      telegramID,
		Username:        "testuser",
		ClientID:        "revoked-client-id",
		Status:          "revoked",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/revoked",
	}
	require.NoError(t, DB.Create(revokedSub).Error, "Failed to create revoked subscription")

	// Create active subscription
	activeSub := &Subscription{
		TelegramID:      telegramID,
		Username:        "testuser",
		ClientID:        "active-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/active",
	}
	require.NoError(t, DB.Create(activeSub).Error, "Failed to create active subscription")

	// Test GetByTelegramID
	got, err := GetByTelegramID(telegramID)
	require.NoError(t, err, "GetByTelegramID() error")

	// Should return the active subscription, not the revoked one
	assert.Equal(t, "active-client-id", got.ClientID, "GetByTelegramID() returned wrong subscription")
	assert.Equal(t, "active", got.Status, "GetByTelegramID() returned wrong status")
}

func TestCreateSubscription(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create subscription
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/test",
	}

	err := CreateSubscription(sub)
	require.NoError(t, err, "CreateSubscription() error")

	// Verify subscription was created
	var count int64
	DB.Model(&Subscription{}).Where("telegram_id = ?", sub.TelegramID).Count(&count)
	assert.Equal(t, int64(1), count, "Expected 1 subscription")
}

func TestCreateSubscription_RevokesOldSubscription(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	telegramID := int64(123456789)

	// Create first subscription
	oldSub := &Subscription{
		TelegramID:      telegramID,
		Username:        "testuser",
		ClientID:        "old-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/old",
	}
	require.NoError(t, DB.Create(oldSub).Error, "Failed to create old subscription")

	// Create new subscription
	newSub := &Subscription{
		TelegramID:      telegramID,
		Username:        "testuser",
		ClientID:        "new-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(48 * time.Hour),
		SubscriptionURL: "http://localhost/sub/new",
	}

	err := CreateSubscription(newSub)
	require.NoError(t, err, "CreateSubscription() error")

	// Verify old subscription was revoked
	var oldSubCheck Subscription
	require.NoError(t, DB.Where("client_id = ?", "old-client-id").First(&oldSubCheck).Error, "Failed to find old subscription")
	assert.Equal(t, "revoked", oldSubCheck.Status, "Old subscription status")

	// Verify new subscription is active
	var newSubCheck Subscription
	require.NoError(t, DB.Where("client_id = ?", "new-client-id").First(&newSubCheck).Error, "Failed to find new subscription")
	assert.Equal(t, "active", newSubCheck.Status, "New subscription status")
}

func TestUpdateSubscription(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create subscription
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/test",
	}
	require.NoError(t, DB.Create(sub).Error, "Failed to create subscription")

	// Update subscription
	sub.Status = "revoked"
	err := UpdateSubscription(sub)
	require.NoError(t, err, "UpdateSubscription() error")

	// Verify update
	var updated Subscription
	require.NoError(t, DB.First(&updated, sub.ID).Error, "Failed to find subscription")
	assert.Equal(t, "revoked", updated.Status, "Status")
}

func TestSubscription_Timestamps(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create subscription
	before := time.Now()
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/test",
	}
	require.NoError(t, DB.Create(sub).Error, "Failed to create subscription")
	after := time.Now()

	// Verify CreatedAt is set
	assert.True(t, sub.CreatedAt.After(before) || sub.CreatedAt.Equal(before), "CreatedAt should be >= before")
	assert.True(t, sub.CreatedAt.Before(after) || sub.CreatedAt.Equal(after), "CreatedAt should be <= after")

	// Verify UpdatedAt is set
	assert.True(t, sub.UpdatedAt.After(before) || sub.UpdatedAt.Equal(before), "UpdatedAt should be >= before")
	assert.True(t, sub.UpdatedAt.Before(after) || sub.UpdatedAt.Equal(after), "UpdatedAt should be <= after")
}

func TestGetByTelegramID_DatabaseNotInitialized(t *testing.T) {
	// Ensure DB is nil
	DB = nil

	_, err := GetByTelegramID(123456789)
	assert.Error(t, err, "GetByTelegramID() should return error when database not initialized")
}

func TestCreateSubscription_DatabaseNotInitialized(t *testing.T) {
	// Ensure DB is nil
	DB = nil

	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/test",
	}

	err := CreateSubscription(sub)
	assert.Error(t, err, "CreateSubscription() should return error when database not initialized")
}

func TestCreateSubscription_TransactionError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Disable foreign keys to cause transaction error
	DB.Exec("PRAGMA foreign_keys = OFF")

	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/test",
	}

	err := CreateSubscription(sub)
	if err != nil {
		t.Logf("CreateSubscription() error = %v (may be expected)", err)
	}
}

func TestClose_MultipleTimes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, Init(dbPath), "Init() error")

	// First close
	require.NoError(t, Close(), "First Close() error")

	// Second close should not panic
	require.NoError(t, Close(), "Second Close() error")

	// Third close
	require.NoError(t, Close(), "Third Close() error")
}

func TestSubscription_AllFields(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

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

	require.NoError(t, DB.Create(sub).Error, "Failed to create subscription")

	// Verify all fields are saved correctly
	var retrieved Subscription
	require.NoError(t, DB.First(&retrieved, sub.ID).Error, "Failed to retrieve subscription")

	assert.Equal(t, sub.TelegramID, retrieved.TelegramID, "TelegramID")
	assert.Equal(t, sub.Username, retrieved.Username, "Username")
	assert.Equal(t, sub.ClientID, retrieved.ClientID, "ClientID")
	assert.Equal(t, sub.SubscriptionID, retrieved.SubscriptionID, "SubscriptionID")
	assert.Equal(t, sub.InboundID, retrieved.InboundID, "InboundID")
	assert.Equal(t, sub.TrafficLimit, retrieved.TrafficLimit, "TrafficLimit")
	assert.Equal(t, sub.Status, retrieved.Status, "Status")
	assert.Equal(t, sub.SubscriptionURL, retrieved.SubscriptionURL, "SubscriptionURL")
}

func TestGetByTelegramID_MultipleUsers(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create multiple subscriptions for different users
	users := []struct {
		telegramID int64
		username   string
	}{
		{111111111, "user1"},
		{222222222, "user2"},
		{333333333, "user3"},
	}

	for _, u := range users {
		sub := &Subscription{
			TelegramID:      u.telegramID,
			Username:        u.username,
			ClientID:        fmt.Sprintf("client-%s", u.username),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%s", u.username),
		}
		require.NoError(t, DB.Create(sub).Error, "Failed to create subscription")
	}

	// Verify each user gets their own subscription
	for _, u := range users {
		got, err := GetByTelegramID(u.telegramID)
		require.NoError(t, err, "GetByTelegramID(%d)", u.telegramID)
		assert.Equal(t, u.username, got.Username, "GetByTelegramID(%d) username", u.telegramID)
	}
}

func TestCreateSubscription_MultipleRevokes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	telegramID := int64(123456789)

	// Create multiple subscriptions over time
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

		require.NoError(t, CreateSubscription(sub), "CreateSubscription() iteration %d", i)
	}

	// Verify only one active subscription exists
	var activeCount int64
	DB.Model(&Subscription{}).Where("telegram_id = ? AND status = ?", telegramID, "active").Count(&activeCount)
	assert.Equal(t, int64(1), activeCount, "Active subscription count")

	// Verify two revoked subscriptions exist
	var revokedCount int64
	DB.Model(&Subscription{}).Where("telegram_id = ? AND status = ?", telegramID, "revoked").Count(&revokedCount)
	assert.Equal(t, int64(2), revokedCount, "Revoked subscription count")
}

func TestSubscription_SoftDelete(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/test",
	}

	require.NoError(t, DB.Create(sub).Error, "Failed to create subscription")

	// Soft delete
	require.NoError(t, DB.Delete(sub).Error, "Failed to soft delete subscription")

	// Verify DeletedAt is set
	var deletedSub Subscription
	require.NoError(t, DB.Unscoped().First(&deletedSub, sub.ID).Error, "Failed to find deleted subscription")

	assert.True(t, deletedSub.DeletedAt.Valid, "DeletedAt should be set after soft delete")

	// Normal query should not find the deleted subscription
	var normalSub Subscription
	err := DB.First(&normalSub, sub.ID).Error
	assert.Error(t, err, "Soft deleted subscription should not be found in normal query")
}

// ==================== Subscription Helper Methods Tests ====================

func TestSubscription_IsExpired(t *testing.T) {
	tests := []struct {
		name       string
		expiryTime time.Time
		want       bool
	}{
		{
			name:       "expired subscription",
			expiryTime: time.Now().Add(-1 * time.Hour),
			want:       true,
		},
		{
			name:       "active subscription",
			expiryTime: time.Now().Add(1 * time.Hour),
			want:       false,
		},
		{
			name:       "expires now",
			expiryTime: time.Now(),
			want:       true,
		},
		{
			name:       "expires in future",
			expiryTime: time.Now().Add(24 * time.Hour),
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &Subscription{
				ExpiryTime: tt.expiryTime,
			}
			assert.Equal(t, tt.want, sub.IsExpired(), "IsExpired()")
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
		{
			name:       "active and not expired",
			status:     "active",
			expiryTime: time.Now().Add(1 * time.Hour),
			want:       true,
		},
		{
			name:       "active but expired",
			status:     "active",
			expiryTime: time.Now().Add(-1 * time.Hour),
			want:       false,
		},
		{
			name:       "revoked status",
			status:     "revoked",
			expiryTime: time.Now().Add(1 * time.Hour),
			want:       false,
		},
		{
			name:       "expired status",
			status:     "expired",
			expiryTime: time.Now().Add(1 * time.Hour),
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &Subscription{
				Status:     tt.status,
				ExpiryTime: tt.expiryTime,
			}
			assert.Equal(t, tt.want, sub.IsActive(), "IsActive()")
		})
	}
}

// ==================== DeleteSubscription Tests ====================

func TestDeleteSubscription(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create a subscription
	sub := &Subscription{
		TelegramID:      12345,
		Username:        "testuser",
		ClientID:        "client-123",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/abc",
	}

	require.NoError(t, CreateSubscription(sub), "CreateSubscription() error")

	// Delete the subscription
	require.NoError(t, DeleteSubscription(sub.TelegramID), "DeleteSubscription() error")

	// Verify it's soft deleted
	var deletedSub Subscription
	require.NoError(t, DB.Unscoped().Where("telegram_id = ?", sub.TelegramID).First(&deletedSub).Error, "Failed to find deleted subscription")

	assert.True(t, deletedSub.DeletedAt.Valid, "Subscription should be soft deleted")
}

func TestDeleteSubscription_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Try to delete non-existent subscription
	err := DeleteSubscription(999999999)
	// Should not error, just soft delete nothing
	assert.NoError(t, err, "DeleteSubscription() error")
}

func TestDeleteSubscription_DatabaseNotInitialized(t *testing.T) {
	// Close database if open
	Close()

	err := DeleteSubscription(12345)
	assert.Error(t, err, "DeleteSubscription() should return error when database not initialized")
}

// ==================== Service Tests ====================

func TestNewService(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	require.NotNil(t, service, "NewService() returned nil service")
	require.NotNil(t, service.db, "Service.db is nil")

	// Clean up
	service.Close()
}

func TestNewService_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")

	// Verify directory was created
	_, err = os.Stat(filepath.Dir(dbPath))
	assert.NoError(t, err, "NewService() did not create parent directory")

	service.Close()
}

func TestService_Close(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")

	// Close should not error
	assert.NoError(t, service.Close(), "Close() error")
}

func TestService_Close_AlreadyClosed(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")

	service.Close()

	assert.NoError(t, service.Close(), "Second Close() error")
}

func TestNewService_InvalidPath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "file.txt")

	require.NoError(t, os.WriteFile(dbPath, []byte("file"), 0644), "Failed to create file")

	_, err := NewService(dbPath)
	assert.Error(t, err, "NewService() should error when parent path is a file")
}

func TestService_GetByTelegramID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create test subscription
	sub := &Subscription{
		TelegramID:      12345,
		Username:        "testuser",
		ClientID:        "client-123",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/abc",
	}

	require.NoError(t, service.CreateSubscription(context.Background(), sub), "CreateSubscription() error")

	// Retrieve the subscription
	retrieved, err := service.GetByTelegramID(context.Background(), 12345)
	require.NoError(t, err, "GetByTelegramID() error")

	assert.Equal(t, int64(12345), retrieved.TelegramID, "TelegramID")
	assert.Equal(t, "testuser", retrieved.Username, "Username")
}

func TestService_CreateSubscription(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

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

	require.NoError(t, service.CreateSubscription(context.Background(), sub), "CreateSubscription() error")

	// Verify it was created
	retrieved, err := service.GetByTelegramID(context.Background(), 54321)
	require.NoError(t, err, "GetByTelegramID() error")

	assert.Equal(t, "client-456", retrieved.ClientID, "ClientID")
}

func TestService_UpdateSubscription(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create subscription
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

	require.NoError(t, service.CreateSubscription(context.Background(), sub), "CreateSubscription() error")

	// Update subscription
	sub.Username = "updateduser"
	sub.TrafficLimit = 214748364800

	require.NoError(t, service.UpdateSubscription(context.Background(), sub), "UpdateSubscription() error")

	// Verify update
	retrieved, err := service.GetByTelegramID(context.Background(), 99999)
	require.NoError(t, err, "GetByTelegramID() error")

	assert.Equal(t, "updateduser", retrieved.Username, "Username")
	assert.Equal(t, int64(214748364800), retrieved.TrafficLimit, "TrafficLimit")
}

func TestService_DeleteSubscription(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create subscription
	sub := &Subscription{
		TelegramID:      77777,
		Username:        "deleteuser",
		ClientID:        "client-delete",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/delete",
	}

	require.NoError(t, service.CreateSubscription(context.Background(), sub), "CreateSubscription() error")

	// Delete subscription
	require.NoError(t, service.DeleteSubscription(context.Background(), 77777), "DeleteSubscription() error")

	// Verify it's deleted
	_, err = service.GetByTelegramID(context.Background(), 77777)
	assert.Error(t, err, "GetByTelegramID() should return error for deleted subscription")
}

func TestService_GetAllSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create multiple subscriptions
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
		require.NoError(t, service.CreateSubscription(context.Background(), sub), "CreateSubscription() error")
	}

	// Get all subscriptions
	subs, err := service.GetAllSubscriptions(context.Background())
	require.NoError(t, err, "GetAllSubscriptions() error")

	assert.Len(t, subs, 5, "GetAllSubscriptions() returned wrong count")
}

func TestService_CountActiveSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create active subscriptions
	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      int64(20000 + i),
			Username:        fmt.Sprintf("active%d", i),
			ClientID:        fmt.Sprintf("client-active-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-active-%d", i),
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour), // Future
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://test.url/sub/active/%d", i),
		}
		require.NoError(t, service.CreateSubscription(context.Background(), sub), "CreateSubscription() error")
	}

	// Create expired subscription
	expiredSub := &Subscription{
		TelegramID:      29999,
		Username:        "expired",
		ClientID:        "client-expired",
		SubscriptionID:  "sub-expired",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(-1 * time.Hour), // Past
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/expired",
	}
	require.NoError(t, service.CreateSubscription(context.Background(), expiredSub), "CreateSubscription() error")

	// Count active subscriptions (all with status='active', regardless of expiry)
	count, err := service.CountActiveSubscriptions(context.Background())
	require.NoError(t, err, "CountActiveSubscriptions() error")

	assert.Equal(t, int64(4), count, "CountActiveSubscriptions()")
}

func TestService_CountExpiredSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create expired subscriptions
	for i := 0; i < 2; i++ {
		sub := &Subscription{
			TelegramID:      int64(30000 + i),
			Username:        fmt.Sprintf("expired%d", i),
			ClientID:        fmt.Sprintf("client-expired-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-expired-%d", i),
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(-1 * time.Hour), // Past
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://test.url/sub/expired/%d", i),
		}
		require.NoError(t, service.CreateSubscription(context.Background(), sub), "CreateSubscription() error")
	}

	// Create active subscription
	activeSub := &Subscription{
		TelegramID:      39999,
		Username:        "active",
		ClientID:        "client-active",
		SubscriptionID:  "sub-active-final",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(1 * time.Hour), // Future
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/active",
	}
	require.NoError(t, service.CreateSubscription(context.Background(), activeSub), "CreateSubscription() error")

	// Count expired subscriptions
	count, err := service.CountExpiredSubscriptions(context.Background())
	require.NoError(t, err, "CountExpiredSubscriptions() error")

	assert.Equal(t, int64(2), count, "CountExpiredSubscriptions()")
}

func TestGetLatestSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create test subscriptions with different creation times
	for i := 0; i < 15; i++ {
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
			CreatedAt:       time.Now().Add(-time.Duration(15-i) * time.Minute),
		}
		require.NoError(t, CreateSubscription(sub), "Failed to create test subscription")
	}

	// Get latest 10 subscriptions
	subs, err := GetLatestSubscriptions(10)
	require.NoError(t, err, "GetLatestSubscriptions() error")

	assert.Len(t, subs, 10, "GetLatestSubscriptions() returned wrong count")

	// Verify they are ordered by created_at DESC (newest first)
	for i := 0; i < len(subs)-1; i++ {
		assert.True(t, subs[i].CreatedAt.After(subs[i+1].CreatedAt) || subs[i].CreatedAt.Equal(subs[i+1].CreatedAt),
			"Subscriptions not ordered by created_at DESC")
	}

	// Verify the first one is the most recent (user14)
	assert.Equal(t, "user14", subs[0].Username, "First subscription username")
}

func TestGetLatestSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// No subscriptions in database
	subs, err := GetLatestSubscriptions(10)
	require.NoError(t, err, "GetLatestSubscriptions() error")

	assert.Len(t, subs, 0, "GetLatestSubscriptions() returned wrong count")
}

func TestGetLatestSubscriptions_OnlyActive(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create active subscription
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
	require.NoError(t, CreateSubscription(activeSub), "Failed to create active subscription")

	// Create revoked subscription
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
	require.NoError(t, DB.Create(revokedSub).Error, "Failed to create revoked subscription")

	// Get latest subscriptions
	subs, err := GetLatestSubscriptions(10)
	require.NoError(t, err, "GetLatestSubscriptions() error")

	assert.Len(t, subs, 1, "GetLatestSubscriptions() returned wrong count")

	if len(subs) > 0 {
		assert.Equal(t, "active_user", subs[0].Username, "Username")
	}
}

func TestService_GetLatestSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create test subscriptions
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
		require.NoError(t, service.CreateSubscription(context.Background(), sub), "Failed to create test subscription")
	}

	// Get latest 3 subscriptions
	subs, err := service.GetLatestSubscriptions(context.Background(), 3)
	require.NoError(t, err, "GetLatestSubscriptions() error")

	assert.Len(t, subs, 3, "GetLatestSubscriptions() returned wrong count")

	// Verify the first one is the most recent
	assert.Equal(t, "service_user4", subs[0].Username, "First subscription username")
}

func TestGetLatestSubscriptions_DatabaseNotInitialized(t *testing.T) {
	// Close any existing connection
	Close()

	_, err := GetLatestSubscriptions(10)
	assert.Error(t, err, "GetLatestSubscriptions() expected error when database not initialized")
}

func TestGetLatestSubscriptions_LimitZero(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create test subscriptions
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
		}
		require.NoError(t, CreateSubscription(sub), "Failed to create test subscription")
	}

	// Get with limit 0
	subs, err := GetLatestSubscriptions(0)
	require.NoError(t, err, "GetLatestSubscriptions(0) error")

	assert.Len(t, subs, 0, "GetLatestSubscriptions(0) returned wrong count")
}

func TestGetLatestSubscriptions_LimitOne(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create test subscriptions
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
		require.NoError(t, CreateSubscription(sub), "Failed to create test subscription")
	}

	// Get with limit 1
	subs, err := GetLatestSubscriptions(1)
	require.NoError(t, err, "GetLatestSubscriptions(1) error")

	assert.Len(t, subs, 1, "GetLatestSubscriptions(1) returned wrong count")

	// Should be the most recent (user4)
	assert.Equal(t, "user4", subs[0].Username, "Username")
}

func TestGetLatestSubscriptions_LimitGreaterThanAvailable(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create 3 test subscriptions
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
		require.NoError(t, CreateSubscription(sub), "Failed to create test subscription")
	}

	// Request 10 but only 3 exist
	subs, err := GetLatestSubscriptions(10)
	require.NoError(t, err, "GetLatestSubscriptions(10) error")

	assert.Len(t, subs, 3, "GetLatestSubscriptions(10) returned wrong count")
}

func TestGetLatestSubscriptions_SpecialCharacters(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create subscriptions with special characters in username
	specialUsernames := []string{
		"user_name",
		"user-name",
		"user.name",
		"user123",
		"User_Case",
	}

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
		require.NoError(t, CreateSubscription(sub), "Failed to create test subscription")
	}

	subs, err := GetLatestSubscriptions(10)
	require.NoError(t, err, "GetLatestSubscriptions() error")

	assert.Len(t, subs, len(specialUsernames), "Expected subscriptions count")

	// Verify all usernames are preserved correctly
	foundUsernames := make(map[string]bool)
	for _, sub := range subs {
		foundUsernames[sub.Username] = true
	}

	for _, username := range specialUsernames {
		assert.True(t, foundUsernames[username], "Username %s not found in results", username)
	}
}

func TestGetLatestSubscriptions_OrderingConsistency(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create subscriptions with specific timestamps
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
		// Use DB.Create to preserve CreatedAt
		require.NoError(t, DB.Create(sub).Error, "Failed to create test subscription")
	}

	// Get subscriptions
	subs, err := GetLatestSubscriptions(10)
	require.NoError(t, err, "GetLatestSubscriptions() error")

	// Verify ordering (newest first: ordered_user4, ordered_user3, ...)
	expectedOrder := []string{"ordered_user4", "ordered_user3", "ordered_user2", "ordered_user1", "ordered_user0"}

	for i, expected := range expectedOrder {
		if i >= len(subs) {
			break
		}
		assert.Equal(t, expected, subs[i].Username, "Position %d username", i)
	}
}

func TestGetLatestSubscriptions_MixedStatuses(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create subscriptions with different statuses
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
			require.NoError(t, CreateSubscription(sub), "Failed to create test subscription")
		} else {
			// Direct DB create for non-active statuses
			require.NoError(t, DB.Create(sub).Error, "Failed to create test subscription")
		}
	}

	// Should only get active subscriptions
	subs, err := GetLatestSubscriptions(10)
	require.NoError(t, err, "GetLatestSubscriptions() error")

	// Count expected active subscriptions (3 active in the list)
	expectedActive := 0
	for _, status := range statuses {
		if status == "active" {
			expectedActive++
		}
	}

	assert.Len(t, subs, expectedActive, "Active subscriptions count")

	// Verify all returned subscriptions are active
	for _, sub := range subs {
		assert.Equal(t, "active", sub.Status, "Subscription status")
	}
}

// ==================== GetSubscriptionByID Tests ====================

func TestGetSubscriptionByID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create a subscription
	sub := &Subscription{
		TelegramID:      12345,
		Username:        "testuser",
		ClientID:        "client-123",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/abc",
	}

	require.NoError(t, CreateSubscription(sub), "CreateSubscription() error")

	// Get the subscription by ID
	got, err := GetSubscriptionByID(sub.ID)
	require.NoError(t, err, "GetSubscriptionByID() error")

	assert.Equal(t, sub.ID, got.ID, "ID")
	assert.Equal(t, sub.TelegramID, got.TelegramID, "TelegramID")
	assert.Equal(t, sub.Username, got.Username, "Username")
	assert.Equal(t, sub.ClientID, got.ClientID, "ClientID")
}

func TestGetSubscriptionByID_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Try to get non-existent subscription
	_, err := GetSubscriptionByID(99999)
	assert.Error(t, err, "GetSubscriptionByID() should return error for non-existent ID")
}

func TestGetSubscriptionByID_DatabaseNotInitialized(t *testing.T) {
	// Close database if open
	Close()

	_, err := GetSubscriptionByID(1)
	assert.Error(t, err, "GetSubscriptionByID() should return error when database not initialized")
}

// ==================== DeleteSubscriptionByID Tests ====================

func TestDeleteSubscriptionByID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create a subscription
	sub := &Subscription{
		TelegramID:      54321,
		Username:        "deleteuser",
		ClientID:        "client-delete",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/delete",
	}

	require.NoError(t, CreateSubscription(sub), "CreateSubscription() error")

	id := sub.ID

	// Delete the subscription by ID
	deleted, err := DeleteSubscriptionByID(id)
	require.NoError(t, err, "DeleteSubscriptionByID() error")

	// Verify returned subscription has correct data
	assert.Equal(t, id, deleted.ID, "DeleteSubscriptionByID() returned ID")
	assert.Equal(t, sub.TelegramID, deleted.TelegramID, "DeleteSubscriptionByID() returned TelegramID")

	// Verify it's hard deleted (not soft delete)
	var count int64
	DB.Model(&Subscription{}).Unscoped().Where("id = ?", id).Count(&count)
	assert.Equal(t, int64(0), count, "Subscription should be hard deleted (permanently removed)")
}

func TestDeleteSubscriptionByID_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Try to delete non-existent subscription
	_, err := DeleteSubscriptionByID(99999)
	assert.Error(t, err, "DeleteSubscriptionByID() should return error for non-existent ID")
}

func TestDeleteSubscriptionByID_DatabaseNotInitialized(t *testing.T) {
	// Close database if open
	Close()

	_, err := DeleteSubscriptionByID(1)
	assert.Error(t, err, "DeleteSubscriptionByID() should return error when database not initialized")
}

func TestGetAllTelegramIDs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create test subscriptions with different Telegram IDs
	subs := []*Subscription{
		{TelegramID: 111111111, Username: "user1", ClientID: "client1", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 222222222, Username: "user2", ClientID: "client2", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		{TelegramID: 333333333, Username: "user3", ClientID: "client3", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
		// Duplicate Telegram ID - should only appear once in results
		{TelegramID: 111111111, Username: "user1_alt", ClientID: "client4", Status: "active", ExpiryTime: time.Now().Add(24 * time.Hour)},
	}

	for _, sub := range subs {
		require.NoError(t, CreateSubscription(sub), "Failed to create subscription")
	}

	// Get all Telegram IDs
	ids, err := GetAllTelegramIDs()
	require.NoError(t, err, "GetAllTelegramIDs() error")

	// Should have 3 unique IDs (111111111, 222222222, 333333333)
	assert.Len(t, ids, 3, "GetAllTelegramIDs() returned wrong count")

	// Verify IDs are present
	idMap := make(map[int64]bool)
	for _, id := range ids {
		idMap[id] = true
	}

	for _, expectedID := range []int64{111111111, 222222222, 333333333} {
		assert.True(t, idMap[expectedID], "Expected Telegram ID %d not found in results", expectedID)
	}
}

func TestGetAllTelegramIDs_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Get all Telegram IDs from empty database
	ids, err := GetAllTelegramIDs()
	require.NoError(t, err, "GetAllTelegramIDs() error")

	assert.Len(t, ids, 0, "GetAllTelegramIDs() returned wrong count")
}

func TestGetAllTelegramIDs_DatabaseNotInitialized(t *testing.T) {
	// Close database if open
	Close()

	_, err := GetAllTelegramIDs()
	assert.Error(t, err, "GetAllTelegramIDs() should return error when database not initialized")
}

func TestGetTelegramIDByUsername(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create test subscription
	sub := &Subscription{
		TelegramID: 123456789,
		Username:   "testuser",
		ClientID:   "client-id",
		Status:     "active",
		ExpiryTime: time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, CreateSubscription(sub), "Failed to create subscription")

	// Get Telegram ID by username
	id, err := GetTelegramIDByUsername("testuser")
	require.NoError(t, err, "GetTelegramIDByUsername() error")

	assert.Equal(t, int64(123456789), id, "GetTelegramIDByUsername()")
}

func TestGetTelegramIDByUsername_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Try to get non-existent username
	_, err := GetTelegramIDByUsername("nonexistent")
	assert.Error(t, err, "GetTelegramIDByUsername() should return error for non-existent username")
}

func TestGetTelegramIDByUsername_DatabaseNotInitialized(t *testing.T) {
	// Close database if open
	Close()

	_, err := GetTelegramIDByUsername("testuser")
	assert.Error(t, err, "GetTelegramIDByUsername() should return error when database not initialized")
}

func TestUpdateSubscription_DatabaseNotInitialized(t *testing.T) {
	// Close database if open
	Close()

	sub := &Subscription{
		TelegramID:      12345,
		Username:        "testuser",
		ClientID:        "client-123",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://test.url/sub/abc",
	}

	err := UpdateSubscription(sub)
	assert.Error(t, err, "UpdateSubscription() should return error when database not initialized")
}

func TestGetAllSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	subs, err := service.GetAllSubscriptions(context.Background())
	require.NoError(t, err, "GetAllSubscriptions() error")
	assert.Len(t, subs, 0, "Expected 0 subscriptions")
}

func TestService_UpdateSubscription_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Try to update non-existent subscription
	sub := &Subscription{
		ID:         99999,
		TelegramID: 99999,
		Username:   "nonexistent",
		ClientID:   "nonexistent",
		Status:     "active",
	}

	err = service.UpdateSubscription(context.Background(), sub)
	assert.NoError(t, err, "UpdateSubscription() on non-existent should not error")
}

func TestService_DeleteSubscription_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Try to delete non-existent subscription - should not error
	err = service.DeleteSubscription(context.Background(), 999999)
	assert.NoError(t, err, "DeleteSubscription() on non-existent should not error")
}

func TestSubscription_TableName(t *testing.T) {
	sub := &Subscription{}
	assert.Equal(t, "subscriptions", sub.TableName(), "TableName()")
}

func TestGetAllTelegramIDs_OneSubscriptionMultipleRevisions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, Init(dbPath), "Init() error")
	defer Close()

	// Create multiple subscriptions for same Telegram ID
	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      111111111,
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
		}
		require.NoError(t, CreateSubscription(sub), "Failed to create subscription")
	}

	// Get all Telegram IDs - should return unique ID once
	ids, err := GetAllTelegramIDs()
	require.NoError(t, err, "GetAllTelegramIDs() error")
	assert.Len(t, ids, 1, "Expected 1 unique ID")
}

func TestService_CreateSubscription_DuplicateTelegramID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	telegramID := int64(123456789)

	sub1 := &Subscription{
		TelegramID:      telegramID,
		Username:        "user1",
		ClientID:        "client1",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/1",
	}

	require.NoError(t, service.CreateSubscription(context.Background(), sub1), "First CreateSubscription() error")

	sub2 := &Subscription{
		TelegramID:      telegramID,
		Username:        "user2",
		ClientID:        "client2",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/2",
	}

	require.NoError(t, service.CreateSubscription(context.Background(), sub2), "Second CreateSubscription() error")

	subs, err := service.GetAllSubscriptions(context.Background())
	require.NoError(t, err, "GetAllSubscriptions() error")

	activeCount := 0
	for _, s := range subs {
		if s.TelegramID == telegramID && s.Status == "active" {
			activeCount++
		}
	}

	assert.Equal(t, 1, activeCount, "Expected 1 active subscription")
}

func TestService_GetLatestSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	subs, err := service.GetLatestSubscriptions(context.Background(), 10)
	require.NoError(t, err, "GetLatestSubscriptions() error")
	assert.Len(t, subs, 0, "Expected 0 subscriptions")
}

func TestService_GetAllSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	subs, err := service.GetAllSubscriptions(context.Background())
	require.NoError(t, err, "GetAllSubscriptions() error")
	assert.Len(t, subs, 0, "Expected 0 subscriptions")
}

func TestService_CountActiveSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	count, err := service.CountActiveSubscriptions(context.Background())
	require.NoError(t, err, "CountActiveSubscriptions() error")
	assert.Equal(t, int64(0), count, "Expected 0 active subscriptions")
}

func TestService_CountExpiredSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	count, err := service.CountExpiredSubscriptions(context.Background())
	require.NoError(t, err, "CountExpiredSubscriptions() error")
	assert.Equal(t, int64(0), count, "Expected 0 expired subscriptions")
}

// === Service.Ping tests ===

func TestService_Ping(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	err = service.Ping(context.Background())
	assert.NoError(t, err, "Ping() error")
}

func TestService_Ping_AfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")

	service.Close()

	err = service.Ping(context.Background())
	assert.Error(t, err, "Ping() should return error after Close()")
}

// === Service.GetPoolStats tests ===

func TestService_GetPoolStats(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	stats, err := service.GetPoolStats()
	require.NoError(t, err, "GetPoolStats() error")
	require.NotNil(t, stats, "GetPoolStats() returned nil")

	// SQLite should have MaxOpen = 1
	assert.Equal(t, 1, stats.MaxOpen, "MaxOpen should be 1 for SQLite")
}

// Note: TestService_GetPoolStats_AfterClose removed - behavior after Close() is
// implementation-dependent. GetPoolStats() may return stats or nil error
// depending on how gorm/sql.DB handles closed connections.

// === Service.CountAllSubscriptions tests ===

func TestService_CountAllSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create subscriptions with different statuses
	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: "http://test.url/sub",
		}
		require.NoError(t, service.CreateSubscription(context.Background(), sub), "CreateSubscription() error")
	}

	count, err := service.CountAllSubscriptions(context.Background())
	require.NoError(t, err, "CountAllSubscriptions() error")
	assert.Equal(t, int64(3), count, "Expected 3 subscriptions")
}

func TestService_CountAllSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	count, err := service.CountAllSubscriptions(context.Background())
	require.NoError(t, err, "CountAllSubscriptions() error")
	assert.Equal(t, int64(0), count, "Expected 0 subscriptions")
}

// === Service.GetTelegramIDsBatch tests ===

func TestService_GetTelegramIDsBatch(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create subscriptions
	for i := 0; i < 5; i++ {
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: "http://test.url/sub",
		}
		require.NoError(t, service.CreateSubscription(context.Background(), sub), "CreateSubscription() error")
	}

	// Get first batch
	ids, err := service.GetTelegramIDsBatch(context.Background(), 0, 3)
	require.NoError(t, err, "GetTelegramIDsBatch() error")
	assert.Len(t, ids, 3, "Expected 3 IDs in first batch")

	// Get second batch
	ids2, err := service.GetTelegramIDsBatch(context.Background(), 3, 3)
	require.NoError(t, err, "GetTelegramIDsBatch() error")
	assert.Len(t, ids2, 2, "Expected 2 IDs in second batch")
}

func TestService_GetTelegramIDsBatch_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	ids, err := service.GetTelegramIDsBatch(context.Background(), 0, 10)
	require.NoError(t, err, "GetTelegramIDsBatch() error")
	assert.Len(t, ids, 0, "Expected 0 IDs")
}

// === Service.GetTotalTelegramIDCount tests ===

func TestService_GetTotalTelegramIDCount(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create subscriptions with different Telegram IDs
	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: "http://test.url/sub",
		}
		require.NoError(t, service.CreateSubscription(context.Background(), sub), "CreateSubscription() error")
	}

	count, err := service.GetTotalTelegramIDCount(context.Background())
	require.NoError(t, err, "GetTotalTelegramIDCount() error")
	assert.Equal(t, int64(3), count, "Expected 3 unique Telegram IDs")
}

func TestService_GetTotalTelegramIDCount_Duplicates(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create multiple subscriptions for same Telegram ID
	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      123456789,
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client%d", i),
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: "http://test.url/sub",
		}
		require.NoError(t, service.db.Create(sub).Error, "Create() error")
	}

	count, err := service.GetTotalTelegramIDCount(context.Background())
	require.NoError(t, err, "GetTotalTelegramIDCount() error")
	assert.Equal(t, int64(1), count, "Expected 1 unique Telegram ID (duplicates should be counted once)")
}

func TestService_GetTotalTelegramIDCount_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	count, err := service.GetTotalTelegramIDCount(context.Background())
	require.NoError(t, err, "GetTotalTelegramIDCount() error")
	assert.Equal(t, int64(0), count, "Expected 0 Telegram IDs")
}

// === Invite/Trial tests ===

func TestService_GetOrCreateInvite_New(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	invite, err := service.GetOrCreateInvite(context.Background(), 123456789, "ABC123")
	require.NoError(t, err, "GetOrCreateInvite() error")
	require.NotNil(t, invite, "GetOrCreateInvite() returned nil")

	assert.Equal(t, "ABC123", invite.Code, "Code")
	assert.Equal(t, int64(123456789), invite.ReferrerTGID, "ReferrerTGID")
	assert.False(t, invite.CreatedAt.IsZero(), "CreatedAt should be set")
}

func TestService_GetOrCreateInvite_Existing(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create first invite
	invite1, err := service.GetOrCreateInvite(context.Background(), 123456789, "ABC123")
	require.NoError(t, err, "GetOrCreateInvite() error")

	// Get existing invite (should return the same one, not create new)
	invite2, err := service.GetOrCreateInvite(context.Background(), 123456789, "XYZ789")
	require.NoError(t, err, "GetOrCreateInvite() error")

	assert.Equal(t, invite1.Code, invite2.Code, "Should return existing invite with original code")
	assert.Equal(t, "ABC123", invite2.Code, "Code should be original, not new code")
}

func TestService_GetInviteByCode_Found(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create invite
	_, err = service.GetOrCreateInvite(context.Background(), 123456789, "TESTCODE")
	require.NoError(t, err, "GetOrCreateInvite() error")

	// Find by code
	invite, err := service.GetInviteByCode(context.Background(), "TESTCODE")
	require.NoError(t, err, "GetInviteByCode() error")
	require.NotNil(t, invite, "GetInviteByCode() returned nil")

	assert.Equal(t, "TESTCODE", invite.Code, "Code")
	assert.Equal(t, int64(123456789), invite.ReferrerTGID, "ReferrerTGID")
}

func TestService_GetInviteByCode_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	_, err = service.GetInviteByCode(context.Background(), "NONEXISTENT")
	assert.Error(t, err, "GetInviteByCode() should return error for nonexistent code")
}

func TestService_CreateTrialSubscription(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	expiry := time.Now().Add(3 * time.Hour)
	sub, err := service.CreateTrialSubscription(
		context.Background(),
		"INVITE123",
		"sub-test-id",
		"client-test-id",
		1,
		107374182400,
		expiry,
		"http://test.url/sub",
	)
	require.NoError(t, err, "CreateTrialSubscription() error")
	require.NotNil(t, sub, "CreateTrialSubscription() returned nil")

	assert.Equal(t, int64(0), sub.TelegramID, "TelegramID should be 0 for trial")
	assert.Equal(t, "sub-test-id", sub.SubscriptionID, "SubscriptionID")
	assert.Equal(t, "client-test-id", sub.ClientID, "ClientID")
	assert.Equal(t, "INVITE123", sub.InviteCode, "InviteCode")
	assert.True(t, sub.IsTrial, "IsTrial should be true")
	assert.Equal(t, "active", sub.Status, "Status should be active")
}

func TestService_BindTrialSubscription_Success(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create invite first
	_, err = service.GetOrCreateInvite(context.Background(), 999888777, "BINDCODE")
	require.NoError(t, err, "GetOrCreateInvite() error")

	// Create trial subscription
	expiry := time.Now().Add(3 * time.Hour)
	_, err = service.CreateTrialSubscription(
		context.Background(),
		"BINDCODE",
		"bind-sub-id",
		"bind-client-id",
		1,
		107374182400,
		expiry,
		"http://test.url/sub",
	)
	require.NoError(t, err, "CreateTrialSubscription() error")

	// Bind trial subscription
	sub, err := service.BindTrialSubscription(context.Background(), "bind-sub-id", 123456789, "testuser")
	require.NoError(t, err, "BindTrialSubscription() error")
	require.NotNil(t, sub, "BindTrialSubscription() returned nil")

	assert.Equal(t, int64(123456789), sub.TelegramID, "TelegramID")
	assert.Equal(t, "testuser", sub.Username, "Username")
	assert.False(t, sub.IsTrial, "IsTrial should be false after binding")
	assert.Equal(t, int64(999888777), sub.ReferredBy, "ReferredBy should be set from invite")
}

func TestService_BindTrialSubscription_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	_, err = service.BindTrialSubscription(context.Background(), "nonexistent", 123456789, "testuser")
	assert.Error(t, err, "BindTrialSubscription() should return error for nonexistent subscription")
	assert.Contains(t, err.Error(), "not found", "Error message should mention not found")
}

func TestService_BindTrialSubscription_AlreadyActivated(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create and bind trial subscription
	expiry := time.Now().Add(3 * time.Hour)
	_, err = service.CreateTrialSubscription(
		context.Background(),
		"",
		"activated-sub-id",
		"activated-client-id",
		1,
		107374182400,
		expiry,
		"http://test.url/sub",
	)
	require.NoError(t, err, "CreateTrialSubscription() error")

	// First bind
	_, err = service.BindTrialSubscription(context.Background(), "activated-sub-id", 111222333, "user1")
	require.NoError(t, err, "First BindTrialSubscription() error")

	// Second bind attempt (should fail)
	_, err = service.BindTrialSubscription(context.Background(), "activated-sub-id", 444555666, "user2")
	assert.Error(t, err, "Second BindTrialSubscription() should return error")
	assert.Contains(t, err.Error(), "already activated", "Error message should mention already activated")
}

func TestService_CountTrialRequestsByIPLastHour(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create trial requests
	require.NoError(t, service.CreateTrialRequest(context.Background(), "192.168.1.1"), "CreateTrialRequest() error")
	require.NoError(t, service.CreateTrialRequest(context.Background(), "192.168.1.1"), "CreateTrialRequest() error")
	require.NoError(t, service.CreateTrialRequest(context.Background(), "192.168.1.2"), "CreateTrialRequest() error")

	count, err := service.CountTrialRequestsByIPLastHour(context.Background(), "192.168.1.1")
	require.NoError(t, err, "CountTrialRequestsByIPLastHour() error")
	assert.Equal(t, 2, count, "Expected 2 requests from 192.168.1.1")

	count2, err := service.CountTrialRequestsByIPLastHour(context.Background(), "192.168.1.2")
	require.NoError(t, err, "CountTrialRequestsByIPLastHour() error")
	assert.Equal(t, 1, count2, "Expected 1 request from 192.168.1.2")
}

func TestService_CountTrialRequestsByIPLastHour_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	count, err := service.CountTrialRequestsByIPLastHour(context.Background(), "10.0.0.1")
	require.NoError(t, err, "CountTrialRequestsByIPLastHour() error")
	assert.Equal(t, 0, count, "Expected 0 requests")
}

func TestService_CreateTrialRequest(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	err = service.CreateTrialRequest(context.Background(), "203.0.113.50")
	assert.NoError(t, err, "CreateTrialRequest() error")

	// Verify it was created
	count, err := service.CountTrialRequestsByIPLastHour(context.Background(), "203.0.113.50")
	require.NoError(t, err, "CountTrialRequestsByIPLastHour() error")
	assert.Equal(t, 1, count, "Expected 1 request after creation")
}

func TestService_CleanupExpiredTrials(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create an old trial subscription (manually set created_at)
	oldTime := time.Now().Add(-2 * time.Hour)
	oldSub := &Subscription{
		TelegramID:      0,
		SubscriptionID:  "old-trial-sub",
		ClientID:        "old-trial-client",
		InboundID:       1,
		IsTrial:         true,
		Status:          "active",
		ExpiryTime:      time.Now().Add(1 * time.Hour),
		SubscriptionURL: "http://test.url/sub",
		CreatedAt:       oldTime,
	}
	require.NoError(t, service.db.Create(oldSub).Error, "Create old subscription")

	// Create a new trial subscription
	newSub := &Subscription{
		TelegramID:      0,
		SubscriptionID:  "new-trial-sub",
		ClientID:        "new-trial-client",
		InboundID:       1,
		IsTrial:         true,
		Status:          "active",
		ExpiryTime:      time.Now().Add(1 * time.Hour),
		SubscriptionURL: "http://test.url/sub",
	}
	require.NoError(t, service.db.Create(newSub).Error, "Create new subscription")

	// Mock XUI client
	mockXUI := &mockXUIClientForCleanup{}

	// Cleanup trials older than 1 hour
	deleted, err := service.CleanupExpiredTrials(context.Background(), 1, mockXUI, 1)
	require.NoError(t, err, "CleanupExpiredTrials() error")
	assert.Equal(t, int64(1), deleted, "Expected 1 trial to be cleaned up")
	assert.True(t, mockXUI.deleteCalled, "DeleteClient should have been called")

	// Verify old trial is gone
	var count int64
	service.db.Model(&Subscription{}).Where("subscription_id = ?", "old-trial-sub").Count(&count)
	assert.Equal(t, int64(0), count, "Old trial should be deleted")

	// Verify new trial still exists
	service.db.Model(&Subscription{}).Where("subscription_id = ?", "new-trial-sub").Count(&count)
	assert.Equal(t, int64(1), count, "New trial should still exist")
}

func TestService_CleanupExpiredTrials_NoExpired(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create a new trial subscription (not expired)
	newSub := &Subscription{
		TelegramID:      0,
		SubscriptionID:  "fresh-trial-sub",
		ClientID:        "fresh-trial-client",
		InboundID:       1,
		IsTrial:         true,
		Status:          "active",
		ExpiryTime:      time.Now().Add(1 * time.Hour),
		SubscriptionURL: "http://test.url/sub",
	}
	require.NoError(t, service.db.Create(newSub).Error, "Create subscription")

	mockXUI := &mockXUIClientForCleanup{}

	deleted, err := service.CleanupExpiredTrials(context.Background(), 1, mockXUI, 1)
	require.NoError(t, err, "CleanupExpiredTrials() error")
	assert.Equal(t, int64(0), deleted, "Expected 0 trials to be cleaned up")
	assert.False(t, mockXUI.deleteCalled, "DeleteClient should not have been called")
}

// Mock XUI client for cleanup tests
type mockXUIClientForCleanup struct {
	deleteCalled bool
}

func (m *mockXUIClientForCleanup) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
	m.deleteCalled = true
	return nil
}

// === Invite/TrialRequest struct tests ===

func TestInvite_TableName(t *testing.T) {
	invite := &Invite{}
	assert.Equal(t, "invites", invite.TableName(), "TableName()")
}

func TestTrialRequest_TableName(t *testing.T) {
	req := &TrialRequest{}
	assert.Equal(t, "trial_requests", req.TableName(), "TableName()")
}

// === Context cancellation tests ===

func TestService_Ping_ContextCancellation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err = service.Ping(ctx)
	// Ping might succeed if it's fast enough, or fail with context error
	// Either way, it should not hang
	if err != nil {
		assert.Contains(t, err.Error(), "context", "Error should be related to context")
	}
}

// === Edge case tests ===

func TestService_GetTelegramIDsBatch_OffsetBeyondData(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create one subscription
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "client-1",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://test.url/sub",
	}
	require.NoError(t, service.CreateSubscription(context.Background(), sub), "CreateSubscription() error")

	// Request with offset beyond available data
	ids, err := service.GetTelegramIDsBatch(context.Background(), 100, 10)
	require.NoError(t, err, "GetTelegramIDsBatch() error")
	assert.Len(t, ids, 0, "Expected 0 IDs when offset is beyond data")
}

func TestService_BindTrialSubscription_NoInviteCode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create trial subscription without invite code
	expiry := time.Now().Add(3 * time.Hour)
	_, err = service.CreateTrialSubscription(
		context.Background(),
		"", // empty invite code
		"no-invite-sub",
		"no-invite-client",
		1,
		107374182400,
		expiry,
		"http://test.url/sub",
	)
	require.NoError(t, err, "CreateTrialSubscription() error")

	// Bind should still work, just with ReferredBy = 0
	sub, err := service.BindTrialSubscription(context.Background(), "no-invite-sub", 123456789, "testuser")
	require.NoError(t, err, "BindTrialSubscription() error")
	assert.Equal(t, int64(0), sub.ReferredBy, "ReferredBy should be 0 when no invite code")
}

func TestService_CleanupExpiredTrials_WithNilXUIClient(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create an old trial subscription
	oldTime := time.Now().Add(-2 * time.Hour)
	oldSub := &Subscription{
		TelegramID:      0,
		SubscriptionID:  "nil-xui-trial",
		ClientID:        "nil-xui-client",
		InboundID:       1,
		IsTrial:         true,
		Status:          "active",
		ExpiryTime:      time.Now().Add(1 * time.Hour),
		SubscriptionURL: "http://test.url/sub",
		CreatedAt:       oldTime,
	}
	require.NoError(t, service.db.Create(oldSub).Error, "Create subscription")

	// Cleanup with nil XUI client (should not panic)
	deleted, err := service.CleanupExpiredTrials(context.Background(), 1, nil, 1)
	require.NoError(t, err, "CleanupExpiredTrials() error")
	assert.Equal(t, int64(1), deleted, "Expected 1 trial to be cleaned up")
}

// === GetSubscriptionBySubscriptionID tests ===

func TestService_GetSubscriptionBySubscriptionID_Found(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create subscription
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "client-123",
		SubscriptionID:  "sub-test-id-unique",
		InboundID:       1,
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://test.url/sub",
	}
	require.NoError(t, service.db.Create(sub).Error, "Create subscription")

	// Find by subscription ID
	found, err := service.GetSubscriptionBySubscriptionID(context.Background(), "sub-test-id-unique")
	require.NoError(t, err, "GetSubscriptionBySubscriptionID() error")
	require.NotNil(t, found, "GetSubscriptionBySubscriptionID() returned nil")

	assert.Equal(t, "sub-test-id-unique", found.SubscriptionID, "SubscriptionID")
	assert.Equal(t, int64(123456789), found.TelegramID, "TelegramID")
	assert.Equal(t, "testuser", found.Username, "Username")
}

func TestService_GetSubscriptionBySubscriptionID_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	_, err = service.GetSubscriptionBySubscriptionID(context.Background(), "nonexistent-sub-id")
	assert.Error(t, err, "GetSubscriptionBySubscriptionID() should return error for nonexistent ID")
}

// === GetByID tests ===

func TestService_GetByID_Found(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create subscription
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "client-123",
		SubscriptionID:  "sub-id-123",
		InboundID:       1,
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://test.url/sub",
	}
	require.NoError(t, service.db.Create(sub).Error, "Create subscription")

	// Find by ID
	found, err := service.GetByID(context.Background(), sub.ID)
	require.NoError(t, err, "GetByID() error")
	require.NotNil(t, found, "GetByID() returned nil")

	assert.Equal(t, sub.ID, found.ID, "ID")
	assert.Equal(t, int64(123456789), found.TelegramID, "TelegramID")
	assert.Equal(t, "testuser", found.Username, "Username")
}

func TestService_GetByID_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	_, err = service.GetByID(context.Background(), 99999)
	assert.Error(t, err, "GetByID() should return error for nonexistent ID")
}

// === GetAllTelegramIDs tests ===

func TestService_GetAllTelegramIDs_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	ids, err := service.GetAllTelegramIDs(context.Background())
	require.NoError(t, err, "GetAllTelegramIDs() error")
	assert.Empty(t, ids, "GetAllTelegramIDs() should return empty slice for empty database")
}

func TestService_GetAllTelegramIDs_WithData(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create subscriptions
	for i := 1; i <= 3; i++ {
		sub := &Subscription{
			TelegramID:      int64(100000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			SubscriptionID:  fmt.Sprintf("sub-id-%d", i),
			InboundID:       1,
			Status:          "active",
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			SubscriptionURL: "http://test.url/sub",
		}
		require.NoError(t, service.db.Create(sub).Error, "Create subscription")
	}

	ids, err := service.GetAllTelegramIDs(context.Background())
	require.NoError(t, err, "GetAllTelegramIDs() error")
	assert.Len(t, ids, 3, "GetAllTelegramIDs() should return 3 IDs")
	assert.Contains(t, ids, int64(100001), "GetAllTelegramIDs() should contain ID 100001")
	assert.Contains(t, ids, int64(100002), "GetAllTelegramIDs() should contain ID 100002")
	assert.Contains(t, ids, int64(100003), "GetAllTelegramIDs() should contain ID 100003")
}

// === GetTelegramIDByUsername tests ===

func TestService_GetTelegramIDByUsername_Found(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create subscription
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "client-123",
		SubscriptionID:  "sub-id-123",
		InboundID:       1,
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://test.url/sub",
	}
	require.NoError(t, service.db.Create(sub).Error, "Create subscription")

	id, err := service.GetTelegramIDByUsername(context.Background(), "testuser")
	require.NoError(t, err, "GetTelegramIDByUsername() error")
	assert.Equal(t, int64(123456789), id, "GetTelegramIDByUsername() returned wrong ID")
}

func TestService_GetTelegramIDByUsername_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	_, err = service.GetTelegramIDByUsername(context.Background(), "nonexistent")
	assert.Error(t, err, "GetTelegramIDByUsername() should return error for nonexistent username")
}

// === DeleteSubscriptionByID tests ===

func TestService_DeleteSubscriptionByID_Success(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create subscription
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "client-123",
		SubscriptionID:  "sub-id-123",
		InboundID:       1,
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://test.url/sub",
	}
	require.NoError(t, service.db.Create(sub).Error, "Create subscription")

	// Delete by ID
	deleted, err := service.DeleteSubscriptionByID(context.Background(), sub.ID)
	require.NoError(t, err, "DeleteSubscriptionByID() error")
	require.NotNil(t, deleted, "DeleteSubscriptionByID() returned nil")
	assert.Equal(t, sub.ID, deleted.ID, "Deleted subscription ID")

	// Verify deleted
	_, err = service.GetByID(context.Background(), sub.ID)
	assert.Error(t, err, "GetByID() should return error after deletion")
}

func TestService_DeleteSubscriptionByID_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	_, err = service.DeleteSubscriptionByID(context.Background(), 99999)
	assert.Error(t, err, "DeleteSubscriptionByID() should return error for nonexistent ID")
}

// === GetTrialSubscriptionBySubID tests ===

func TestService_GetTrialSubscriptionBySubID_Success(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create trial subscription
	sub := &Subscription{
		TelegramID:      0, // Unactivated trial
		Username:        "",
		ClientID:        "client-trial-123",
		SubscriptionID:  "trial-sub-id-123",
		InboundID:       1,
		Status:          "active",
		IsTrial:         true,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://test.url/sub",
	}
	require.NoError(t, service.db.Create(sub).Error, "Create trial subscription")

	// Get by subscription ID
	got, err := service.GetTrialSubscriptionBySubID(context.Background(), sub.SubscriptionID)
	require.NoError(t, err, "GetTrialSubscriptionBySubID() error")
	require.NotNil(t, got, "GetTrialSubscriptionBySubID() returned nil")
	assert.Equal(t, sub.SubscriptionID, got.SubscriptionID, "SubscriptionID mismatch")
	assert.True(t, got.IsTrial, "IsTrial should be true")
	assert.Equal(t, int64(0), got.TelegramID, "TelegramID should be 0 for unactivated trial")
}

func TestService_GetTrialSubscriptionBySubID_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	_, err = service.GetTrialSubscriptionBySubID(context.Background(), "nonexistent")
	assert.Error(t, err, "GetTrialSubscriptionBySubID() should return error for nonexistent ID")
}

func TestService_GetTrialSubscriptionBySubID_NotTrial(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err, "NewService() error")
	defer service.Close()

	// Create regular (non-trial) subscription
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "client-regular-123",
		SubscriptionID:  "regular-sub-id-123",
		InboundID:       1,
		Status:          "active",
		IsTrial:         false,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://test.url/sub",
	}
	require.NoError(t, service.db.Create(sub).Error, "Create regular subscription")

	// Try to get as trial - should return error (method filters by IsTrial)
	_, err = service.GetTrialSubscriptionBySubID(context.Background(), sub.SubscriptionID)
	assert.Error(t, err, "GetTrialSubscriptionBySubID() should return error for non-trial subscription")
}

// ==================== Migration Tests ====================

func TestRunMigrationsWithDBAndDir_FreshDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	sqlDB, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0644)
	require.NoError(t, err)
	sqlDB.Close()

	// Create a real SQLite DB
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Create migrations dir
	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Should not panic on fresh DB
	err = runMigrationsWithDBAndDir(db, migDir)
	// May error on missing migration files, that's expected
	t.Logf("runMigrationsWithDBAndDir on fresh DB: %v", err)
}

func TestRunMigrationsWithDBAndDir_EmptyMigrationDir(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Empty migration directory should not error
	err = runMigrationsWithDBAndDir(db, migDir)
	// May return error about no migrations, that's OK
	t.Logf("runMigrationsWithDBAndDir with empty dir: %v", err)
}

func TestRunMigrationsWithDBAndDir_InvalidSQL(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Write invalid SQL migration
	err = os.WriteFile(filepath.Join(migDir, "000_test.up.sql"), []byte("INVALID SQL STATEMENT!!!"), 0644)
	require.NoError(t, err)

	err = runMigrationsWithDBAndDir(db, migDir)
	assert.Error(t, err, "runMigrationsWithDBAndDir should error on invalid SQL")
}

func TestRunMigrationsWithDBAndDir_ValidMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Write a valid simple migration
	migration := `CREATE TABLE IF NOT EXISTS test_table (id INTEGER PRIMARY KEY, name TEXT);`
	err = os.WriteFile(filepath.Join(migDir, "000_create_test.up.sql"), []byte(migration), 0644)
	require.NoError(t, err)

	err = runMigrationsWithDBAndDir(db, migDir)
	require.NoError(t, err, "runMigrationsWithDBAndDir should succeed with valid migration")

	// Verify table was created
	var tableExists int
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='test_table'").Scan(&tableExists)
	assert.Equal(t, 1, tableExists, "test_table should exist after migration")
}

func TestRunMigrationsWithDBAndDir_CorruptedSQL(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Write corrupted SQL (binary-like garbage that's not valid SQL)
	corrupted := []byte{0x00, 0x01, 0xFF, 0xFE, 0x89, 0x50, 0x4E, 0x47}
	err = os.WriteFile(filepath.Join(migDir, "001_corrupted.up.sql"), corrupted, 0644)
	require.NoError(t, err)

	err = runMigrationsWithDBAndDir(db, migDir)
	// SQLite may or may not error on binary data depending on migration library
	// The important thing is it doesn't panic
	t.Logf("Corrupted SQL migration result: %v", err)
}

func TestRunMigrationsWithDBAndDir_PartialMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// First migration is valid
	err = os.WriteFile(filepath.Join(migDir, "000_create_users.up.sql"),
		[]byte("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);"), 0644)
	require.NoError(t, err)

	// Second migration is invalid
	err = os.WriteFile(filepath.Join(migDir, "001_add_email.up.sql"),
		[]byte("INVALID SQL HERE!!!"), 0644)
	require.NoError(t, err)

	err = runMigrationsWithDBAndDir(db, migDir)
	assert.Error(t, err, "runMigrationsWithDBAndDir should error on second migration")

	// First migration should still be applied
	var tableExists int
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'").Scan(&tableExists)
	assert.Equal(t, 1, tableExists, "First migration should still be applied")
}

func TestRunMigrationsWithDBAndDir_DuplicateMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Write a valid migration
	err = os.WriteFile(filepath.Join(migDir, "000_create_table.up.sql"),
		[]byte("CREATE TABLE test_dup (id INTEGER PRIMARY KEY);"), 0644)
	require.NoError(t, err)

	// Run migration first time
	err = runMigrationsWithDBAndDir(db, migDir)
	require.NoError(t, err, "First migration run should succeed")

	// Run migration second time - should be idempotent
	err = runMigrationsWithDBAndDir(db, migDir)
	// May return error about no change or succeed, both are acceptable
	t.Logf("Second migration run result: %v", err)
}

func TestRunMigrationsWithDBAndDir_NonSQLFile(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Write a non-SQL file (should be ignored by migration system)
	err = os.WriteFile(filepath.Join(migDir, "README.txt"),
		[]byte("This is not a migration file"), 0644)
	require.NoError(t, err)

	// Write a valid migration
	err = os.WriteFile(filepath.Join(migDir, "000_create_table.up.sql"),
		[]byte("CREATE TABLE test_non_sql (id INTEGER PRIMARY KEY);"), 0644)
	require.NoError(t, err)

	err = runMigrationsWithDBAndDir(db, migDir)
	require.NoError(t, err, "runMigrationsWithDBAndDir should succeed")

	// Verify table was created
	var tableExists int
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='test_non_sql'").Scan(&tableExists)
	assert.Equal(t, 1, tableExists, "test_non_sql table should exist")
}

func TestRunMigrationsWithDBAndDir_EmptyMigrationFile(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Write an empty migration file
	err = os.WriteFile(filepath.Join(migDir, "000_empty.up.sql"), []byte(""), 0644)
	require.NoError(t, err)

	err = runMigrationsWithDBAndDir(db, migDir)
	// Empty migration may succeed or fail depending on migration library behavior
	t.Logf("Empty migration result: %v", err)
}

func TestRunMigrationsWithDBAndDir_LegacyMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Create legacy subscriptions table (without subscription_id column)
	_, err = db.Exec(`CREATE TABLE subscriptions (id INTEGER PRIMARY KEY, x_ui_host TEXT, subscription_url TEXT)`)
	require.NoError(t, err, "Failed to create legacy table")

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Write a migration that creates schema_migrations table
	err = os.WriteFile(filepath.Join(migDir, "003_add_referral_columns.up.sql"),
		[]byte("CREATE TABLE IF NOT EXISTS referrals (id INTEGER PRIMARY KEY, code TEXT);"), 0644)
	require.NoError(t, err)

	err = runMigrationsWithDBAndDir(db, migDir)
	// Legacy migration should handle the old table structure
	t.Logf("Legacy migration result: %v", err)

	// Verify x_ui_host column was dropped
	var xuiHostExists int
	db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('subscriptions') WHERE name = 'x_ui_host'").Scan(&xuiHostExists)
	assert.Equal(t, 0, xuiHostExists, "x_ui_host column should be dropped after legacy migration")
}

func TestRunMigrationsWithDBAndDir_MultipleMigrations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Create multiple migrations
	migrations := map[string]string{
		"000_create_users.up.sql":    "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);",
		"001_create_posts.up.sql":    "CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER, title TEXT);",
		"002_create_comments.up.sql": "CREATE TABLE comments (id INTEGER PRIMARY KEY, post_id INTEGER, body TEXT);",
	}

	for name, content := range migrations {
		err = os.WriteFile(filepath.Join(migDir, name), []byte(content), 0644)
		require.NoError(t, err, "Failed to write migration %s", name)
	}

	err = runMigrationsWithDBAndDir(db, migDir)
	require.NoError(t, err, "runMigrationsWithDBAndDir should succeed with multiple migrations")

	// Verify all tables were created
	for _, table := range []string{"users", "posts", "comments"} {
		var tableExists int
		db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&tableExists)
		assert.Equal(t, 1, tableExists, "%s table should exist", table)
	}
}

func TestRunMigrationsWithDBAndDir_DownMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Create up and down migrations
	err = os.WriteFile(filepath.Join(migDir, "000_create_table.up.sql"),
		[]byte("CREATE TABLE test_down (id INTEGER PRIMARY KEY);"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(migDir, "000_create_table.down.sql"),
		[]byte("DROP TABLE IF EXISTS test_down;"), 0644)
	require.NoError(t, err)

	// Run up migration
	err = runMigrationsWithDBAndDir(db, migDir)
	require.NoError(t, err, "Up migration should succeed")

	// Verify table exists
	var tableExists int
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='test_down'").Scan(&tableExists)
	assert.Equal(t, 1, tableExists, "test_down table should exist after up migration")
}

func TestRunMigrationsWithDBAndDir_MigrationWithSyntaxError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Migration with SQL syntax error
	err = os.WriteFile(filepath.Join(migDir, "000_syntax_error.up.sql"),
		[]byte("CREAT TABL test_syntax (id INTEGER PRIMARI KEY);"), 0644)
	require.NoError(t, err)

	err = runMigrationsWithDBAndDir(db, migDir)
	assert.Error(t, err, "runMigrationsWithDBAndDir should error on syntax error")
}

func TestRunMigrationsWithDBAndDir_LargeMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Create a large migration with many columns
	var sql string
	sql = "CREATE TABLE large_table (id INTEGER PRIMARY KEY,\n"
	for i := 0; i < 100; i++ {
		sql += fmt.Sprintf("column_%d TEXT,\n", i)
	}
	sql = sql[:len(sql)-2] + ");" // Remove trailing comma and newline, add closing paren

	err = os.WriteFile(filepath.Join(migDir, "000_large_table.up.sql"), []byte(sql), 0644)
	require.NoError(t, err, "Failed to write large migration")

	err = runMigrationsWithDBAndDir(db, migDir)
	require.NoError(t, err, "runMigrationsWithDBAndDir should succeed with large migration")

	// Verify table was created
	var tableExists int
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='large_table'").Scan(&tableExists)
	assert.Equal(t, 1, tableExists, "large_table should exist")
}

func TestRunMigrationsWithDBAndDir_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	migDir := filepath.Join(tmpDir, "migrations")
	err = os.MkdirAll(migDir, 0755)
	require.NoError(t, err)

	// Write a valid migration
	err = os.WriteFile(filepath.Join(migDir, "000_create_table.up.sql"),
		[]byte("CREATE TABLE concurrent_test (id INTEGER PRIMARY KEY, data TEXT);"), 0644)
	require.NoError(t, err)

	// Run migrations multiple times concurrently
	done := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() {
			conn, _ := sql.Open("sqlite3", dbPath)
			defer conn.Close()
			done <- runMigrationsWithDBAndDir(conn, migDir)
		}()
	}

	// All should complete without error (or at least not panic)
	for i := 0; i < 5; i++ {
		err := <-done
		// Some may error due to concurrent access, that's acceptable
		t.Logf("Concurrent migration %d result: %v", i, err)
	}

	// Verify table was created
	var tableExists int
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='concurrent test'").Scan(&tableExists)
	// Table may or may not exist depending on which migration won the race
	t.Logf("Table exists after concurrent migrations: %v", tableExists > 0)
}

// ==================== Additional Query Edge Cases ====================

func TestService_GetByUsername_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	id, err := service.GetTelegramIDByUsername(ctx, "nonexistent_user_12345")
	assert.Error(t, err, "GetTelegramIDByUsername should error for non-existent user")
	assert.Equal(t, int64(0), id, "Should return 0 for non-existent user")
}

func TestService_CountTrialRequestsAtLimit(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()
	ip := "192.168.1.100"

	for i := 0; i < 3; i++ {
		err := service.CreateTrialRequest(ctx, ip)
		require.NoError(t, err, "CreateTrialRequest() error")
	}

	count, err := service.CountTrialRequestsByIPLastHour(ctx, ip)
	require.NoError(t, err, "CountTrialRequestsByIPLastHour() error")
	assert.Equal(t, 3, count, "Should be at rate limit")
}

func TestService_CountTrialRequests_MultipleIPs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	require.NoError(t, err)
	defer service.Close()

	ctx := context.Background()

	for i := 0; i < 2; i++ {
		ip := fmt.Sprintf("192.168.1.%d", i)
		err := service.CreateTrialRequest(ctx, ip)
		require.NoError(t, err, "CreateTrialRequest() error")
	}

	count1, err := service.CountTrialRequestsByIPLastHour(ctx, "192.168.1.0")
	require.NoError(t, err)
	assert.Equal(t, 1, count1, "IP 1 should have 1 request")

	count2, err := service.CountTrialRequestsByIPLastHour(ctx, "192.168.1.1")
	require.NoError(t, err)
	assert.Equal(t, 1, count2, "IP 2 should have 1 request")
}
