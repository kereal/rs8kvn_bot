package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/logger"
)

func TestMain(m *testing.M) {
	_, _ = logger.Init("", "error")

	os.Exit(m.Run())
}
func ptrTime(t time.Time) *time.Time { return &t }

func ptrInt64(v int64) *int64 { return &v }

// ==================== Model Method Tests ====================

func TestOrder_StatusUsesOrderStatusType(t *testing.T) {
	t.Parallel()

	orderType := reflect.TypeFor[Order]()
	statusField, ok := orderType.FieldByName("Status")
	require.True(t, ok)
	assert.Equal(t, "database.OrderStatus", statusField.Type.String())
}

func TestNode_TypeUsesNodeType(t *testing.T) {
	t.Parallel()

	nodeType := reflect.TypeFor[Node]()
	typeField, ok := nodeType.FieldByName("Type")
	require.True(t, ok)
	assert.Equal(t, "database.NodeType", typeField.Type.String())
}

func TestSubscriptionNode_StatusUsesSyncStatus(t *testing.T) {
	t.Parallel()

	subscriptionNodeType := reflect.TypeFor[SubscriptionNode]()
	statusField, ok := subscriptionNodeType.FieldByName("Status")
	require.True(t, ok)
	assert.Equal(t, "database.SyncStatus", statusField.Type.String())
}

func TestSubscription_IsExpired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		expiryTime *time.Time
		want       bool
	}{
		{"expired", ptrTime(time.Now().Add(-1 * time.Hour)), true},
		{"active", ptrTime(time.Now().Add(1 * time.Hour)), false},
		{"expires now", ptrTime(time.Now()), true},
		{"expires in future", ptrTime(time.Now().Add(24 * time.Hour)), false},
		{"nil expiry time (no expiry set)", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &Subscription{ExpiresAt: tt.expiryTime}
			assert.Equal(t, tt.want, sub.IsExpired())
		})
	}
}

func TestSubscription_IsActive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     string
		expiryTime *time.Time
		want       bool
	}{
		{"active and not expired", "active", ptrTime(time.Now().Add(1 * time.Hour)), true},
		{"active but expired", "active", ptrTime(time.Now().Add(-1 * time.Hour)), false},
		{"active with nil expiry (no expiry set)", "active", nil, true},
		{"revoked", "revoked", ptrTime(time.Now().Add(1 * time.Hour)), false},
		{"expired status", "expired", ptrTime(time.Now().Add(1 * time.Hour)), false},
		{"revoked with nil expiry", "revoked", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := &Subscription{Status: tt.status, ExpiresAt: tt.expiryTime}
			assert.Equal(t, tt.want, sub.IsActive())
		})
	}
}

// ==================== Service Lifecycle Tests ====================

func TestNewService(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NotNil(t, svc.db)

	err = svc.Close()
	if err != nil {
		t.Logf("Warning: failed to close database service: %v", err)
	}
}

func TestNewService_CreatesDirectory(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "test.db")

	svc, err := NewService(dbPath)

	require.NoError(t, err)
	defer func() {
		err := svc.Close()
		if err != nil {
			t.Logf("Warning: failed to close database service: %v", err)
		}
	}()

	_, err = os.Stat(filepath.Dir(dbPath))
	assert.NoError(t, err)
}

func TestNewService_InvalidPath(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "file.txt")

	require.NoError(t, os.WriteFile(dbPath, []byte("file"), 0600))

	_, err := NewService(dbPath)
	assert.Error(t, err)
}

func TestService_Close(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)
	require.NoError(t, err)

	assert.NoError(t, svc.Close())
}

func TestService_Close_AlreadyClosed(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)
	require.NoError(t, err)

	err = svc.Close()
	if err != nil {
		t.Logf("Warning: failed to close database service: %v", err)
	}

	assert.NoError(t, svc.Close(), "Second Close should return no error")
}

func TestService_Ping(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)

	require.NoError(t, err)
	defer func() {
		err := svc.Close()
		if err != nil {
			t.Logf("Warning: failed to close database service: %v", err)
		}
	}()

	assert.NoError(t, svc.Ping(context.Background()))
}

func TestService_GetPoolStats(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)

	require.NoError(t, err)
	defer func() {
		err := svc.Close()
		if err != nil {
			t.Logf("Warning: failed to close database service: %v", err)
		}
	}()

	stats, err := svc.GetPoolStats()
	require.NoError(t, err)
	assert.NotNil(t, stats)
	assert.GreaterOrEqual(t, stats.MaxOpen, 0)
}

// ==================== Service Subscription CRUD Tests ====================

func TestService_GetByTelegramID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	sub := createTestSubscription(t, svc, 12345, "testuser", "client-1")

	retrieved, err := svc.GetByTelegramID(context.Background(), 12345)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, retrieved.ID)
	assert.Equal(t, "testuser", retrieved.Username)
	assert.Equal(t, "active", retrieved.Status)
}

func TestService_GetByTelegramID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	_, err := svc.GetByTelegramID(context.Background(), 999999)
	assert.Error(t, err)
}

