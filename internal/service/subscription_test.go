package service

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/webhook"
	"rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	testutil.InitLogger(m)
	os.Exit(m.Run())
}

// mockWebhookSender captures webhook events for verification in tests.
type mockWebhookSender struct {
	mu     sync.Mutex
	events []Event
}

func (m *mockWebhookSender) SendAsync(event Event) {
	m.mu.Lock()
	m.events = append(m.events, event)
	m.mu.Unlock()
}

func (m *mockWebhookSender) getEvents() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Event, len(m.events))
	copy(result, m.events)
	return result
}

// ---------- 1. Create success ----------

func TestSubscriptionService_Create_Success(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
		XUIHost:        "http://localhost:2053",
		XUISubPath:     "sub",
	}

	var createdSub *database.Subscription
	db := &testutil.MockDatabaseService{
		CreateSubscriptionFunc: func(_ context.Context, sub *database.Subscription) error {
			createdSub = sub
			return nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(_ context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, _ time.Time, resetDays int) (*xui.ClientConfig, error) {
			assert.Equal(t, 1, inboundID)
			assert.Equal(t, "testuser", email)
			assert.Equal(t, int64(100*1024*1024*1024), trafficBytes)
			assert.Equal(t, config.SubscriptionResetDay, resetDays)
			return &xui.ClientConfig{ID: "client-123", SubID: "sub-456"}, nil
		},
		GetSubscriptionLinkFunc: func(host, subID, subPath string) string {
			return host + "/" + subPath + "/" + subID
		},
		GetExternalURLFunc: func(host string) string {
			return host
		},
	}

	wh := &mockWebhookSender{}
	svc := NewSubscriptionService(db, xuiClient, cfg, wh)
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify subscription fields
	sub := result.Subscription
	assert.Equal(t, int64(123456), sub.TelegramID)
	assert.Equal(t, "testuser", sub.Username)
	assert.Equal(t, "client-123", sub.ClientID)
	assert.Equal(t, "sub-456", sub.SubscriptionID)
	assert.Equal(t, 1, sub.InboundID)
	assert.Equal(t, int64(100*1024*1024*1024), sub.TrafficLimit)
	assert.Equal(t, "active", sub.Status)
	assert.Contains(t, sub.SubscriptionURL, "sub-456")

	// Verify CreateResult
	assert.Equal(t, sub.SubscriptionURL, result.SubscriptionURL)

	// Verify the sub passed to DB matches
	assert.NotNil(t, createdSub)
	assert.Equal(t, "client-123", createdSub.ClientID)
	assert.Equal(t, "sub-456", createdSub.SubscriptionID)

	// Verify webhook was sent
	events := wh.getEvents()
	assert.Len(t, events, 1)
	assert.Equal(t, webhook.EventSubscriptionActivated, events[0].Event)
	assert.Equal(t, "client-123", events[0].UserID)
	assert.Equal(t, "testuser", events[0].Email)
	assert.Equal(t, "sub-456", events[0].SubscriptionToken)
	assert.Contains(t, events[0].EventID, "evt-")
}

// ---------- 2. Create XUI fail ----------

