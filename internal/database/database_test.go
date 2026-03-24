package database

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rs8kvn_bot/internal/logger"
)

func init() {
	// Initialize logger for tests
	_, _ = logger.Init("", "error")
}

func TestInit(t *testing.T) {
	// Create temporary directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Test initialization
	err := Init(dbPath)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Verify database is initialized
	if DB == nil {
		t.Fatal("DB is nil after Init()")
	}

	// Verify table exists
	if !DB.Migrator().HasTable(&Subscription{}) {
		t.Fatal("Subscriptions table not created")
	}

	// Clean up
	Close()
}

func TestInit_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "test.db")

	err := Init(dbPath)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(filepath.Dir(dbPath)); os.IsNotExist(err) {
		t.Fatal("Init() did not create parent directory")
	}

	Close()
}

func TestInit_InvalidPath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "file.txt")

	if err := os.WriteFile(dbPath, []byte("file"), 0644); err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	err := Init(dbPath)
	if err == nil {
		t.Fatal("Init() should error when parent path is a file")
	}
}

func TestClose(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize database
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Test closing
	err := Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Verify sqlDB is nil after close
	if sqlDB != nil {
		t.Fatal("sqlDB should be nil after Close()")
	}
}

func TestGetByTelegramID(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create test subscription
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/test",
	}

	if err := DB.Create(sub).Error; err != nil {
		t.Fatalf("Failed to create test subscription: %v", err)
	}

	// Test GetByTelegramID
	got, err := GetByTelegramID(123456789)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}

	if got.TelegramID != sub.TelegramID {
		t.Errorf("TelegramID = %v, want %v", got.TelegramID, sub.TelegramID)
	}
	if got.Username != sub.Username {
		t.Errorf("Username = %v, want %v", got.Username, sub.Username)
	}
	if got.Status != "active" {
		t.Errorf("Status = %v, want active", got.Status)
	}
}

func TestGetByTelegramID_NotFound(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Test GetByTelegramID with non-existent ID
	_, err := GetByTelegramID(999999999)
	if err == nil {
		t.Fatal("GetByTelegramID() should return error for non-existent ID")
	}
}

func TestGetByTelegramID_ReturnsActiveOnly(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
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
	if err := DB.Create(revokedSub).Error; err != nil {
		t.Fatalf("Failed to create revoked subscription: %v", err)
	}

	// Create active subscription
	activeSub := &Subscription{
		TelegramID:      telegramID,
		Username:        "testuser",
		ClientID:        "active-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/active",
	}
	if err := DB.Create(activeSub).Error; err != nil {
		t.Fatalf("Failed to create active subscription: %v", err)
	}

	// Test GetByTelegramID returns only active
	got, err := GetByTelegramID(telegramID)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}

	if got.Status != "active" {
		t.Errorf("Status = %v, want active", got.Status)
	}
	if got.ClientID != "active-client-id" {
		t.Errorf("ClientID = %v, want active-client-id", got.ClientID)
	}
}

func TestCreateSubscription(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create subscription
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/test",
	}

	err := CreateSubscription(sub)
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	// Verify subscription was created
	var count int64
	DB.Model(&Subscription{}).Where("telegram_id = ?", sub.TelegramID).Count(&count)
	if count != 1 {
		t.Errorf("Expected 1 subscription, got %d", count)
	}
}

func TestCreateSubscription_RevokesOldSubscription(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
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
	if err := DB.Create(oldSub).Error; err != nil {
		t.Fatalf("Failed to create old subscription: %v", err)
	}

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
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	// Verify old subscription was revoked
	var oldSubCheck Subscription
	if err := DB.Where("client_id = ?", "old-client-id").First(&oldSubCheck).Error; err != nil {
		t.Fatalf("Failed to find old subscription: %v", err)
	}
	if oldSubCheck.Status != "revoked" {
		t.Errorf("Old subscription status = %v, want revoked", oldSubCheck.Status)
	}

	// Verify new subscription is active
	var newSubCheck Subscription
	if err := DB.Where("client_id = ?", "new-client-id").First(&newSubCheck).Error; err != nil {
		t.Fatalf("Failed to find new subscription: %v", err)
	}
	if newSubCheck.Status != "active" {
		t.Errorf("New subscription status = %v, want active", newSubCheck.Status)
	}
}

func TestUpdateSubscription(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
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
	if err := DB.Create(sub).Error; err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	// Update subscription
	sub.Status = "revoked"
	err := UpdateSubscription(sub)
	if err != nil {
		t.Fatalf("UpdateSubscription() error = %v", err)
	}

	// Verify update
	var updated Subscription
	if err := DB.First(&updated, sub.ID).Error; err != nil {
		t.Fatalf("Failed to find subscription: %v", err)
	}
	if updated.Status != "revoked" {
		t.Errorf("Status = %v, want revoked", updated.Status)
	}
}

func TestSubscription_Timestamps(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
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
	if err := DB.Create(sub).Error; err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}
	after := time.Now()

	// Verify CreatedAt is set
	if sub.CreatedAt.Before(before) || sub.CreatedAt.After(after) {
		t.Errorf("CreatedAt = %v, should be between %v and %v", sub.CreatedAt, before, after)
	}

	// Verify UpdatedAt is set
	if sub.UpdatedAt.Before(before) || sub.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt = %v, should be between %v and %v", sub.UpdatedAt, before, after)
	}
}

func TestGetByTelegramID_DatabaseNotInitialized(t *testing.T) {
	// Ensure DB is nil
	DB = nil

	_, err := GetByTelegramID(123456789)
	if err == nil {
		t.Fatal("GetByTelegramID() should return error when database not initialized")
	}
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
	if err == nil {
		t.Fatal("CreateSubscription() should return error when database not initialized")
	}
}

func TestCreateSubscription_TransactionError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
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

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// First close
	if err := Close(); err != nil {
		t.Fatalf("First Close() error = %v", err)
	}

	// Second close should not panic
	if err := Close(); err != nil {
		t.Fatalf("Second Close() error = %v", err)
	}

	// Third close
	if err := Close(); err != nil {
		t.Fatalf("Third Close() error = %v", err)
	}
}