func TestService_GetByTelegramID_ReturnsActiveOnly(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	// Create active subscription
	activeSub := &Subscription{
		TelegramID: 12345,
		Username:   "active_user",
		ClientID:   "client-active",
		Status:     "active",
		ExpiresAt:  ptrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), prepareForCreate(t, svc, activeSub), ""))

	retrieved, err := svc.GetByTelegramID(context.Background(), 12345)
	require.NoError(t, err)
	assert.Equal(t, "client-active", retrieved.ClientID)
	assert.Equal(t, "active", retrieved.Status)
}

func TestService_GetByTelegramID_MultipleUsers(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	svc := newTestService(t)

	sub := &Subscription{
		TelegramID:     54321,
		Username:       "newuser",
		ClientID:       "client-456",
		SubscriptionID: "test-sub-id",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		Status:         "active",
	}

	require.NoError(t, svc.CreateSubscription(context.Background(), prepareForCreate(t, svc, sub), ""))

	retrieved, err := svc.GetByTelegramID(context.Background(), 54321)
	require.NoError(t, err)
	assert.Equal(t, "client-456", retrieved.ClientID)
}

func TestService_CreateSubscription_PersistsInviteCodeAndReferredBy(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Pre-create a referral invite for the referrer.
	_, err := svc.GetOrCreateInvite(ctx, 777777, "REFER123")
	require.NoError(t, err)

	sub := &Subscription{
		TelegramID:     888888,
		Username:       "refby",
		ClientID:       "c1",
		SubscriptionID: "s1",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		Status:         "active",
	}

	require.NoError(t, svc.CreateSubscription(ctx, prepareForCreate(t, svc, sub), "REFER123"))

	retrieved, err := svc.GetByTelegramID(ctx, 888888)
	require.NoError(t, err)
	require.NotNil(t, retrieved.InviteCode)
	assert.Equal(t, "REFER123", *retrieved.InviteCode, "InviteCode must be persisted on the subscription row")
	require.NotNil(t, retrieved.ReferredBy)
	assert.Equal(t, int64(777777), *retrieved.ReferredBy, "ReferredBy must be resolved and persisted")

	// Referral counts must reflect the new attribution.
	count, err := svc.GetReferralCount(ctx, 777777)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestService_CreateSubscription_EmptyInviteCodeLeavesFieldsEmpty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := &Subscription{
		TelegramID:     888889,
		Username:       "noinvite",
		ClientID:       "c2",
		SubscriptionID: "s2",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		Status:         "active",
	}

	require.NoError(t, svc.CreateSubscription(ctx, prepareForCreate(t, svc, sub), ""))

	retrieved, err := svc.GetByTelegramID(ctx, 888889)
	require.NoError(t, err)
	assert.Nil(t, retrieved.InviteCode)
	assert.Nil(t, retrieved.ReferredBy)
}

func TestService_CreateSubscription_UnknownInviteCodeDoesNotFail(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := &Subscription{
		TelegramID:     888890,
		Username:       "missinginvite",
		ClientID:       "c3",
		SubscriptionID: "s3",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		Status:         "active",
	}

	// An unknown invite code must not abort subscription creation.
	require.NoError(t, svc.CreateSubscription(ctx, prepareForCreate(t, svc, sub), "DOES_NOT_EXIST"))

	retrieved, err := svc.GetByTelegramID(ctx, 888890)
	require.NoError(t, err)
	assert.Nil(t, retrieved.InviteCode)
	assert.Nil(t, retrieved.ReferredBy)
}

func TestService_CreateSubscription_AllFields(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	now := time.Now()
	sub := &Subscription{
		TelegramID:     123456789,
		Username:       "testuser",
		ClientID:       "test-client-id",
		SubscriptionID: "test-sub-id",
		ExpiresAt:      ptrTime(now.Add(24 * time.Hour)),
		Status:         "active",
	}

	require.NoError(t, svc.CreateSubscription(context.Background(), prepareForCreate(t, svc, sub), ""))

	var retrieved Subscription
	require.NoError(t, svc.db.First(&retrieved, sub.ID).Error)

	assert.Equal(t, sub.TelegramID, retrieved.TelegramID)
	assert.Equal(t, sub.Username, retrieved.Username)
	assert.Equal(t, sub.ClientID, retrieved.ClientID)
	assert.Equal(t, sub.SubscriptionID, retrieved.SubscriptionID)
	assert.Equal(t, sub.Status, retrieved.Status)
}

func TestService_CreateSubscription_Timestamps(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	before := time.Now()
	sub := &Subscription{
		TelegramID: 123456789,
		Username:   "testuser",
		ClientID:   "test-client-id",
		Status:     "active",
		ExpiresAt:  ptrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), prepareForCreate(t, svc, sub), ""))

	after := time.Now()

	assert.True(t, sub.CreatedAt.After(before) || sub.CreatedAt.Equal(before))
	assert.True(t, sub.CreatedAt.Before(after) || sub.CreatedAt.Equal(after))
	assert.True(t, sub.UpdatedAt.After(before) || sub.UpdatedAt.Equal(before))
	assert.True(t, sub.UpdatedAt.Before(after) || sub.UpdatedAt.Equal(after))
}

func TestService_UpdateSubscription(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	sub := &Subscription{
		TelegramID:     99999,
		Username:       "updateuser",
		ClientID:       "client-789",
		SubscriptionID: "test-sub-id",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		Status:         "active",
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), prepareForCreate(t, svc, sub), ""))

	sub.Username = "updateduser"

	require.NoError(t, svc.UpdateSubscription(context.Background(), sub))

	retrieved, err := svc.GetByTelegramID(context.Background(), 99999)
	require.NoError(t, err)
	assert.Equal(t, "updateduser", retrieved.Username)
}