func TestSubscriptionService_Create_XUIError(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
		XUIHost:        "http://localhost:2053",
		XUISubPath:     "sub",
	}

	dbCreateCalled := false
	db := &testutil.MockDatabaseService{
		CreateSubscriptionFunc: func(_ context.Context, _ *database.Subscription) error {
			dbCreateCalled = true
			return nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(_ context.Context, _ int, _, _, _ string, _ int64, _ time.Time, _ int) (*xui.ClientConfig, error) {
			return nil, errors.New("connection refused")
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "xui add client")
	// DB CreateSubscription should NOT have been called
	assert.False(t, dbCreateCalled, "DB should not be called when XUI fails")
}

// ---------- 3. Create DB fail + rollback success ----------

func TestSubscriptionService_Create_DBError_RollbackSuccess(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
		XUIHost:        "http://localhost:2053",
		XUISubPath:     "sub",
	}

	rollbackCalled := false
	db := &testutil.MockDatabaseService{
		CreateSubscriptionFunc: func(_ context.Context, _ *database.Subscription) error {
			return errors.New("database error")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(_ context.Context, _ int, _, _, _ string, _ int64, _ time.Time, _ int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-123", SubID: "sub-456"}, nil
		},
		DeleteClientFunc: func(_ context.Context, inboundID int, clientID string) error {
			rollbackCalled = true
			assert.Equal(t, cfg.XUIInboundID, inboundID)
			assert.Equal(t, "client-123", clientID)
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, rollbackCalled, "XUI rollback should be called on DB failure")
	assert.Contains(t, err.Error(), "create subscription")
	// Should NOT contain rollback error since rollback succeeded
	assert.NotContains(t, err.Error(), "rollback failed")
}

// ---------- 4. Create DB fail + rollback fail ----------

func TestSubscriptionService_Create_DBError_RollbackFailed(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
		XUIHost:        "http://localhost:2053",
		XUISubPath:     "sub",
	}

	rollbackCalled := false
	db := &testutil.MockDatabaseService{
		CreateSubscriptionFunc: func(_ context.Context, _ *database.Subscription) error {
			return errors.New("database error")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(_ context.Context, _ int, _, _, _ string, _ int64, _ time.Time, _ int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-123", SubID: "sub-456"}, nil
		},
		DeleteClientFunc: func(_ context.Context, _ int, _ string) error {
			rollbackCalled = true
			return errors.New("rollback failed")
		},
	}

	wh := &mockWebhookSender{}
	svc := NewSubscriptionService(db, xuiClient, cfg, wh)
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, rollbackCalled, "XUI rollback attempt should be made")
	// Both original error and rollback error should be present
	assert.Contains(t, err.Error(), "create subscription")
	assert.Contains(t, err.Error(), "rollback failed")
	// Webhook should NOT be sent on failure
	assert.Empty(t, wh.getEvents())
}

// ---------- 5. Delete success ----------

func TestSubscriptionService_Delete_Success(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	sub := &database.Subscription{
		TelegramID:     123456,
		Username:       "testuser",
		ClientID:       "client-123",
		SubscriptionID: "sub-456",
		InboundID:      1,
	}

	var callOrder []string
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, telegramID int64) (*database.Subscription, error) {
			assert.Equal(t, int64(123456), telegramID)
			return sub, nil
		},
		DeleteSubscriptionFunc: func(_ context.Context, telegramID int64) error {
			callOrder = append(callOrder, "db")
			return nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(_ context.Context, inboundID int, clientID string) error {
			callOrder = append(callOrder, "xui")
			assert.Equal(t, 1, inboundID)
			assert.Equal(t, "client-123", clientID)
			return nil
		},
	}

	wh := &mockWebhookSender{}
	svc := NewSubscriptionService(db, xuiClient, cfg, wh)
	err := svc.Delete(context.Background(), 123456)

	assert.NoError(t, err)
	// DB delete must happen before XUI delete
	assert.Equal(t, []string{"db", "xui"}, callOrder, "DB delete should happen before XUI delete")

	// Verify webhook was sent with expired event
	events := wh.getEvents()
	assert.Len(t, events, 1)
	assert.Equal(t, webhook.EventSubscriptionExpired, events[0].Event)
	assert.Equal(t, "client-123", events[0].UserID)
	assert.Equal(t, "testuser", events[0].Email)
	assert.Equal(t, "sub-456", events[0].SubscriptionToken)
}

// ---------- 6. Delete not found ----------

func TestSubscriptionService_Delete_NotFound(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	xuiDeleteCalled := false
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, _ int64) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(_ context.Context, _ int, _ string) error {
			xuiDeleteCalled = true
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	err := svc.Delete(context.Background(), 999999)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.False(t, xuiDeleteCalled, "XUI should not be called when subscription not found")
}

// ---------- 7. Delete DB fail ----------

func TestSubscriptionService_Delete_DBError(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	sub := &database.Subscription{
		TelegramID: 123456,
		ClientID:   "client-123",
		InboundID:  1,
	}

	xuiDeleteCalled := false
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, _ int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(_ context.Context, _ int64) error {
			return errors.New("db connection refused")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(_ context.Context, _ int, _ string) error {
			xuiDeleteCalled = true
			return nil
		},
	}

	wh := &mockWebhookSender{}
	svc := NewSubscriptionService(db, xuiClient, cfg, wh)
	err := svc.Delete(context.Background(), 123456)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db delete")
	assert.False(t, xuiDeleteCalled, "XUI should not be called when DB delete fails")
	// Webhook should NOT be sent on failure
	assert.Empty(t, wh.getEvents())
}