func TestSubscription_AllFields(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	now := time.Now()
	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      now.Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/test",
	}

	if err := DB.Create(sub).Error; err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	// Verify all fields are saved correctly
	var retrieved Subscription
	if err := DB.First(&retrieved, sub.ID).Error; err != nil {
		t.Fatalf("Failed to retrieve subscription: %v", err)
	}

	if retrieved.TelegramID != sub.TelegramID {
		t.Errorf("TelegramID = %v, want %v", retrieved.TelegramID, sub.TelegramID)
	}
	if retrieved.Username != sub.Username {
		t.Errorf("Username = %v, want %v", retrieved.Username, sub.Username)
	}
	if retrieved.ClientID != sub.ClientID {
		t.Errorf("ClientID = %v, want %v", retrieved.ClientID, sub.ClientID)
	}
	if retrieved.XUIHost != sub.XUIHost {
		t.Errorf("XUIHost = %v, want %v", retrieved.XUIHost, sub.XUIHost)
	}
	if retrieved.InboundID != sub.InboundID {
		t.Errorf("InboundID = %v, want %v", retrieved.InboundID, sub.InboundID)
	}
	if retrieved.TrafficLimit != sub.TrafficLimit {
		t.Errorf("TrafficLimit = %v, want %v", retrieved.TrafficLimit, sub.TrafficLimit)
	}
	if retrieved.Status != sub.Status {
		t.Errorf("Status = %v, want %v", retrieved.Status, sub.Status)
	}
	if retrieved.SubscriptionURL != sub.SubscriptionURL {
		t.Errorf("SubscriptionURL = %v, want %v", retrieved.SubscriptionURL, sub.SubscriptionURL)
	}
}

func TestGetByTelegramID_MultipleUsers(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
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
		if err := DB.Create(sub).Error; err != nil {
			t.Fatalf("Failed to create subscription: %v", err)
		}
	}

	// Verify each user gets their own subscription
	for _, u := range users {
		got, err := GetByTelegramID(u.telegramID)
		if err != nil {
			t.Fatalf("GetByTelegramID(%d) error = %v", u.telegramID, err)
		}
		if got.Username != u.username {
			t.Errorf("GetByTelegramID(%d) username = %s, want %s", u.telegramID, got.Username, u.username)
		}
	}
}

func TestCreateSubscription_MultipleRevokes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
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
		}

		err := CreateSubscription(sub)
		if err != nil {
			t.Fatalf("CreateSubscription() iteration %d error = %v", i, err)
		}

		// Small delay to ensure different timestamps
		time.Sleep(10 * time.Millisecond)
	}

	// Verify only one active subscription exists
	var activeCount int64
	DB.Model(&Subscription{}).Where("telegram_id = ? AND status = ?", telegramID, "active").Count(&activeCount)
	if activeCount != 1 {
		t.Errorf("Active subscription count = %d, want 1", activeCount)
	}

	// Verify two revoked subscriptions exist
	var revokedCount int64
	DB.Model(&Subscription{}).Where("telegram_id = ? AND status = ?", telegramID, "revoked").Count(&revokedCount)
	if revokedCount != 2 {
		t.Errorf("Revoked subscription count = %d, want 2", revokedCount)
	}
}

func TestSubscription_SoftDelete(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	sub := &Subscription{
		TelegramID:      123456789,
		Username:        "testuser",
		ClientID:        "test-client-id",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/test",
	}

	if err := DB.Create(sub).Error; err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	// Soft delete
	if err := DB.Delete(sub).Error; err != nil {
		t.Fatalf("Failed to soft delete subscription: %v", err)
	}

	// Verify DeletedAt is set
	var deletedSub Subscription
	err := DB.Unscoped().First(&deletedSub, sub.ID).Error
	if err != nil {
		t.Fatalf("Failed to find deleted subscription: %v", err)
	}

	if deletedSub.DeletedAt.Valid == false {
		t.Error("DeletedAt should be set after soft delete")
	}

	// Normal query should not find the deleted subscription
	var normalSub Subscription
	err = DB.First(&normalSub, sub.ID).Error
	if err == nil {
		t.Error("Soft deleted subscription should not be found in normal query")
	}
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
			if got := sub.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
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
			if got := sub.IsActive(); got != tt.want {
				t.Errorf("IsActive() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ==================== DeleteSubscription Tests ====================

func TestDeleteSubscription(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create a subscription
	sub := &Subscription{
		TelegramID:      12345,
		Username:        "testuser",
		ClientID:        "client-123",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/abc",
	}

	err := CreateSubscription(sub)
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	// Delete the subscription
	err = DeleteSubscription(sub.TelegramID)
	if err != nil {
		t.Fatalf("DeleteSubscription() error = %v", err)
	}

	// Verify it's soft deleted
	var deletedSub Subscription
	err = DB.Unscoped().Where("telegram_id = ?", sub.TelegramID).First(&deletedSub).Error
	if err != nil {
		t.Fatalf("Failed to find deleted subscription: %v", err)
	}

	if !deletedSub.DeletedAt.Valid {
		t.Error("Subscription should be soft deleted")
	}
}

func TestDeleteSubscription_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Try to delete non-existent subscription
	err := DeleteSubscription(999999999)
	// Should not error, just soft delete nothing
	if err != nil {
		t.Errorf("DeleteSubscription() error = %v", err)
	}
}

func TestDeleteSubscription_DatabaseNotInitialized(t *testing.T) {
	// Close database if open
	Close()

	err := DeleteSubscription(12345)
	if err == nil {
		t.Error("DeleteSubscription() should return error when database not initialized")
	}
}

// ==================== Service Tests ====================

func TestNewService(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if service == nil {
		t.Fatal("NewService() returned nil service")
	}
	if service.db == nil {
		t.Fatal("Service.db is nil")
	}

	// Clean up
	service.Close()
}

func TestNewService_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(filepath.Dir(dbPath)); os.IsNotExist(err) {
		t.Fatal("NewService() did not create parent directory")
	}

	service.Close()
}

func TestService_Close(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	// Close should not error
	err = service.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestService_Close_AlreadyClosed(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	service.Close()

	err = service.Close()
	if err != nil {
		t.Errorf("Second Close() error = %v", err)
	}
}

func TestNewService_InvalidPath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "file.txt")

	if err := os.WriteFile(dbPath, []byte("file"), 0644); err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	_, err := NewService(dbPath)
	if err == nil {
		t.Fatal("NewService() should error when parent path is a file")
	}
}