func TestService_UpdateSubscription_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	sub := &Subscription{
		ID:         99999,
		TelegramID: 99999,
		Username:   "nonexistent",
		ClientID:   "nonexistent",
		Status:     "active",
	}

	err := svc.UpdateSubscription(context.Background(), sub)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

// ==================== Service GetByID Tests ====================

func TestService_GetByID(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	svc := newTestService(t)

	_, err := svc.GetByID(context.Background(), 99999)
	assert.Error(t, err)
}

// ==================== Service DeleteSubscriptionByID Tests ====================

func TestService_DeleteSubscriptionByID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	sub := createTestSubscription(t, svc, 54321, "deleteuser", "client-delete")

	deleted, err := svc.DeleteSubscriptionByID(context.Background(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, deleted.ID)
	assert.Equal(t, sub.TelegramID, deleted.TelegramID)

	// Verify GetByID returns error (hard deleted)
	_, err = svc.GetByID(context.Background(), sub.ID)
	assert.Error(t, err)
}

// TestNewService_EnablesForeignKeys verifies that the shared DSN enables
// SQLite foreign-key enforcement on every connection (mattn/go-sqlite3 defaults
// to OFF): the migrations' PRAGMA foreign_keys epilogues and the admin /del
// cascade (migration 034) rely on it.
func TestNewService_EnablesForeignKeys(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	var foreignKeys int
	require.NoError(t, svc.db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error)
	assert.Equal(t, 1, foreignKeys, "_foreign_keys=on in the DSN must be applied per connection")
}

// TestService_DeleteSubscriptionByID_CascadesOrders verifies migration 034:
// deleting a subscription that has orders must succeed because the
// orders.subscription_id FK is ON DELETE CASCADE — otherwise the admin /del
// flow would fail with FOREIGN KEY constraint failed once FK enforcement is on.
func TestService_DeleteSubscriptionByID_CascadesOrders(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	sub := createTestSubscription(t, svc, 987654, "cascade-user", "client-cascade")
	plan, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)
	product := &Product{PlanID: plan.ID, Name: "cascade", DurationDays: 30, PriceCents: 100, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: sub.ID,
		ProductID:      product.ID,
		Status:         OrderStatusPaid,
		AmountCents:    100,
		Currency:       "RUB",
	}).Error)

	_, err = svc.DeleteSubscriptionByID(ctx, sub.ID)
	require.NoError(t, err, "delete must cascade the subscription's orders")

	var orderCount int64
	require.NoError(t, svc.db.Model(&Order{}).Where("subscription_id = ?", sub.ID).Count(&orderCount).Error)
	assert.Zero(t, orderCount, "orders must be cascade-deleted with the subscription")
}

func TestService_DeleteSubscriptionByID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	_, err := svc.DeleteSubscriptionByID(context.Background(), 99999)
	assert.Error(t, err)
}

// ==================== Service GetLatestSubscriptions Tests ====================

func TestService_GetLatestSubscriptions(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	for i := range 5 {
		sub := &Subscription{
			TelegramID:     int64(200000000 + i),
			Username:       fmt.Sprintf("service_user%d", i),
			ClientID:       fmt.Sprintf("client-%d", i),
			SubscriptionID: fmt.Sprintf("sub-%d", i),
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			Status:         "active",
			CreatedAt:      time.Now().Add(-time.Duration(5-i) * time.Minute),
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
	}

	subs, err := svc.GetLatestSubscriptions(context.Background(), 3)
	require.NoError(t, err)
	assert.Len(t, subs, 3)
	assert.Equal(t, "service_user4", subs[0].Username)
}

func TestService_GetLatestSubscriptions_Empty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	subs, err := svc.GetLatestSubscriptions(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, subs, 0)
}

func TestService_GetLatestSubscriptions_OnlyActive(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	activeSub := &Subscription{
		TelegramID:     100000001,
		Username:       "active_user",
		ClientID:       "client-active",
		SubscriptionID: "sub-active",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		Status:         "active",
	}
	require.NoError(t, svc.db.Create(prepareForCreate(t, svc, activeSub)).Error)

	revokedSub := &Subscription{
		TelegramID:     100000002,
		Username:       "revoked_user",
		ClientID:       "client-revoked",
		SubscriptionID: "sub-revoked",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		Status:         "revoked",
	}
	require.NoError(t, svc.db.Create(prepareForCreate(t, svc, revokedSub)).Error)

	subs, err := svc.GetLatestSubscriptions(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, subs, 1)
	assert.Equal(t, "active_user", subs[0].Username)
}

func TestService_GetLatestSubscriptions_LimitZero(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	for i := range 5 {
		createTestSubscription(t, svc, int64(100000000+i), fmt.Sprintf("user%d", i), fmt.Sprintf("client-%d", i))
	}

	subs, err := svc.GetLatestSubscriptions(context.Background(), 0)
	require.NoError(t, err)
	assert.Len(t, subs, 0)
}