// ---------- 8. Delete XUI fail (best-effort) ----------

func TestSubscriptionService_Delete_XUIError(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	sub := &database.Subscription{
		TelegramID:     123456,
		Username:       "testuser",
		ClientID:       "client-123",
		SubscriptionID: "sub-456",
		InboundID:      1,
	}

	dbDeleteCalled := false
	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, _ int64) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionFunc: func(_ context.Context, _ int64) error {
			dbDeleteCalled = true
			return nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(_ context.Context, _ int, _ string) error {
			return errors.New("xui connection refused")
		},
	}

	wh := &mockWebhookSender{}
	svc := NewSubscriptionService(db, xuiClient, cfg, wh)
	err := svc.Delete(context.Background(), 123456)

	// XUI deletion is best-effort: DB is deleted first, XUI errors don't fail the operation
	assert.NoError(t, err)
	assert.True(t, dbDeleteCalled, "DB delete should succeed even when XUI fails")

	// Webhook should still be sent on successful DB deletion
	events := wh.getEvents()
	assert.Len(t, events, 1)
	assert.Equal(t, webhook.EventSubscriptionExpired, events[0].Event)
	assert.Equal(t, "client-123", events[0].UserID)
	assert.Equal(t, "testuser", events[0].Email)
	assert.Equal(t, "sub-456", events[0].SubscriptionToken)
}

// ---------- 9. DeleteByID success ----------

func TestSubscriptionService_DeleteByID_Success(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	sub := &database.Subscription{
		ID:             5,
		TelegramID:     123456,
		Username:       "testuser",
		ClientID:       "client-abc",
		SubscriptionID: "sub-xyz",
		InboundID:      1,
	}

	var callOrder []string
	db := &testutil.MockDatabaseService{
		GetByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			assert.Equal(t, uint(5), id)
			return sub, nil
		},
		DeleteSubscriptionByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			callOrder = append(callOrder, "db")
			return sub, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(_ context.Context, inboundID int, clientID string) error {
			callOrder = append(callOrder, "xui")
			assert.Equal(t, sub.InboundID, inboundID)
			assert.Equal(t, "client-abc", clientID)
			return nil
		},
	}

	wh := &mockWebhookSender{}
	svc := NewSubscriptionService(db, xuiClient, cfg, wh)
	deleted, err := svc.DeleteByID(context.Background(), 5)

	assert.NoError(t, err)
	assert.NotNil(t, deleted)
	assert.Equal(t, uint(5), deleted.ID)
	assert.Equal(t, "client-abc", deleted.ClientID)

	// DB delete must happen before XUI delete
	assert.Equal(t, []string{"db", "xui"}, callOrder, "DB delete should happen before XUI delete")

	// Verify webhook
	events := wh.getEvents()
	assert.Len(t, events, 1)
	assert.Equal(t, webhook.EventSubscriptionExpired, events[0].Event)
	assert.Equal(t, "client-abc", events[0].UserID)
	assert.Equal(t, "testuser", events[0].Email)
	assert.Equal(t, "sub-xyz", events[0].SubscriptionToken)
}

// ---------- 10. DeleteByID not found ----------