func TestService_GetByTelegramID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Create test subscription
	sub := &Subscription{
		TelegramID:      12345,
		Username:        "testuser",
		ClientID:        "client-123",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/abc",
	}

	err = service.CreateSubscription(nil, sub)
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	// Retrieve the subscription
	retrieved, err := service.GetByTelegramID(nil, 12345)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}

	if retrieved.TelegramID != 12345 {
		t.Errorf("TelegramID = %d, want 12345", retrieved.TelegramID)
	}
	if retrieved.Username != "testuser" {
		t.Errorf("Username = %s, want testuser", retrieved.Username)
	}
}

func TestService_CreateSubscription(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	sub := &Subscription{
		TelegramID:      54321,
		Username:        "newuser",
		ClientID:        "client-456",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/xyz",
	}

	err = service.CreateSubscription(nil, sub)
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	// Verify it was created
	retrieved, err := service.GetByTelegramID(nil, 54321)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}

	if retrieved.ClientID != "client-456" {
		t.Errorf("ClientID = %s, want client-456", retrieved.ClientID)
	}
}

func TestService_UpdateSubscription(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Create subscription
	sub := &Subscription{
		TelegramID:      99999,
		Username:        "updateuser",
		ClientID:        "client-789",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/update",
	}

	err = service.CreateSubscription(nil, sub)
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	// Update subscription
	sub.Username = "updateduser"
	sub.TrafficLimit = 214748364800

	err = service.UpdateSubscription(nil, sub)
	if err != nil {
		t.Fatalf("UpdateSubscription() error = %v", err)
	}

	// Verify update
	retrieved, err := service.GetByTelegramID(nil, 99999)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}

	if retrieved.Username != "updateduser" {
		t.Errorf("Username = %s, want updateduser", retrieved.Username)
	}
	if retrieved.TrafficLimit != 214748364800 {
		t.Errorf("TrafficLimit = %d, want 214748364800", retrieved.TrafficLimit)
	}
}

func TestService_DeleteSubscription(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Create subscription
	sub := &Subscription{
		TelegramID:      77777,
		Username:        "deleteuser",
		ClientID:        "client-delete",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/delete",
	}

	err = service.CreateSubscription(nil, sub)
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	// Delete subscription
	err = service.DeleteSubscription(nil, 77777)
	if err != nil {
		t.Fatalf("DeleteSubscription() error = %v", err)
	}

	// Verify it's deleted
	_, err = service.GetByTelegramID(nil, 77777)
	if err == nil {
		t.Error("GetByTelegramID() should return error for deleted subscription")
	}
}

func TestService_GetAllSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Create multiple subscriptions
	for i := 0; i < 5; i++ {
		sub := &Subscription{
			TelegramID:      int64(10000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://test.url/sub/%d", i),
		}
		err = service.CreateSubscription(nil, sub)
		if err != nil {
			t.Fatalf("CreateSubscription() error = %v", err)
		}
	}

	// Get all subscriptions
	subs, err := service.GetAllSubscriptions(nil)
	if err != nil {
		t.Fatalf("GetAllSubscriptions() error = %v", err)
	}

	if len(subs) != 5 {
		t.Errorf("GetAllSubscriptions() returned %d subscriptions, want 5", len(subs))
	}
}

func TestService_CountActiveSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Create active subscriptions
	for i := 0; i < 3; i++ {
		sub := &Subscription{
			TelegramID:      int64(20000 + i),
			Username:        fmt.Sprintf("active%d", i),
			ClientID:        fmt.Sprintf("client-active-%d", i),
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour), // Future
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://test.url/sub/active/%d", i),
		}
		err = service.CreateSubscription(nil, sub)
		if err != nil {
			t.Fatalf("CreateSubscription() error = %v", err)
		}
	}

	// Create expired subscription
	expiredSub := &Subscription{
		TelegramID:      29999,
		Username:        "expired",
		ClientID:        "client-expired",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(-1 * time.Hour), // Past
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/expired",
	}
	err = service.CreateSubscription(nil, expiredSub)
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	// Count active subscriptions
	count, err := service.CountActiveSubscriptions(nil)
	if err != nil {
		t.Fatalf("CountActiveSubscriptions() error = %v", err)
	}

	if count != 3 {
		t.Errorf("CountActiveSubscriptions() = %d, want 3", count)
	}
}

func TestService_CountExpiredSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Create expired subscriptions
	for i := 0; i < 2; i++ {
		sub := &Subscription{
			TelegramID:      int64(30000 + i),
			Username:        fmt.Sprintf("expired%d", i),
			ClientID:        fmt.Sprintf("client-expired-%d", i),
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(-1 * time.Hour), // Past
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://test.url/sub/expired/%d", i),
		}
		err = service.CreateSubscription(nil, sub)
		if err != nil {
			t.Fatalf("CreateSubscription() error = %v", err)
		}
	}

	// Create active subscription
	activeSub := &Subscription{
		TelegramID:      39999,
		Username:        "active",
		ClientID:        "client-active",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(1 * time.Hour), // Future
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/active",
	}
	err = service.CreateSubscription(nil, activeSub)
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	// Count expired subscriptions
	count, err := service.CountExpiredSubscriptions(nil)
	if err != nil {
		t.Fatalf("CountExpiredSubscriptions() error = %v", err)
	}

	if count != 2 {
		t.Errorf("CountExpiredSubscriptions() = %d, want 2", count)
	}
}

func TestGetLatestSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create test subscriptions with different creation times
	for i := 0; i < 15; i++ {
		time.Sleep(time.Millisecond * 10) // Ensure different timestamps
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
		}
		if err := CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create test subscription: %v", err)
		}
	}

	// Get latest 10 subscriptions
	subs, err := GetLatestSubscriptions(10)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions() error = %v", err)
	}

	if len(subs) != 10 {
		t.Errorf("GetLatestSubscriptions() returned %d subscriptions, want 10", len(subs))
	}

	// Verify they are ordered by created_at DESC (newest first)
	for i := 0; i < len(subs)-1; i++ {
		if subs[i].CreatedAt.Before(subs[i+1].CreatedAt) {
			t.Errorf("Subscriptions not ordered by created_at DESC: %v before %v",
				subs[i].CreatedAt, subs[i+1].CreatedAt)
		}
	}

	// Verify the first one is the most recent (user14)
	if subs[0].Username != "user14" {
		t.Errorf("First subscription username = %s, want user14", subs[0].Username)
	}
}

func TestGetLatestSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// No subscriptions in database
	subs, err := GetLatestSubscriptions(10)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions() error = %v", err)
	}

	if len(subs) != 0 {
		t.Errorf("GetLatestSubscriptions() returned %d subscriptions, want 0", len(subs))
	}
}

func TestGetLatestSubscriptions_OnlyActive(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create active subscription
	activeSub := &Subscription{
		TelegramID:      100000001,
		Username:        "active_user",
		ClientID:        "client-active",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://localhost/sub/active",
	}
	if err := CreateSubscription(activeSub); err != nil {
		t.Fatalf("Failed to create active subscription: %v", err)
	}

	// Create revoked subscription
	revokedSub := &Subscription{
		TelegramID:      100000002,
		Username:        "revoked_user",
		ClientID:        "client-revoked",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "revoked",
		SubscriptionURL: "http://localhost/sub/revoked",
	}
	if err := DB.Create(revokedSub).Error; err != nil {
		t.Fatalf("Failed to create revoked subscription: %v", err)
	}

	// Get latest subscriptions
	subs, err := GetLatestSubscriptions(10)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions() error = %v", err)
	}

	if len(subs) != 1 {
		t.Errorf("GetLatestSubscriptions() returned %d subscriptions, want 1", len(subs))
	}

	if len(subs) > 0 && subs[0].Username != "active_user" {
		t.Errorf("Username = %s, want active_user", subs[0].Username)
	}
}

func TestService_GetLatestSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Create test subscriptions
	for i := 0; i < 5; i++ {
		time.Sleep(time.Millisecond * 10)
		sub := &Subscription{
			TelegramID:      int64(200000000 + i),
			Username:        fmt.Sprintf("service_user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
		}
		if err := service.CreateSubscription(nil, sub); err != nil {
			t.Fatalf("Failed to create test subscription: %v", err)
		}
	}

	// Get latest 3 subscriptions
	subs, err := service.GetLatestSubscriptions(nil, 3)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions() error = %v", err)
	}

	if len(subs) != 3 {
		t.Errorf("GetLatestSubscriptions() returned %d subscriptions, want 3", len(subs))
	}

	// Verify the first one is the most recent
	if subs[0].Username != "service_user4" {
		t.Errorf("First subscription username = %s, want service_user4", subs[0].Username)
	}
}

func TestGetLatestSubscriptions_DatabaseNotInitialized(t *testing.T) {
	// Close any existing connection
	Close()

	_, err := GetLatestSubscriptions(10)
	if err == nil {
		t.Error("GetLatestSubscriptions() expected error when database not initialized, got nil")
	}
}

func TestGetLatestSubscriptions_LimitZero(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create test subscriptions
	for i := 0; i < 5; i++ {
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
		}
		if err := CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create test subscription: %v", err)
		}
	}

	// Get with limit 0
	subs, err := GetLatestSubscriptions(0)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions(0) error = %v", err)
	}

	if len(subs) != 0 {
		t.Errorf("GetLatestSubscriptions(0) returned %d subscriptions, want 0", len(subs))
	}
}

func TestGetLatestSubscriptions_LimitOne(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create test subscriptions
	for i := 0; i < 5; i++ {
		time.Sleep(time.Millisecond * 10)
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
		}
		if err := CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create test subscription: %v", err)
		}
	}

	// Get with limit 1
	subs, err := GetLatestSubscriptions(1)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions(1) error = %v", err)
	}

	if len(subs) != 1 {
		t.Errorf("GetLatestSubscriptions(1) returned %d subscriptions, want 1", len(subs))
	}

	// Should be the most recent (user4)
	if subs[0].Username != "user4" {
		t.Errorf("Username = %s, want user4", subs[0].Username)
	}
}

func TestGetLatestSubscriptions_LimitGreaterThanAvailable(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create 3 test subscriptions
	for i := 0; i < 3; i++ {
		time.Sleep(time.Millisecond * 10)
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
		}
		if err := CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create test subscription: %v", err)
		}
	}

	// Request 10 but only 3 exist
	subs, err := GetLatestSubscriptions(10)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions(10) error = %v", err)
	}

	if len(subs) != 3 {
		t.Errorf("GetLatestSubscriptions(10) returned %d subscriptions, want 3", len(subs))
	}
}

func TestGetLatestSubscriptions_SpecialCharacters(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
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
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
		}
		if err := CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create test subscription: %v", err)
		}
		time.Sleep(time.Millisecond * 10)
	}

	subs, err := GetLatestSubscriptions(10)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions() error = %v", err)
	}

	if len(subs) != len(specialUsernames) {
		t.Errorf("Expected %d subscriptions, got %d", len(specialUsernames), len(subs))
	}

	// Verify all usernames are preserved correctly
	foundUsernames := make(map[string]bool)
	for _, sub := range subs {
		foundUsernames[sub.Username] = true
	}

	for _, username := range specialUsernames {
		if !foundUsernames[username] {
			t.Errorf("Username %s not found in results", username)
		}
	}
}

func TestGetLatestSubscriptions_OrderingConsistency(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create subscriptions with specific timestamps
	baseTime := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("ordered_user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
			CreatedAt:       baseTime.Add(time.Duration(i) * time.Hour),
		}
		// Use DB.Create to preserve CreatedAt
		if err := DB.Create(sub).Error; err != nil {
			t.Fatalf("Failed to create test subscription: %v", err)
		}
	}

	// Get subscriptions
	subs, err := GetLatestSubscriptions(10)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions() error = %v", err)
	}

	// Verify ordering (newest first: ordered_user4, ordered_user3, ...)
	expectedOrder := []string{"ordered_user4", "ordered_user3", "ordered_user2", "ordered_user1", "ordered_user0"}

	for i, expected := range expectedOrder {
		if i >= len(subs) {
			break
		}
		if subs[i].Username != expected {
			t.Errorf("Position %d: got %s, want %s", i, subs[i].Username, expected)
		}
	}
}

