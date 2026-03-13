package database

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