func TestSubscriptionService_DeleteByID_NotFound(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	xuiDeleteCalled := false
	db := &testutil.MockDatabaseService{
		GetByIDFunc: func(_ context.Context, id uint) (*database.Subscription, error) {
			return nil, errors.New("record not found")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(_ context.Context, _ int, _ string) error {
			xuiDeleteCalled = true
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.DeleteByID(context.Background(), 999)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "get subscription")
	assert.False(t, xuiDeleteCalled, "XUI should not be called when subscription not found")
}

// ---------- 11. DeleteByID DB fail ----------

func TestSubscriptionService_DeleteByID_DBError(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	sub := &database.Subscription{
		ID:        5,
		ClientID:  "client-abc",
		InboundID: 1,
	}

	xuiDeleteCalled := false
	db := &testutil.MockDatabaseService{
		GetByIDFunc: func(_ context.Context, _ uint) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionByIDFunc: func(_ context.Context, _ uint) (*database.Subscription, error) {
			return nil, errors.New("db error")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(_ context.Context, _ int, _ string) error {
			xuiDeleteCalled = true
			return nil
		},
	}

	wh := &mockWebhookSender{}
	svc := NewSubscriptionService(db, xuiClient, cfg, wh)
	result, err := svc.DeleteByID(context.Background(), 5)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "db delete")
	assert.False(t, xuiDeleteCalled, "XUI should not be called when DB delete fails")
	assert.Empty(t, wh.getEvents(), "Webhook should not be sent on failure")
}

// ---------- 12. GetWithTraffic success ----------

func TestSubscriptionService_GetWithTraffic_Success(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
	}

	now := time.Now()
	expiryTime := now.Add(7 * 24 * time.Hour)
	sub := &database.Subscription{
		TelegramID:   123456,
		Username:     "testuser",
		TrafficLimit: 100 * 1024 * 1024 * 1024,
		CreatedAt:    now.Add(-24 * time.Hour),
		ExpiryTime:   expiryTime,
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, telegramID int64) (*database.Subscription, error) {
			assert.Equal(t, int64(123456), telegramID)
			return sub, nil
		},
	}

	// 50GB up + 30GB down = 80GB used
	xuiClient := &testutil.MockXUIClient{
		GetClientTrafficFunc: func(_ context.Context, username string) (*xui.ClientTraffic, error) {
			assert.Equal(t, "testuser", username)
			return &xui.ClientTraffic{
				Up:   50 * 1024 * 1024 * 1024,
				Down: 30 * 1024 * 1024 * 1024,
			}, nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	resultSub, traffic, err := svc.GetWithTraffic(context.Background(), 123456)

	assert.NoError(t, err)
	assert.Equal(t, sub, resultSub)

	// Verify traffic calculations
	assert.Equal(t, float64(80), traffic.UsedGB)
	assert.Equal(t, 100, traffic.LimitGB)
	assert.Equal(t, float64(80), traffic.Percentage)

	// Verify progress bar: 80% = 8 filled, 2 empty
	assert.Equal(t, "🟩🟩🟩🟩🟩🟩🟩🟩⬜⬜", traffic.ProgressBar)

	// Verify reset info (7 days ± 1 due to integer hour division)
	assert.GreaterOrEqual(t, traffic.DaysUntilReset, 6)
	assert.LessOrEqual(t, traffic.DaysUntilReset, 7)
	assert.Contains(t, traffic.ResetInfo, "дн.")

	// Verify formatted dates (non-zero time should produce Russian date)
	assert.NotEmpty(t, traffic.CreatedAtFormatted)
	assert.NotEqual(t, "—", traffic.CreatedAtFormatted)
	assert.NotEmpty(t, traffic.ExpiryTimeFormatted)
	assert.NotEqual(t, "—", traffic.ExpiryTimeFormatted)
}

func TestSubscriptionService_GetWithTraffic_Success_ZeroExpiry(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
	}

	now := time.Now()
	sub := &database.Subscription{
		TelegramID:   123456,
		Username:     "testuser",
		TrafficLimit: 100 * 1024 * 1024 * 1024,
		CreatedAt:    now.Add(-24 * time.Hour),
		ExpiryTime:   time.Time{}, // zero expiry
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, _ int64) (*database.Subscription, error) {
			return sub, nil
		},
	}

	xuiClient := &testutil.MockXUIClient{
		GetClientTrafficFunc: func(_ context.Context, _ string) (*xui.ClientTraffic, error) {
			return &xui.ClientTraffic{Up: 0, Down: 0}, nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	_, traffic, err := svc.GetWithTraffic(context.Background(), 123456)

	assert.NoError(t, err)
	// With zero ExpiryTime, reset time = CreatedAt + SubscriptionResetDay
	assert.GreaterOrEqual(t, traffic.DaysUntilReset, 28)
	assert.Contains(t, traffic.ResetInfo, "дн.")
	// Zero ExpiryTime should show "—" in formatted date
	assert.Equal(t, "—", traffic.ExpiryTimeFormatted)
}

func TestSubscriptionService_GetWithTraffic_NotFound(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, _ int64) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}

	svc := NewSubscriptionService(db, &testutil.MockXUIClient{}, cfg, &webhook.NoopSender{})
	sub, traffic, err := svc.GetWithTraffic(context.Background(), 999999)

	assert.Error(t, err)
	assert.Nil(t, sub)
	assert.Nil(t, traffic)
}