func TestGetLatestSubscriptions_MixedStatuses(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create subscriptions with different statuses
	statuses := []string{"active", "revoked", "expired", "active", "active"}

	for i, status := range statuses {
		sub := &Subscription{
			TelegramID:      int64(100000000 + i),
			Username:        fmt.Sprintf("status_user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost:2053",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          status,
			SubscriptionURL: fmt.Sprintf("http://localhost/sub/%d", i),
		}
		if status == "active" {
			if err := CreateSubscription(sub); err != nil {
				t.Fatalf("Failed to create test subscription: %v", err)
			}
		} else {
			// Direct DB create for non-active statuses
			if err := DB.Create(sub).Error; err != nil {
				t.Fatalf("Failed to create test subscription: %v", err)
			}
		}
		time.Sleep(time.Millisecond * 10)
	}

	// Should only get active subscriptions
	subs, err := GetLatestSubscriptions(10)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions() error = %v", err)
	}

	// Count expected active subscriptions (3 active in the list)
	expectedActive := 0
	for _, status := range statuses {
		if status == "active" {
			expectedActive++
		}
	}

	if len(subs) != expectedActive {
		t.Errorf("Expected %d active subscriptions, got %d", expectedActive, len(subs))
	}

	// Verify all returned subscriptions are active
	for _, sub := range subs {
		if sub.Status != "active" {
			t.Errorf("Got subscription with status %s, want active", sub.Status)
		}
	}
}

// ==================== GetSubscriptionByID Tests ====================

func TestGetSubscriptionByID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create a subscription
	sub := &Subscription{
		TelegramID:      12345,
		Username:        "testuser",
		ClientID:        "client-123",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/abc",
	}

	err := CreateSubscription(sub)
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	// Get the subscription by ID
	got, err := GetSubscriptionByID(sub.ID)
	if err != nil {
		t.Fatalf("GetSubscriptionByID() error = %v", err)
	}

	if got.ID != sub.ID {
		t.Errorf("GetSubscriptionByID() ID = %d, want %d", got.ID, sub.ID)
	}
	if got.TelegramID != sub.TelegramID {
		t.Errorf("GetSubscriptionByID() TelegramID = %d, want %d", got.TelegramID, sub.TelegramID)
	}
	if got.Username != sub.Username {
		t.Errorf("GetSubscriptionByID() Username = %s, want %s", got.Username, sub.Username)
	}
	if got.ClientID != sub.ClientID {
		t.Errorf("GetSubscriptionByID() ClientID = %s, want %s", got.ClientID, sub.ClientID)
	}
}

func TestGetSubscriptionByID_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Try to get non-existent subscription
	_, err := GetSubscriptionByID(99999)
	if err == nil {
		t.Error("GetSubscriptionByID() should return error for non-existent ID")
	}
}

func TestGetSubscriptionByID_DatabaseNotInitialized(t *testing.T) {
	// Close database if open
	Close()

	_, err := GetSubscriptionByID(1)
	if err == nil {
		t.Error("GetSubscriptionByID() should return error when database not initialized")
	}
}

// ==================== DeleteSubscriptionByID Tests ====================

func TestDeleteSubscriptionByID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create a subscription
	sub := &Subscription{
		TelegramID:      54321,
		Username:        "deleteuser",
		ClientID:        "client-delete",
		XUIHost:         "http://localhost:2053",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/delete",
	}

	err := CreateSubscription(sub)
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	id := sub.ID

	// Delete the subscription by ID
	deleted, err := DeleteSubscriptionByID(id)
	if err != nil {
		t.Fatalf("DeleteSubscriptionByID() error = %v", err)
	}

	// Verify returned subscription has correct data
	if deleted.ID != id {
		t.Errorf("DeleteSubscriptionByID() returned ID = %d, want %d", deleted.ID, id)
	}
	if deleted.TelegramID != sub.TelegramID {
		t.Errorf("DeleteSubscriptionByID() returned TelegramID = %d, want %d", deleted.TelegramID, sub.TelegramID)
	}

	// Verify it's hard deleted (not soft delete)
	var count int64
	DB.Model(&Subscription{}).Unscoped().Where("id = ?", id).Count(&count)
	if count != 0 {
		t.Error("Subscription should be hard deleted (permanently removed)")
	}
}

func TestDeleteSubscriptionByID_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Try to delete non-existent subscription
	_, err := DeleteSubscriptionByID(99999)
	if err == nil {
		t.Error("DeleteSubscriptionByID() should return error for non-existent ID")
	}
}

func TestDeleteSubscriptionByID_DatabaseNotInitialized(t *testing.T) {
	// Close database if open
	Close()

	_, err := DeleteSubscriptionByID(1)
	if err == nil {
		t.Error("DeleteSubscriptionByID() should return error when database not initialized")
	}
}

func TestGetAllTelegramIDs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
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
		if err := CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create subscription: %v", err)
		}
	}

	// Get all Telegram IDs
	ids, err := GetAllTelegramIDs()
	if err != nil {
		t.Fatalf("GetAllTelegramIDs() error = %v", err)
	}

	// Should have 3 unique IDs (111111111, 222222222, 333333333)
	if len(ids) != 3 {
		t.Errorf("GetAllTelegramIDs() returned %d IDs, want 3", len(ids))
	}

	// Verify IDs are present
	idMap := make(map[int64]bool)
	for _, id := range ids {
		idMap[id] = true
	}

	for _, expectedID := range []int64{111111111, 222222222, 333333333} {
		if !idMap[expectedID] {
			t.Errorf("Expected Telegram ID %d not found in results", expectedID)
		}
	}
}

func TestGetAllTelegramIDs_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Get all Telegram IDs from empty database
	ids, err := GetAllTelegramIDs()
	if err != nil {
		t.Fatalf("GetAllTelegramIDs() error = %v", err)
	}

	if len(ids) != 0 {
		t.Errorf("GetAllTelegramIDs() returned %d IDs, want 0", len(ids))
	}
}

func TestGetAllTelegramIDs_DatabaseNotInitialized(t *testing.T) {
	// Close database if open
	Close()

	_, err := GetAllTelegramIDs()
	if err == nil {
		t.Error("GetAllTelegramIDs() should return error when database not initialized")
	}
}

func TestGetTelegramIDByUsername(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Create test subscription
	sub := &Subscription{
		TelegramID: 123456789,
		Username:   "testuser",
		ClientID:   "client-id",
		Status:     "active",
		ExpiryTime: time.Now().Add(24 * time.Hour),
	}
	if err := CreateSubscription(sub); err != nil {
		t.Fatalf("Failed to create subscription: %v", err)
	}

	// Get Telegram ID by username
	id, err := GetTelegramIDByUsername("testuser")
	if err != nil {
		t.Fatalf("GetTelegramIDByUsername() error = %v", err)
	}

	if id != 123456789 {
		t.Errorf("GetTelegramIDByUsername() returned %d, want 123456789", id)
	}
}