func TestService_GetLatestSubscriptions_LimitOne(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	for i := range 5 {
		sub := &Subscription{
			TelegramID:     int64(100000000 + i),
			Username:       fmt.Sprintf("user%d", i),
			ClientID:       fmt.Sprintf("client-%d", i),
			SubscriptionID: fmt.Sprintf("sub-%d", i),
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			Status:         "active",
			CreatedAt:      time.Now().Add(-time.Duration(5-i) * time.Minute),
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
	}

	subs, err := svc.GetLatestSubscriptions(context.Background(), 1)
	require.NoError(t, err)
	assert.Len(t, subs, 1)
	assert.Equal(t, "user4", subs[0].Username)
}

func TestService_GetLatestSubscriptions_LimitGreaterThanTotal(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	for i := range 3 {
		sub := &Subscription{
			TelegramID:     int64(100000000 + i),
			Username:       fmt.Sprintf("user%d", i),
			ClientID:       fmt.Sprintf("client-%d", i),
			SubscriptionID: fmt.Sprintf("sub-%d", i),
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			Status:         "active",
			CreatedAt:      time.Now().Add(-time.Duration(3-i) * time.Minute),
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
	}

	subs, err := svc.GetLatestSubscriptions(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, subs, 3)
}

func TestService_GetLatestSubscriptions_SpecialCharacters(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	specialUsernames := []string{"user_name", "user-name", "user.name", "user123", "User_Case"}

	for i, username := range specialUsernames {
		sub := &Subscription{
			TelegramID:     int64(100000000 + i),
			Username:       username,
			ClientID:       fmt.Sprintf("client-%d", i),
			SubscriptionID: fmt.Sprintf("sub-%d", i),
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			Status:         "active",
			CreatedAt:      time.Now().Add(-time.Duration(len(specialUsernames)-i) * time.Minute),
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
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
	t.Parallel()

	svc := newTestService(t)

	baseTime := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	for i := range 5 {
		sub := &Subscription{
			TelegramID:     int64(100000000 + i),
			Username:       fmt.Sprintf("ordered_user%d", i),
			ClientID:       fmt.Sprintf("client-%d", i),
			SubscriptionID: fmt.Sprintf("sub-%d", i),
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			Status:         "active",
			CreatedAt:      baseTime.Add(time.Duration(i) * time.Hour),
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
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
	t.Parallel()

	svc := newTestService(t)

	statuses := []string{"active", "revoked", "expired", "active", "active"}

	for i, status := range statuses {
		sub := &Subscription{
			TelegramID:     int64(100000000 + i),
			Username:       fmt.Sprintf("status_user%d", i),
			ClientID:       fmt.Sprintf("client-%d", i),
			SubscriptionID: fmt.Sprintf("sub-%d", i),
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			Status:         status,
			CreatedAt:      time.Now().Add(-time.Duration(len(statuses)-i) * time.Minute),
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
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
	t.Parallel()

	svc := newTestService(t)

	for i := range 5 {
		sub := &Subscription{
			TelegramID:     int64(10000 + i),
			Username:       fmt.Sprintf("user%d", i),
			ClientID:       fmt.Sprintf("client-%d", i),
			SubscriptionID: fmt.Sprintf("sub-%d", i),
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			Status:         "active",
		}
		require.NoError(t, svc.CreateSubscription(context.Background(), prepareForCreate(t, svc, sub), ""))
	}

	subs, err := svc.GetAllSubscriptions(context.Background())
	require.NoError(t, err)
	assert.Len(t, subs, 5)
}

func TestService_GetAllSubscriptions_Empty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	subs, err := svc.GetAllSubscriptions(context.Background())
	require.NoError(t, err)
	assert.Len(t, subs, 0)
}

// ==================== Service Count Tests ====================

func TestService_CountActiveSubscriptions(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	for i := range 3 {
		sub := &Subscription{
			TelegramID:     int64(20000 + i),
			Username:       fmt.Sprintf("active%d", i),
			ClientID:       fmt.Sprintf("client-active-%d", i),
			SubscriptionID: fmt.Sprintf("sub-active-%d", i),
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			Status:         "active",
		}
		require.NoError(t, svc.CreateSubscription(context.Background(), prepareForCreate(t, svc, sub), ""))
	}

	// Create expired subscription (status=active but expiry in past)
	expiredSub := &Subscription{
		TelegramID:     29999,
		Username:       "expired",
		ClientID:       "client-expired",
		SubscriptionID: "sub-expired",
		ExpiresAt:      ptrTime(time.Now().Add(-1 * time.Hour)),
		Status:         "active",
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), prepareForCreate(t, svc, expiredSub), ""))

	count, err := svc.CountActiveSubscriptions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(4), count)
}

func TestService_CountTrialSubscriptions(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	// Create trial subscriptions (telegram_id < 0)
	for i := range 3 {
		sub := &Subscription{
			TelegramID:     int64(-1000 - i), // negative IDs are for trials
			Username:       fmt.Sprintf("trial%d", i),
			ClientID:       fmt.Sprintf("client-trial-%d", i),
			SubscriptionID: fmt.Sprintf("sub-trial-%d", i),
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			Status:         "active",
		}
		require.NoError(t, svc.CreateSubscription(context.Background(), prepareForCreate(t, svc, sub), ""))
	}

	// Create regular subscription (telegram_id > 0)
	regularSub := &Subscription{
		TelegramID:     12345,
		Username:       "regular",
		ClientID:       "client-regular",
		SubscriptionID: "sub-regular",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		Status:         "active",
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), prepareForCreate(t, svc, regularSub), ""))

	count, err := svc.CountTrialSubscriptions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

func TestService_CountAllSubscriptions(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	for i := range 3 {
		sub := &Subscription{
			TelegramID:     int64(40000 + i),
			Username:       fmt.Sprintf("countuser%d", i),
			ClientID:       fmt.Sprintf("client-count-%d", i),
			SubscriptionID: fmt.Sprintf("sub-count-%d", i),
			Status:         "active",
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
	}

	count, err := svc.CountAllSubscriptions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

// ==================== Service GetTelegramIDByUsername Tests ====================

func TestService_GetTelegramIDByUsername(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	sub := &Subscription{
		TelegramID:     123456789,
		Username:       "testuser",
		ClientID:       "client-id",
		SubscriptionID: "sub-client-id",
		Status:         "active",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
	}
	require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)

	id, err := svc.GetTelegramIDByUsername(context.Background(), "testuser")
	require.NoError(t, err)
	assert.Equal(t, int64(123456789), id)
}

func TestService_GetTelegramIDByUsername_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	_, err := svc.GetTelegramIDByUsername(context.Background(), "nonexistent")
	assert.Error(t, err)
}

// ==================== Service GetTelegramIDsBatch Tests ====================

func TestService_GetTelegramIDsBatch(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	for i := range 10 {
		sub := &Subscription{
			TelegramID:     int64(50000 + i),
			Username:       fmt.Sprintf("batchuser%d", i),
			ClientID:       fmt.Sprintf("client-batch-%d", i),
			SubscriptionID: fmt.Sprintf("sub-batch-%d", i),
			Status:         "active",
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
	}

	ids, err := svc.GetTelegramIDsBatch(context.Background(), 0, 5)
	require.NoError(t, err)
	assert.Len(t, ids, 5)

	ids2, err := svc.GetTelegramIDsBatch(context.Background(), 5, 5)
	require.NoError(t, err)
	assert.Len(t, ids2, 5)
}

func TestService_GetTelegramIDsBatch_OffsetBeyondTotal(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	for i := range 3 {
		sub := &Subscription{
			TelegramID:     int64(60000 + i),
			Username:       fmt.Sprintf("offsetuser%d", i),
			ClientID:       fmt.Sprintf("client-offset-%d", i),
			SubscriptionID: fmt.Sprintf("sub-offset-%d", i),
			Status:         "active",
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
	}

	ids, err := svc.GetTelegramIDsBatch(context.Background(), 10, 5)
	require.NoError(t, err)
	assert.Len(t, ids, 0)
}

// ==================== Invite Tests ====================

func TestService_GetOrCreateInvite(t *testing.T) {
	t.Parallel()

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

func TestService_GetInviteByReferrer(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Normal case: first creation
	invite, err := svc.GetOrCreateInvite(ctx, 77777, "FIRSTCODE")
	require.NoError(t, err)
	assert.Equal(t, "FIRSTCODE", invite.Code)

	// GetInviteByReferrer should return it
	found, err := svc.GetInviteByReferrer(ctx, 77777)
	require.NoError(t, err)
	assert.Equal(t, "FIRSTCODE", found.Code)
	assert.Equal(t, int64(77777), found.ReferrerTGID)

	// Simulate historical duplicates (pre-005 situation) by raw insert after temporarily dropping the unique constraint
	sqlDB, err := svc.db.DB()
	require.NoError(t, err)

	// Drop unique index if it exists (migration 005 creates it)
	_, _ = sqlDB.Exec(`DROP INDEX IF EXISTS idx_invites_referrer_unique`)

	// Insert two older codes for the same referrer (different created_at)
	_, err = sqlDB.Exec(`
		INSERT INTO invites (code, referrer_tg_id, created_at) VALUES 
		('OLDCODE1', 77777, '2024-01-01 00:00:00'),
		('OLDCODE2', 77777, '2024-06-01 00:00:00')
	`)
	require.NoError(t, err)

	// Even with duplicates present, GetInviteByReferrer must return the oldest one
	oldest, err := svc.GetInviteByReferrer(ctx, 77777)
	require.NoError(t, err)
	assert.Equal(t, "OLDCODE1", oldest.Code, "GetInviteByReferrer must return the oldest code by created_at")

	// Restore the index for other tests (not strictly necessary in this isolated test DB, but good hygiene)
	_, _ = sqlDB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_invites_referrer_unique ON invites(referrer_tg_id)`)
}

func TestService_GetInviteByCode(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	_, err := svc.GetOrCreateInvite(context.Background(), 54321, "GETCODE456")
	require.NoError(t, err)

	retrieved, err := svc.GetInviteByCode(context.Background(), "GETCODE456")
	require.NoError(t, err)
	assert.Equal(t, "GETCODE456", retrieved.Code)
	assert.Equal(t, int64(54321), retrieved.ReferrerTGID)
}

func TestService_GetInviteByCode_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	_, err := svc.GetInviteByCode(context.Background(), "NONEXISTENT")
	assert.Error(t, err)
}

func TestService_GetReferralCount(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	referrerID := int64(22222)
	for i := range 3 {
		sub := &Subscription{
			TelegramID:     int64(70000 + i),
			Username:       fmt.Sprintf("referral%d", i),
			ClientID:       fmt.Sprintf("client-ref-%d", i),
			SubscriptionID: fmt.Sprintf("sub-ref-%d", i),
			Status:         "active",
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			ReferredBy:     ptrInt64(referrerID),
			PlanID:         0,
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
	}

	count, err := svc.GetReferralCount(context.Background(), referrerID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

func TestService_GetReferralCount_None(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	count, err := svc.GetReferralCount(context.Background(), 99999)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestService_GetAllReferralCounts(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	referrerID := int64(33333)
	for i := range 2 {
		sub := &Subscription{
			TelegramID:     int64(80000 + i),
			Username:       fmt.Sprintf("refuser%d", i),
			ClientID:       fmt.Sprintf("client-refall-%d", i),
			SubscriptionID: fmt.Sprintf("sub-refall-%d", i),
			Status:         "active",
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			ReferredBy:     ptrInt64(referrerID),
			PlanID:         0,
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
	}

	counts, err := svc.GetAllReferralCounts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), counts[referrerID])
}

// ==================== GetTotalTelegramIDCount Tests ====================

func TestService_GetTotalTelegramIDCount_WithData(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	// Create multiple subscriptions (some with same telegram_id to test distinct)
	for i := range 3 {
		sub := &Subscription{
			TelegramID:     int64(90000 + i),
			Username:       fmt.Sprintf("countuser%d", i),
			ClientID:       fmt.Sprintf("client-count-%d", i),
			SubscriptionID: fmt.Sprintf("sub-count-%d", i),
			ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
			Status:         "active",
			PlanID:         0,
		}
		require.NoError(t, svc.db.Create(prepareForCreate(t, svc, sub)).Error)
	}

	count, err := svc.GetTotalTelegramIDCount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), count, "Should count unique telegram IDs")
}

func TestService_GetTotalTelegramIDCount_Empty(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	count, err := svc.GetTotalTelegramIDCount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(0), count, "Empty database should return 0")
}

// ==================== Trial Tests ====================

func TestService_CreateTrialRequest(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	require.NoError(t, svc.CreateTrialRequest(context.Background(), "192.168.1.1"))

	var count int64
	svc.db.Model(&TrialRequest{}).Where("ip = ?", "192.168.1.1").Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestService_CountTrialRequestsByIPLastHour(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	ip := "10.0.0.1"
	for range 3 {
		require.NoError(t, svc.CreateTrialRequest(context.Background(), ip))
	}

	count, err := svc.CountTrialRequestsByIPLastHour(context.Background(), ip)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestService_CountTrialRequestsByIPLastHour_None(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	count, err := svc.CountTrialRequestsByIPLastHour(context.Background(), "10.0.0.99")
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestService_CleanupExpiredTrials(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	// Get the actual trial plan ID
	trialPlan, err := svc.GetPlanByName(context.Background(), TrialPlanName)
	require.NoError(t, err)

	// Create old trial subscription
	oldTrial := &Subscription{
		TelegramID:     -1,
		ClientID:       "old-trial-client",
		SubscriptionID: "old-trial-sub",
		PlanID:         trialPlan.ID,
		Status:         "active",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		CreatedAt:      time.Now().Add(-48 * time.Hour),
	}
	require.NoError(t, svc.db.Create(oldTrial).Error)

	// Create old trial request
	oldRequest := &TrialRequest{
		IP:        "10.0.0.1",
		CreatedAt: time.Now().Add(-48 * time.Hour),
	}
	require.NoError(t, svc.db.Create(oldRequest).Error)

	// Create recent trial request (30 min ago — within the 1-hour rate limit window)
	recentRequest := &TrialRequest{
		IP:        "10.0.0.2",
		CreatedAt: time.Now().Add(-30 * time.Minute),
	}
	require.NoError(t, svc.db.Create(recentRequest).Error)

	deleted, err := svc.CleanupExpiredTrials(context.Background(), 24)
	require.NoError(t, err)
	assert.Len(t, deleted, 1)

	// Old trial should be deleted
	var oldCount int64
	svc.db.Unscoped().Model(&Subscription{}).Where("subscription_id = ?", "old-trial-sub").Count(&oldCount)
	assert.Equal(t, int64(0), oldCount)

	// Old trial request should be deleted
	var oldReqCount int64
	svc.db.Model(&TrialRequest{}).Where("ip = ?", "10.0.0.1").Count(&oldReqCount)
	assert.Equal(t, int64(0), oldReqCount)

	// Recent trial request should still exist
	var recentReqCount int64
	svc.db.Model(&TrialRequest{}).Where("ip = ?", "10.0.0.2").Count(&recentReqCount)
	assert.Equal(t, int64(1), recentReqCount)
}

func TestService_CleanupExpiredTrials_UsesSubInboundID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	// Create old trial on inbound 5
	oldTrial := &Subscription{
		TelegramID:     -2,
		ClientID:       "multi-inbound-client",
		SubscriptionID: "multi-inbound-sub",
		PlanID:         1,
		Status:         "active",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		CreatedAt:      time.Now().Add(-48 * time.Hour),
	}
	require.NoError(t, svc.db.Create(oldTrial).Error)

	deleted, err := svc.CleanupExpiredTrials(context.Background(), 24)
	require.NoError(t, err)
	require.Len(t, deleted, 1)
	assert.Equal(t, "multi-inbound-sub", deleted[0].SubscriptionID)
}

// ==================== Trial Subscription Tests ====================

func TestService_CreateTrialSubscription_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	ctx := context.Background()
	expiresAt := time.Now().UTC().Add(3 * time.Hour)
	sub, err := svc.CreateTrialSubscription(
		ctx,
		"INVITE123",
		"sub-trial-abc",
		"client-xyz",
		expiresAt,
	)

	require.NoError(t, err, "CreateTrialSubscription should not error")
	assert.NotNil(t, sub)
	assert.Equal(t, "sub-trial-abc", sub.SubscriptionID)
	assert.Equal(t, "client-xyz", sub.ClientID)
	assert.Equal(t, uint(1), sub.PlanID, "Should be marked as trial")
	assert.Less(t, sub.TelegramID, int64(0), "Unbound trial should have negative telegram_id")
	require.NotNil(t, sub.ExpiresAt)
	assert.WithinDuration(t, expiresAt, *sub.ExpiresAt, time.Second)
	require.NotNil(t, sub.InviteCode)
	assert.Equal(t, "INVITE123", *sub.InviteCode)
}

func TestService_CreateTrialSubscription_AllowsSameSubID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	ctx := context.Background()

	// Create a trial subscription
	_, err := svc.CreateTrialSubscription(
		ctx,
		"INVITE123",
		"sub-same",
		"client-1",
		time.Now().Add(3*time.Hour),
	)
	require.NoError(t, err)

	// Verify the trial was created with a unique negative telegram_id
	sub, err := svc.GetByID(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "sub-same", sub.SubscriptionID)
	assert.Less(t, sub.TelegramID, int64(0), "Trial should have negative telegram_id")
}

func TestService_GetTrialSubscriptionBySubID_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	ctx := context.Background()

	// Create trial subscription
	sub, err := svc.CreateTrialSubscription(
		ctx,
		"INVITE999",
		"sub-trial-only",
		"client-trial-only",
		time.Now().Add(3*time.Hour),
	)
	require.NoError(t, err)

	// Retrieve only trial subscriptions
	found, err := svc.GetTrialSubscriptionBySubID(ctx, "sub-trial-only")

	require.NoError(t, err)
	assert.NotNil(t, found)
	assert.True(t, found.PlanID == 1)
	assert.Equal(t, sub.ID, found.ID)
}

func TestService_GetTrialSubscriptionBySubID_NonTrial(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	ctx := context.Background()

	// Create a regular (non-trial) subscription
	origSub := &Subscription{
		TelegramID:     999999,
		Username:       "regularuser",
		ClientID:       "client-regular",
		SubscriptionID: "sub-regular",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		Status:         "active",
		PlanID:         0,
	}
	err := svc.db.Create(prepareForCreate(t, svc, origSub)).Error
	require.NoError(t, err)

	// GetTrialSubscriptionBySubID should NOT return non-trial subscriptions
	_, err = svc.GetTrialSubscriptionBySubID(ctx, "sub-regular")

	assert.Error(t, err, "Non-trial subscription should not be returned by GetTrialSubscriptionBySubID")
}

func TestService_GetTrialSubscriptionBySubID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	ctx := context.Background()

	_, err := svc.GetTrialSubscriptionBySubID(ctx, "nonexistent-trial")

	assert.Error(t, err, "Non-existent trial subscription should return error")
}

func TestService_BindTrialSubscription_Success(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	ctx := context.Background()

	// Create trial subscription (unbound - negative telegram_id)
	_, err := svc.CreateTrialSubscription(
		ctx,
		"INVITE000",
		"sub-bind-test",
		"client-bind",
		time.Now().Add(3*time.Hour),
	)
	require.NoError(t, err)

	// Bind to a user
	bound, err := svc.BindTrialSubscription(ctx, "sub-bind-test", 555555, "bounduser")

	require.NoError(t, err)
	assert.NotNil(t, bound)
	assert.Equal(t, int64(555555), bound.TelegramID)
	assert.Equal(t, "bounduser", bound.Username)
	assert.Equal(t, uint(2), bound.PlanID, "Should be switched to free plan after binding")
	assert.Nil(t, bound.ExpiresAt, "bound trial becomes a perpetual free subscription")

	var stored Subscription
	require.NoError(t, svc.db.Where("subscription_id = ?", "sub-bind-test").First(&stored).Error)
	assert.Nil(t, stored.ExpiresAt, "free subscription must not retain the trial expiry")
}

func TestService_BindTrialSubscription_InviteLookupError(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.CreateTrialSubscription(
		ctx,
		"INVITE-LOOKUP-ERROR",
		"sub-invite-error",
		"client-invite-error",
		time.Now().Add(3*time.Hour),
	)
	require.NoError(t, err)
	require.NoError(t, svc.db.Migrator().DropTable(&Invite{}))

	bound, err := svc.BindTrialSubscription(ctx, "sub-invite-error", 777777, "user")

	require.Error(t, err)
	assert.Nil(t, bound)
	assert.Contains(t, err.Error(), "failed to resolve trial invite")

	var sub Subscription
	require.NoError(t, svc.db.Where("subscription_id = ?", "sub-invite-error").First(&sub).Error)
	assert.Less(t, sub.TelegramID, int64(0), "binding must not continue after invite lookup failure")
}

func TestService_BindTrialSubscription_Concurrent(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	ctx := context.Background()

	// Create trial subscription
	_, err := svc.CreateTrialSubscription(
		ctx,
		"INVITE111",
		"sub-concurrent",
		"client-concurrent",
		time.Now().Add(3*time.Hour),
	)
	require.NoError(t, err)

	// First bind should succeed
	first, err := svc.BindTrialSubscription(ctx, "sub-concurrent", 111111, "firstuser")
	require.NoError(t, err)
	assert.Equal(t, int64(111111), first.TelegramID)

	// Second bind should fail (already bound)
	second, err := svc.BindTrialSubscription(ctx, "sub-concurrent", 222222, "seconduser")
	assert.Error(t, err, "Second bind should fail when already bound")
	assert.Nil(t, second)
}

func TestService_BindTrialSubscription_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	ctx := context.Background()

	_, err := svc.BindTrialSubscription(ctx, "nonexistent-sub", 123456, "test")

	assert.Error(t, err, "Binding non-existent subscription should fail")
}

// TestService_BindTrialSubscription_SetsTelegramID verifies that
// binding a trial sets the telegram_id on the trial subscription.
func TestService_BindTrialSubscription_SetsTelegramID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// Resolve the free plan id (BindTrial promotes trial→free).
	var freePlan Plan
	require.NoError(t, svc.db.Where("name = ?", FreePlanName).First(&freePlan).Error)

	// 1) Create a trial (unbound) for a subscription id.
	_, err := svc.CreateTrialSubscription(
		ctx, "INVITE000", "sub-bind-test", "client-trial", time.Now().Add(3*time.Hour),
	)
	require.NoError(t, err)

	// 2) Bind the trial to a user.
	bound, err := svc.BindTrialSubscription(ctx, "sub-bind-test", 999000, "newuser")
	require.NoError(t, err)
	require.NotNil(t, bound)
	assert.Equal(t, int64(999000), bound.TelegramID)
	assert.Equal(t, "active", bound.Status)

	// 3) Verify the subscription has the correct telegram_id.
	var sub Subscription
	require.NoError(t, svc.db.Where("subscription_id = ?", "sub-bind-test").First(&sub).Error)
	assert.Equal(t, int64(999000), sub.TelegramID)
	assert.Equal(t, freePlan.ID, sub.PlanID)
	assert.Nil(t, sub.ExpiresAt, "bound free subscription must have no expiry")
}

// ==================== Edge-case Tests ====================

func TestService_Ping_ContextCanceled(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	defer func() {
		err := svc.Close()
		if err != nil {
			t.Logf("Warning: failed to close database service: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.Ping(ctx)
	assert.Error(t, err, "Ping should fail when context is already canceled")
}

func TestService_CleanupExpiredTrials_RecordsError(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	plan, err := svc.GetPlanByName(context.Background(), TrialPlanName)
	require.NoError(t, err)
	require.NotNil(t, plan)

	oldTrial := &Subscription{
		TelegramID:     -3,
		ClientID:       "old-trial-client",
		SubscriptionID: "old-trial-sub",
		PlanID:         plan.ID,
		Status:         "active",
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		CreatedAt:      time.Now().Add(-48 * time.Hour),
	}
	require.NoError(t, svc.db.Create(oldTrial).Error)

	deleted, err := svc.CleanupExpiredTrials(context.Background(), 24)
	require.NoError(t, err, "CleanupExpiredTrials must surface cleanup errors instead of swallowing them")
	assert.Len(t, deleted, 1)
	assert.Equal(t, "old-trial-sub", deleted[0].SubscriptionID)
}

// ==================== Helper Functions ====================

func newTestService(t *testing.T) *Service {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	svc, err := NewService(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := svc.Close()
		if err != nil {
			t.Logf("Warning: failed to close database service: %v", err)
		}
	})

	return svc
}

// testFreePlanID returns the seeded free plan ID. The test DSN enables SQLite
// foreign-key enforcement, so subscriptions must reference an existing plan.
func testFreePlanID(t *testing.T, svc *Service) uint {
	t.Helper()

	plan, err := svc.GetPlanByName(context.Background(), FreePlanName)
	require.NoError(t, err)

	return plan.ID
}

// prepareForCreate defaults a subscription's plan to the seeded free plan so
// raw inserts satisfy the plans(id) foreign key enforced by the test DSN.
func prepareForCreate(t *testing.T, svc *Service, sub *Subscription) *Subscription {
	t.Helper()

	if sub.PlanID == 0 {
		sub.PlanID = testFreePlanID(t, svc)
	}

	return sub
}

func createTestSubscription(t *testing.T, svc *Service, telegramID int64, username, clientID string) *Subscription {
	t.Helper()

	sub := &Subscription{
		TelegramID:     telegramID,
		Username:       username,
		ClientID:       clientID,
		SubscriptionID: fmt.Sprintf("sub-%s", clientID),
		ExpiresAt:      ptrTime(time.Now().Add(24 * time.Hour)),
		Status:         "active",
		PlanID:         testFreePlanID(t, svc),
	}
	require.NoError(t, svc.CreateSubscription(context.Background(), prepareForCreate(t, svc, sub), ""))

	return sub
}