func TestSubscriptionService_GetWithTraffic_100Percent(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 10,
		XUIInboundID:   1,
	}

	now := time.Now()
	sub := &database.Subscription{
		TelegramID:   123456,
		Username:     "testuser",
		TrafficLimit: 10 * 1024 * 1024 * 1024,
		CreatedAt:    now.Add(-24 * time.Hour),
		ExpiryTime:   now.Add(7 * 24 * time.Hour),
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, _ int64) (*database.Subscription, error) {
			return sub, nil
		},
	}

	// 8GB up + 3GB down = 11GB used, but cap at 100% (10GB limit)
	xuiClient := &testutil.MockXUIClient{
		GetClientTrafficFunc: func(_ context.Context, _ string) (*xui.ClientTraffic, error) {
			return &xui.ClientTraffic{
				Up:   8 * 1024 * 1024 * 1024,
				Down: 3 * 1024 * 1024 * 1024,
			}, nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	_, traffic, err := svc.GetWithTraffic(context.Background(), 123456)

	assert.NoError(t, err)
	assert.Equal(t, float64(11), traffic.UsedGB)
	assert.Equal(t, float64(100), traffic.Percentage, "percentage should be capped at 100")
	assert.Equal(t, "🟩🟩🟩🟩🟩🟩🟩🟩🟩🟩", traffic.ProgressBar, "progress bar should be full at 100%")
}

func TestSubscriptionService_GetWithTraffic_ResetToday(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
	}

	// Expiry in the past means reset should happen now (daysUntilReset=0)
	sub := &database.Subscription{
		TelegramID:   123456,
		Username:     "testuser",
		TrafficLimit: 100 * 1024 * 1024 * 1024,
		CreatedAt:    time.Now().Add(-31 * 24 * time.Hour),
		ExpiryTime:   time.Now().Add(-1 * time.Hour), // expired 1 hour ago
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, _ int64) (*database.Subscription, error) {
			return sub, nil
		},
	}

	xuiClient := &testutil.MockXUIClient{
		GetClientTrafficFunc: func(_ context.Context, _ string) (*xui.ClientTraffic, error) {
			return &xui.ClientTraffic{Up: 0, Down: 0}, nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	_, traffic, err := svc.GetWithTraffic(context.Background(), 123456)

	assert.NoError(t, err)
	assert.Equal(t, 0, traffic.DaysUntilReset)
	assert.Contains(t, traffic.ResetInfo, "сегодня")
}

func TestSubscriptionService_GetWithTraffic_ZeroTimes_ResetToday(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
	}

	// Both CreatedAt and ExpiryTime zero -> resetTime = CreatedAt + 30 days
	// (year 0001 + 30 days), which is far in the past -> daysUntilReset = 0
	sub := &database.Subscription{
		TelegramID:   123456,
		Username:     "testuser",
		TrafficLimit: 100 * 1024 * 1024 * 1024,
		CreatedAt:    time.Time{},
		ExpiryTime:   time.Time{},
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, _ int64) (*database.Subscription, error) {
			return sub, nil
		},
	}

	xuiClient := &testutil.MockXUIClient{
		GetClientTrafficFunc: func(_ context.Context, _ string) (*xui.ClientTraffic, error) {
			return &xui.ClientTraffic{Up: 0, Down: 0}, nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	_, traffic, err := svc.GetWithTraffic(context.Background(), 123456)

	assert.NoError(t, err)
	// With zero CreatedAt, resetTime = year 0001 + 30 days, which is in the past
	// so DaysUntilReset returns 0 (reset should happen now)
	assert.Equal(t, 0, traffic.DaysUntilReset)
	assert.Contains(t, traffic.ResetInfo, "сегодня")
	assert.Equal(t, "—", traffic.CreatedAtFormatted)
	assert.Equal(t, "—", traffic.ExpiryTimeFormatted)
}

// ---------- 13. GetWithTraffic XUI fail (zero-traffic fallback) ----------

func TestSubscriptionService_GetWithTraffic_XUIErrorFallback(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
	}

	now := time.Now()
	sub := &database.Subscription{
		TelegramID:   123456,
		Username:     "testuser",
		TrafficLimit: 100 * 1024 * 1024 * 1024,
		CreatedAt:    now,
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, _ int64) (*database.Subscription, error) {
			return sub, nil
		},
	}

	xuiClient := &testutil.MockXUIClient{
		GetClientTrafficFunc: func(_ context.Context, _ string) (*xui.ClientTraffic, error) {
			return nil, errors.New("connection refused")
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	resultSub, traffic, err := svc.GetWithTraffic(context.Background(), 123456)

	assert.NoError(t, err)
	assert.Equal(t, sub, resultSub)
	assert.NotNil(t, traffic)
	// Should return zero-traffic fallback
	assert.Equal(t, float64(0), traffic.UsedGB)
	assert.Equal(t, 100, traffic.LimitGB)
}

// ---------- 14. CreateTrial success ----------

func TestSubscriptionService_CreateTrial_Success(t *testing.T) {
	cfg := &config.Config{
		XUIInboundID:       1,
		XUIHost:            "http://localhost:2053",
		XUISubPath:         "sub",
		TrialDurationHours: 3,
	}

	var capturedEmail string
	var capturedTraffic int64
	var capturedResetDays int
	db := &testutil.MockDatabaseService{
		CreateTrialSubscriptionFunc: func(_ context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, _ time.Time, subURL string) (*database.Subscription, error) {
			assert.Equal(t, "testcode", inviteCode)
			assert.NotEmpty(t, subscriptionID)
			assert.NotEmpty(t, clientID)
			assert.Equal(t, 1, inboundID)
			assert.GreaterOrEqual(t, trafficBytes, int64(1024*1024*1024)) // at least 1GB
			assert.NotEmpty(t, subURL)
			return &database.Subscription{
				TelegramID:     0,
				SubscriptionID: subscriptionID,
				ClientID:       clientID,
				IsTrial:        true,
			}, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(_ context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, _ time.Time, resetDays int) (*xui.ClientConfig, error) {
			capturedEmail = email
			capturedTraffic = trafficBytes
			capturedResetDays = resetDays
			assert.Equal(t, 1, inboundID)
			assert.True(t, len(email) > 6 && email[:6] == "trial_", "email should have trial_ prefix, got: %s", email)
			assert.NotEmpty(t, clientID)
			assert.NotEmpty(t, subID)
			return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
		},
		GetSubscriptionLinkFunc: func(host, subID, subPath string) string {
			return host + "/" + subPath + "/" + subID
		},
		GetExternalURLFunc: func(host string) string {
			return host
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify TrialCreateResult fields
	assert.NotEmpty(t, result.SubID)
	assert.NotEmpty(t, result.ClientID)
	assert.Contains(t, result.SubscriptionURL, result.SubID)
	assert.NotNil(t, result.Subscription)
	assert.True(t, result.Subscription.IsTrial)
	assert.Equal(t, result.SubID, result.Subscription.SubscriptionID)
	assert.Equal(t, result.ClientID, result.Subscription.ClientID)

	// Verify XUI call parameters
	assert.Contains(t, capturedEmail, "trial_")
	assert.Equal(t, 0, capturedResetDays, "trial should have resetDays=0")
	// For 3 hours: 3 * 1GiB / 12 < 1GB -> minimum 1GB
	assert.Equal(t, int64(1024*1024*1024), capturedTraffic)
}

// ---------- 15. CreateTrial XUI fail ----------

func TestSubscriptionService_CreateTrial_XUIError(t *testing.T) {
	cfg := &config.Config{
		XUIInboundID:       1,
		XUIHost:            "http://localhost:2053",
		XUISubPath:         "sub",
		TrialDurationHours: 3,
	}

	dbCreateCalled := false
	db := &testutil.MockDatabaseService{
		CreateTrialSubscriptionFunc: func(_ context.Context, _, _, _ string, _ int, _ int64, _ time.Time, _ string) (*database.Subscription, error) {
			dbCreateCalled = true
			return nil, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(_ context.Context, _ int, _, _, _ string, _ int64, _ time.Time, _ int) (*xui.ClientConfig, error) {
			return nil, errors.New("xui error")
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "xui add client")
	assert.False(t, dbCreateCalled, "DB should not be called when XUI fails")
}

// ---------- 16. CreateTrial DB fail ----------

func TestSubscriptionService_CreateTrial_DBError(t *testing.T) {
	cfg := &config.Config{
		XUIInboundID:       1,
		XUIHost:            "http://localhost:2053",
		XUISubPath:         "sub",
		TrialDurationHours: 3,
	}

	rollbackCalled := false
	db := &testutil.MockDatabaseService{
		CreateTrialSubscriptionFunc: func(_ context.Context, _, _, _ string, _ int, _ int64, _ time.Time, _ string) (*database.Subscription, error) {
			return nil, errors.New("db error")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(_ context.Context, _ int, _, _, _ string, _ int64, _ time.Time, _ int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-1", SubID: "sub-1"}, nil
		},
		DeleteClientFunc: func(_ context.Context, inboundID int, clientID string) error {
			rollbackCalled = true
			return nil
		},
		GetSubscriptionLinkFunc: func(host, subID, subPath string) string {
			return host + "/" + subPath + "/" + subID
		},
		GetExternalURLFunc: func(host string) string {
			return host
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, rollbackCalled, "XUI rollback should be called on DB failure")
	assert.Contains(t, err.Error(), "create trial subscription")
	assert.NotContains(t, err.Error(), "rollback failed")
}

func TestSubscriptionService_CreateTrial_DBError_RollbackFailed(t *testing.T) {
	cfg := &config.Config{
		XUIInboundID:       1,
		XUIHost:            "http://localhost:2053",
		XUISubPath:         "sub",
		TrialDurationHours: 3,
	}

	db := &testutil.MockDatabaseService{
		CreateTrialSubscriptionFunc: func(_ context.Context, _, _, _ string, _ int, _ int64, _ time.Time, _ string) (*database.Subscription, error) {
			return nil, errors.New("db error")
		},
	}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(_ context.Context, _ int, _, _, _ string, _ int64, _ time.Time, _ int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-1", SubID: "sub-1"}, nil
		},
		DeleteClientFunc: func(_ context.Context, _ int, _ string) error {
			return errors.New("rollback failed")
		},
		GetSubscriptionLinkFunc: func(host, subID, subPath string) string {
			return host + "/" + subPath + "/" + subID
		},
		GetExternalURLFunc: func(host string) string {
			return host
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.CreateTrial(context.Background(), "testcode")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "create trial subscription")
	assert.Contains(t, err.Error(), "rollback failed")
}

// ---------- 17. CalcTrialTraffic ----------

func TestCalcTrialTraffic(t *testing.T) {
	const gib = 1024 * 1024 * 1024

	tests := []struct {
		name       string
		trialHours int
		want       int64
	}{
		{"1 hour minimum", 1, gib},
		{"2 hours still minimum", 2, gib},
		{"3 hours minimum", 3, gib},
		{"12 hours proportional", 12, gib},    // 12 * 1GiB / 12 = 1GiB
		{"24 hours", 24, 2 * gib},             // 24 * 1GiB / 12 = 2GiB
		{"48 hours", 48, 4 * gib},             // 48 * 1GiB / 12 = 4GiB
		{"168 hours (7 days)", 168, 14 * gib}, // 168 * 1GiB / 12 = 14GiB
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calcTrialTraffic(tt.trialHours)
			assert.Equal(t, tt.want, got)
			// Minimum is always 1GB
			assert.GreaterOrEqual(t, got, int64(gib))
		})
	}
}

func TestCalcTrialTraffic_Minimum1GB(t *testing.T) {
	const gib = 1024 * 1024 * 1024

	// Very small hours should still get 1GB minimum
	for _, hours := range []int{0, 1, 2, 5, 11} {
		got := calcTrialTraffic(hours)
		assert.GreaterOrEqual(t, got, int64(gib), "calcTrialTraffic(%d) should return at least 1GB", hours)
	}
}

func TestCalcTrialTraffic_Proportional(t *testing.T) {
	const gib = 1024 * 1024 * 1024

	// For large hours, should be proportional: hours * 1GiB / 12
	largeHours := []int{24, 48, 72, 168}
	for _, hours := range largeHours {
		expected := int64(hours) * gib / 12
		got := calcTrialTraffic(hours)
		assert.Equal(t, expected, got, "calcTrialTraffic(%d)", hours)
	}
}

// ---------- GetByTelegramID ----------

func TestSubscriptionService_GetByTelegramID_Success(t *testing.T) {
	cfg := &config.Config{}
	expected := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
		Status:     "active",
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, telegramID int64) (*database.Subscription, error) {
			assert.Equal(t, int64(123456), telegramID)
			return expected, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.GetByTelegramID(context.Background(), 123456)

	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestSubscriptionService_GetByTelegramID_NotFound(t *testing.T) {
	cfg := &config.Config{}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, _ int64) (*database.Subscription, error) {
			return nil, errors.New("not found")
		},
	}
	xuiClient := &testutil.MockXUIClient{}

	svc := NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})
	result, err := svc.GetByTelegramID(context.Background(), 999999)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "not found")
}

// ---------- DeleteByID XUI best-effort ----------

func TestSubscriptionService_DeleteByID_XUIError(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	sub := &database.Subscription{
		ID:             5,
		TelegramID:     123456,
		Username:       "testuser",
		ClientID:       "client-abc",
		SubscriptionID: "sub-xyz",
		InboundID:      1,
	}

	db := &testutil.MockDatabaseService{
		GetByIDFunc: func(_ context.Context, _ uint) (*database.Subscription, error) {
			return sub, nil
		},
		DeleteSubscriptionByIDFunc: func(_ context.Context, _ uint) (*database.Subscription, error) {
			return sub, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(_ context.Context, _ int, _ string) error {
			return errors.New("xui connection refused")
		},
	}

	wh := &mockWebhookSender{}
	svc := NewSubscriptionService(db, xuiClient, cfg, wh)
	deleted, err := svc.DeleteByID(context.Background(), 5)

	// XUI deletion is best-effort for DeleteByID too
	assert.NoError(t, err)
	assert.NotNil(t, deleted)

	// Webhook should still be sent
	events := wh.getEvents()
	assert.Len(t, events, 1)
	assert.Equal(t, webhook.EventSubscriptionExpired, events[0].Event)
}

// ---------- Create nil webhook (no panic) ----------

func TestSubscriptionService_Create_NilWebhook(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
		XUIInboundID:   1,
		XUIHost:        "http://localhost:2053",
		XUISubPath:     "sub",
	}

	db := &testutil.MockDatabaseService{}
	xuiClient := &testutil.MockXUIClient{
		AddClientWithIDFunc: func(_ context.Context, _ int, _, _, _ string, _ int64, _ time.Time, _ int) (*xui.ClientConfig, error) {
			return &xui.ClientConfig{ID: "client-1", SubID: "sub-1"}, nil
		},
		GetSubscriptionLinkFunc: func(host, subID, subPath string) string {
			return host + "/" + subPath + "/" + subID
		},
		GetExternalURLFunc: func(host string) string {
			return host
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, nil)
	result, err := svc.Create(context.Background(), 123456, "testuser")

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

// ---------- Delete nil webhook (no panic) ----------

func TestSubscriptionService_Delete_NilWebhook(t *testing.T) {
	cfg := &config.Config{XUIInboundID: 1}

	sub := &database.Subscription{
		TelegramID:     123456,
		Username:       "testuser",
		ClientID:       "client-123",
		SubscriptionID: "sub-456",
		InboundID:      1,
	}

	db := &testutil.MockDatabaseService{
		GetByTelegramIDFunc: func(_ context.Context, _ int64) (*database.Subscription, error) {
			return sub, nil
		},
	}
	xuiClient := &testutil.MockXUIClient{
		DeleteClientFunc: func(_ context.Context, _ int, _ string) error {
			return nil
		},
	}

	svc := NewSubscriptionService(db, xuiClient, cfg, nil)
	err := svc.Delete(context.Background(), 123456)

	assert.NoError(t, err)
}