func TestGetTelegramIDByUsername_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Try to get non-existent username
	_, err := GetTelegramIDByUsername("nonexistent")
	if err == nil {
		t.Error("GetTelegramIDByUsername() should return error for non-existent username")
	}
}

func TestGetTelegramIDByUsername_DatabaseNotInitialized(t *testing.T) {
	// Close database if open
	Close()

	_, err := GetTelegramIDByUsername("testuser")
	if err == nil {
		t.Error("GetTelegramIDByUsername() should return error when database not initialized")
	}
}

// ==================== Migration Tests ====================

func TestRunMigrations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Save original migrations
	originalMigrations := migrations
	defer func() { migrations = originalMigrations }()

	// Add a test migration
	migrations = []Migration{
		{Name: "test_migration_001", SQL: ""},
	}

	// Run migrations
	err := RunMigrations(DB)
	if err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	// Verify schema_migrations table was created
	if !DB.Migrator().HasTable(&SchemaMigration{}) {
		t.Error("schema_migrations table was not created")
	}
}

func TestRunMigrations_AppliesNewMigrations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Save and replace migrations
	originalMigrations := migrations
	defer func() { migrations = originalMigrations }()

	// Add test migrations
	migrations = []Migration{
		{Name: "test_migration_001", SQL: ""},
		{Name: "test_migration_002", SQL: ""},
	}

	// Run migrations
	err := RunMigrations(DB)
	if err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	// Verify migrations were recorded
	applied, err := GetAppliedMigrations()
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}

	if len(applied) != 2 {
		t.Errorf("Expected 2 applied migrations, got %d", len(applied))
	}
}

func TestRunMigrations_SkipsAlreadyApplied(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Save and replace migrations
	originalMigrations := migrations
	defer func() { migrations = originalMigrations }()

	migrations = []Migration{
		{Name: "test_skip_migration", SQL: ""},
	}

	// First run
	err := RunMigrations(DB)
	if err != nil {
		t.Fatalf("RunMigrations() first error = %v", err)
	}

	// Second run should skip
	err = RunMigrations(DB)
	if err != nil {
		t.Fatalf("RunMigrations() second error = %v", err)
	}

	// Verify only one migration was applied
	applied, err := GetAppliedMigrations()
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}

	if len(applied) != 1 {
		t.Errorf("Expected 1 applied migration, got %d", len(applied))
	}
}

func TestIsMigrationApplied(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Save and replace migrations
	originalMigrations := migrations
	defer func() { migrations = originalMigrations }()

	migrations = []Migration{
		{Name: "test_apply_check", SQL: ""},
	}

	// Check before applying
	applied, err := isMigrationApplied(DB, "test_apply_check")
	if err != nil {
		t.Fatalf("isMigrationApplied() error = %v", err)
	}
	if applied {
		t.Error("Migration should not be applied yet")
	}

	// Apply migration
	err = RunMigrations(DB)
	if err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	// Check after applying
	applied, err = isMigrationApplied(DB, "test_apply_check")
	if err != nil {
		t.Fatalf("isMigrationApplied() error = %v", err)
	}
	if !applied {
		t.Error("Migration should be applied")
	}
}

func TestGetSchemaVersion(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Save and replace migrations
	originalMigrations := migrations
	defer func() { migrations = originalMigrations }()

	migrations = []Migration{
		{Name: "schema_version_test", SQL: ""},
	}

	// Before any migration - should return "initial"
	version, err := GetSchemaVersion()
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != "initial" {
		t.Errorf("GetSchemaVersion() = %q, want 'initial'", version)
	}

	// Run migrations
	err = RunMigrations(DB)
	if err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	// After migration - should return migration name
	version, err = GetSchemaVersion()
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != "schema_version_test" {
		t.Errorf("GetSchemaVersion() = %q, want 'schema_version_test'", version)
	}
}

func TestGetSchemaVersion_DatabaseNotInitialized(t *testing.T) {
	// Close database
	Close()

	_, err := GetSchemaVersion()
	if err == nil {
		t.Error("GetSchemaVersion() should return error when database not initialized")
	}
}

func TestAddMigration(t *testing.T) {
	// Save original migrations
	originalMigrations := migrations
	defer func() { migrations = originalMigrations }()

	// Add a new migration
	AddMigration("programmatic_migration", "CREATE TABLE test_table (id INTEGER);")

	// Verify it was added
	if len(migrations) != len(originalMigrations)+1 {
		t.Errorf("Expected %d migrations, got %d", len(originalMigrations)+1, len(migrations))
	}

	// Find the added migration
	found := false
	for _, m := range migrations {
		if m.Name == "programmatic_migration" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Added migration not found in migrations list")
	}
}

func TestGetPendingMigrations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Save and replace migrations
	originalMigrations := migrations
	defer func() { migrations = originalMigrations }()

	migrations = []Migration{
		{Name: "pending_test_1", SQL: ""},
		{Name: "pending_test_2", SQL: ""},
	}

	// Before running migrations - all should be pending
	pending, err := GetPendingMigrations()
	if err != nil {
		t.Fatalf("GetPendingMigrations() error = %v", err)
	}

	if len(pending) != 2 {
		t.Errorf("Expected 2 pending migrations, got %d", len(pending))
	}

	// Run one migration
	migrations = []Migration{
		{Name: "pending_test_1", SQL: ""},
	}
	err = RunMigrations(DB)
	if err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	// Reset and check again
	migrations = []Migration{
		{Name: "pending_test_1", SQL: ""},
		{Name: "pending_test_2", SQL: ""},
	}
	pending, err = GetPendingMigrations()
	if err != nil {
		t.Fatalf("GetPendingMigrations() error = %v", err)
	}

	// Now only pending_test_2 should be pending
	if len(pending) != 1 {
		t.Errorf("Expected 1 pending migration, got %d", len(pending))
	}
}

func TestGetPendingMigrations_DatabaseNotInitialized(t *testing.T) {
	// Close database
	Close()

	_, err := GetPendingMigrations()
	if err == nil {
		t.Error("GetPendingMigrations() should return error when database not initialized")
	}
}

func TestGetAppliedMigrations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Save and replace migrations
	originalMigrations := migrations
	defer func() { migrations = originalMigrations }()

	migrations = []Migration{
		{Name: "applied_test_1", SQL: ""},
		{Name: "applied_test_2", SQL: ""},
	}

	// Run migrations
	err := RunMigrations(DB)
	if err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	// Get applied migrations
	applied, err := GetAppliedMigrations()
	if err != nil {
		t.Fatalf("GetAppliedMigrations() error = %v", err)
	}

	if len(applied) != 2 {
		t.Errorf("Expected 2 applied migrations, got %d", len(applied))
	}

	// Verify they are ordered by applied_at ASC
	for i := 0; i < len(applied)-1; i++ {
		if applied[i].AppliedAt.After(applied[i+1].AppliedAt) {
			t.Error("Applied migrations not ordered by applied_at ASC")
		}
	}
}

func TestGetAppliedMigrations_DatabaseNotInitialized(t *testing.T) {
	// Close database
	Close()

	_, err := GetAppliedMigrations()
	if err == nil {
		t.Error("GetAppliedMigrations() should return error when database not initialized")
	}
}

func TestExecSQL(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Execute simple SQL (create a table)
	err := ExecSQL("CREATE TABLE test_sql_table (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatalf("ExecSQL() error = %v", err)
	}

	// Verify table was created
	if !DB.Migrator().HasTable("test_sql_table") {
		t.Error("Table was not created by ExecSQL")
	}
}

func TestExecSQL_DatabaseNotInitialized(t *testing.T) {
	// Close database
	Close()

	err := ExecSQL("SELECT 1")
	if err == nil {
		t.Error("ExecSQL() should return error when database not initialized")
	}
}

func TestGetSQLDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	// Get underlying sql.DB
	sqlDB, err := GetSQLDB()
	if err != nil {
		t.Fatalf("GetSQLDB() error = %v", err)
	}
	if sqlDB == nil {
		t.Error("GetSQLDB() returned nil")
	}

	// Verify it's functional
	err = sqlDB.Ping()
	if err != nil {
		t.Errorf("sqlDB.Ping() error = %v", err)
	}
}

func TestGetSQLDB_DatabaseNotInitialized(t *testing.T) {
	// Close database
	Close()

	_, err := GetSQLDB()
	if err == nil {
		t.Error("GetSQLDB() should return error when database not initialized")
	}
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
	if err == nil {
		t.Error("UpdateSubscription() should return error when database not initialized")
	}
}

func TestGetAllSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	subs, err := service.GetAllSubscriptions(nil)
	if err != nil {
		t.Fatalf("GetAllSubscriptions() error = %v", err)
	}

	if len(subs) != 0 {
		t.Errorf("Expected 0 subscriptions, got %d", len(subs))
	}
}

func TestService_UpdateSubscription_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Try to update non-existent subscription
	sub := &Subscription{
		ID:         99999,
		TelegramID: 99999,
		Username:   "nonexistent",
		ClientID:   "nonexistent",
		Status:     "active",
	}

	err = service.UpdateSubscription(nil, sub)
	if err != nil {
		t.Errorf("UpdateSubscription() on non-existent should not error: %v", err)
	}
}

func TestService_DeleteSubscription_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Try to delete non-existent subscription - should not error
	err = service.DeleteSubscription(nil, 999999)
	if err != nil {
		t.Errorf("DeleteSubscription() on non-existent should not error: %v", err)
	}
}

func TestSubscription_TableName(t *testing.T) {
	sub := &Subscription{}
	if sub.TableName() != "subscriptions" {
		t.Errorf("TableName() = %s, want subscriptions", sub.TableName())
	}
}

func TestGetAllTelegramIDs_OneSubscriptionMultipleRevisions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
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
		if err := CreateSubscription(sub); err != nil {
			t.Fatalf("Failed to create subscription: %v", err)
		}
	}

	// Get all Telegram IDs - should return unique ID once
	ids, err := GetAllTelegramIDs()
	if err != nil {
		t.Fatalf("GetAllTelegramIDs() error = %v", err)
	}

	if len(ids) != 1 {
		t.Errorf("Expected 1 unique ID, got %d", len(ids))
	}
}

func TestService_CreateSubscription_DuplicateTelegramID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
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

	if err := service.CreateSubscription(nil, sub1); err != nil {
		t.Fatalf("First CreateSubscription() error = %v", err)
	}

	sub2 := &Subscription{
		TelegramID:      telegramID,
		Username:        "user2",
		ClientID:        "client2",
		Status:          "active",
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		SubscriptionURL: "http://localhost/sub/2",
	}

	if err := service.CreateSubscription(nil, sub2); err != nil {
		t.Fatalf("Second CreateSubscription() error = %v", err)
	}

	subs, err := service.GetAllSubscriptions(nil)
	if err != nil {
		t.Fatalf("GetAllSubscriptions() error = %v", err)
	}

	activeCount := 0
	for _, s := range subs {
		if s.TelegramID == telegramID && s.Status == "active" {
			activeCount++
		}
	}

	if activeCount != 1 {
		t.Errorf("Expected 1 active subscription, got %d", activeCount)
	}
}

func TestService_GetLatestSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	subs, err := service.GetLatestSubscriptions(nil, 10)
	if err != nil {
		t.Fatalf("GetLatestSubscriptions() error = %v", err)
	}

	if len(subs) != 0 {
		t.Errorf("Expected 0 subscriptions, got %d", len(subs))
	}
}

func TestService_GetAllSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	subs, err := service.GetAllSubscriptions(nil)
	if err != nil {
		t.Fatalf("GetAllSubscriptions() error = %v", err)
	}

	if len(subs) != 0 {
		t.Errorf("Expected 0 subscriptions, got %d", len(subs))
	}
}

func TestService_CountActiveSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	count, err := service.CountActiveSubscriptions(nil)
	if err != nil {
		t.Fatalf("CountActiveSubscriptions() error = %v", err)
	}

	if count != 0 {
		t.Errorf("Expected 0 active subscriptions, got %d", count)
	}
}

func TestService_CountExpiredSubscriptions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	count, err := service.CountExpiredSubscriptions(nil)
	if err != nil {
		t.Fatalf("CountExpiredSubscriptions() error = %v", err)
	}

	if count != 0 {
		t.Errorf("Expected 0 expired subscriptions, got %d", count)
	}
}

func TestService_GetByID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	sub := &Subscription{
		TelegramID:      123,
		Username:        "testuser",
		ClientID:        "client-1",
		XUIHost:         "http://localhost",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/abc",
	}

	err = service.CreateSubscription(nil, sub)
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	retrieved, err := service.GetByID(nil, sub.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if retrieved.ID != sub.ID {
		t.Errorf("GetByID() ID = %d, want %d", retrieved.ID, sub.ID)
	}
	if retrieved.TelegramID != 123 {
		t.Errorf("GetByID() TelegramID = %d, want 123", retrieved.TelegramID)
	}
}

func TestService_GetByID_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	_, err = service.GetByID(nil, 999)
	if err == nil {
		t.Error("GetByID() expected error for non-existent ID")
	}
}

func TestService_GetAllTelegramIDs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	for i, id := range []int64{100, 200, 300} {
		sub := &Subscription{
			TelegramID:      id,
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://test.url/sub/%d", i),
		}
		if err := service.CreateSubscription(nil, sub); err != nil {
			t.Fatalf("CreateSubscription() error = %v", err)
		}
	}

	ids, err := service.GetAllTelegramIDs(nil)
	if err != nil {
		t.Fatalf("GetAllTelegramIDs() error = %v", err)
	}

	if len(ids) != 3 {
		t.Errorf("GetAllTelegramIDs() len = %d, want 3", len(ids))
	}

	idMap := make(map[int64]bool)
	for _, id := range ids {
		idMap[id] = true
	}
	for _, expected := range []int64{100, 200, 300} {
		if !idMap[expected] {
			t.Errorf("GetAllTelegramIDs() missing ID %d", expected)
		}
	}
}

func TestService_GetTelegramIDByUsername(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	sub := &Subscription{
		TelegramID:      555,
		Username:        "findme",
		ClientID:        "client-find",
		XUIHost:         "http://localhost",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/find",
	}
	if err := service.CreateSubscription(nil, sub); err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	id, err := service.GetTelegramIDByUsername(nil, "findme")
	if err != nil {
		t.Fatalf("GetTelegramIDByUsername() error = %v", err)
	}
	if id != 555 {
		t.Errorf("GetTelegramIDByUsername() = %d, want 555", id)
	}
}

func TestService_GetTelegramIDByUsername_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	_, err = service.GetTelegramIDByUsername(nil, "nonexistent")
	if err == nil {
		t.Error("GetTelegramIDByUsername() expected error for non-existent username")
	}
}

func TestService_DeleteSubscriptionByID(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	sub := &Subscription{
		TelegramID:      666,
		Username:        "deleteme",
		ClientID:        "client-del",
		XUIHost:         "http://localhost",
		InboundID:       1,
		TrafficLimit:    107374182400,
		ExpiryTime:      time.Now().Add(24 * time.Hour),
		Status:          "active",
		SubscriptionURL: "http://test.url/sub/del",
	}
	if err := service.CreateSubscription(nil, sub); err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	deleted, err := service.DeleteSubscriptionByID(nil, sub.ID)
	if err != nil {
		t.Fatalf("DeleteSubscriptionByID() error = %v", err)
	}
	if deleted.ID != sub.ID {
		t.Errorf("DeleteSubscriptionByID() returned ID = %d, want %d", deleted.ID, sub.ID)
	}

	// Verify it's actually deleted
	_, err = service.GetByID(nil, sub.ID)
	if err == nil {
		t.Error("Subscription still exists after DeleteSubscriptionByID()")
	}
}

func TestService_DeleteSubscriptionByID_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	_, err = service.DeleteSubscriptionByID(nil, 999)
	if err == nil {
		t.Error("DeleteSubscriptionByID() expected error for non-existent ID")
	}
}

func TestService_Ping(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	if err := service.Ping(); err != nil {
		t.Errorf("Ping() error = %v", err)
	}
}

func TestService_Ping_AfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	service.Close()

	if err := service.Ping(); err == nil {
		t.Error("Ping() expected error after Close()")
	}
}

func TestService_GetTelegramIDsBatch(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Create test subscriptions
	for i := int64(1); i <= 5; i++ {
		sub := &Subscription{
			TelegramID:      i * 100,
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://test.url/sub/%d", i),
		}
		if err := service.CreateSubscription(nil, sub); err != nil {
			t.Fatalf("CreateSubscription() error = %v", err)
		}
	}

	// Test batch retrieval
	ids, err := service.GetTelegramIDsBatch(nil, 0, 3)
	if err != nil {
		t.Fatalf("GetTelegramIDsBatch() error = %v", err)
	}
	if len(ids) != 3 {
		t.Errorf("GetTelegramIDsBatch(0, 3) returned %d IDs, want 3", len(ids))
	}

	// Test offset
	ids, err = service.GetTelegramIDsBatch(nil, 3, 3)
	if err != nil {
		t.Fatalf("GetTelegramIDsBatch() error = %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("GetTelegramIDsBatch(3, 3) returned %d IDs, want 2", len(ids))
	}
}

func TestService_GetTotalTelegramIDCount(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	// Create test subscriptions with unique telegram IDs
	for i := int64(1); i <= 3; i++ {
		sub := &Subscription{
			TelegramID:      i * 100,
			Username:        fmt.Sprintf("user%d", i),
			ClientID:        fmt.Sprintf("client-%d", i),
			XUIHost:         "http://localhost",
			InboundID:       1,
			TrafficLimit:    107374182400,
			ExpiryTime:      time.Now().Add(24 * time.Hour),
			Status:          "active",
			SubscriptionURL: fmt.Sprintf("http://test.url/sub/%d", i),
		}
		if err := service.CreateSubscription(nil, sub); err != nil {
			t.Fatalf("CreateSubscription() error = %v", err)
		}
	}

	count, err := service.GetTotalTelegramIDCount(nil)
	if err != nil {
		t.Fatalf("GetTotalTelegramIDCount() error = %v", err)
	}
	if count != 3 {
		t.Errorf("GetTotalTelegramIDCount() = %d, want 3", count)
	}
}

func TestService_GetPoolStats(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	service, err := NewService(dbPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer service.Close()

	stats, err := service.GetPoolStats()
	if err != nil {
		t.Fatalf("GetPoolStats() error = %v", err)
	}

	if stats.MaxOpen < 0 {
		t.Errorf("GetPoolStats().MaxOpen = %d, want >= 0", stats.MaxOpen)
	}
}
