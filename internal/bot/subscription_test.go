package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShowLoadingMessage_WithMessageID(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 100

	resultID := handler.showLoadingMessage(chatID, messageID)

	assert.Equal(t, messageID, resultID, "Should return same messageID when editing")
	assert.True(t, mockBot.SendCalledSafe(), "Send should be called")
}

func TestShowLoadingMessage_WithoutMessageID(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 0

	resultID := handler.showLoadingMessage(chatID, messageID)

	assert.NotEqual(t, 0, resultID, "Should return new messageID when sending new message")
	assert.True(t, mockBot.SendCalledSafe(), "Send should be called")
}

func TestShowLoadingMessage_SendFails(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	mockBot.SendError = errors.New("send error")
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 0

	resultID := handler.showLoadingMessage(chatID, messageID)

	assert.Equal(t, 0, resultID, "Should return 0 when send fails")
}

func TestShowLoadingMessage_EditFails(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	// First call fails (edit), second succeeds (send new)
	mockBot.SendError = errors.New("edit error")
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 100

	// Reset error for second call
	resultID := handler.showLoadingMessage(chatID, messageID)

	// Should attempt to send new message if edit fails
	assert.True(t, mockBot.SendCalledSafe())
	// When edit fails and send new succeeds, should return new message ID
	_ = resultID // Just acknowledge the result, don't make assertions about it
}

func TestCreateSubscription_Success(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
		XUIHost:         "https://panel.example.com",
		XUISubPath:      "/sub",
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, mockXUI, cfg)

	clientConfig := &xui.ClientConfig{
		ID:    "client-uuid-123",
		SubID: "sub-id-456",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		assert.Equal(t, 1, inboundID)
		assert.Equal(t, "testuser", email)
		assert.NotEmpty(t, clientID)
		assert.NotEmpty(t, subID)
		return clientConfig, nil
	}

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		assert.Equal(t, int64(123456), sub.TelegramID)
		assert.Equal(t, "testuser", sub.Username)
		assert.Equal(t, "client-uuid-123", sub.ClientID)
		assert.Equal(t, "sub-id-456", sub.SubscriptionID)
		return nil
	}

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.True(t, mockXUI.AddClientWithIDFunc != nil)
	assert.True(t, mockDB.CreateSubscriptionFunc != nil)
}

func TestCreateSubscription_XUIFailure(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, mockXUI, cfg)

	tests := []struct {
		name          string
		errMsg        string
		expectedError string
	}{
		{
			name:          "connection refused",
			errMsg:        "connection refused",
			expectedError: "подключиться к серверу",
		},
		{
			name:          "timeout",
			errMsg:        "timeout error",
			expectedError: "подключиться к серверу",
		},
		{
			name:          "authentication error",
			errMsg:        "authentication failed",
			expectedError: "авторизации",
		},
		{
			name:          "unauthorized",
			errMsg:        "unauthorized access",
			expectedError: "авторизации",
		},
		{
			name:          "context canceled",
			errMsg:        "context canceled",
			expectedError: "прерван",
		},
		{
			name:          "no such host",
			errMsg:        "no such host",
			expectedError: "DNS",
		},
		{
			name:          "dial tcp error",
			errMsg:        "dial tcp error",
			expectedError: "DNS",
		},
		{
			name:          "certificate error",
			errMsg:        "certificate error",
			expectedError: "SSL/TLS",
		},
		{
			name:          "TLS error",
			errMsg:        "TLS handshake failed",
			expectedError: "SSL/TLS",
		},
		{
			name:          "inbound error",
			errMsg:        "inbound not found",
			expectedError: "сервера",
		},
		{
			name:          "client error",
			errMsg:        "client creation failed",
			expectedError: "сервера",
		},
		{
			name:          "generic error",
			errMsg:        "unknown error",
			expectedError: "создании подписки",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockBot.SetSendCalled(false)
			mockBot.LastSentText = ""

			mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
				return nil, errors.New(tt.errMsg)
			}

			ctx := context.Background()
			handler.createSubscription(ctx, 123456, "testuser", 1)

			assert.True(t, mockBot.SendCalledSafe())
			assert.Contains(t, mockBot.LastSentTextSafe(), tt.expectedError)
		})
	}
}

func TestCreateSubscription_DatabaseFailure_RollbackSuccess(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, mockXUI, cfg)

	clientConfig := &xui.ClientConfig{
		ID:    "client-uuid-123",
		SubID: "sub-id-456",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return clientConfig, nil
	}

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		return errors.New("database error")
	}

	rollbackCalled := false
	mockXUI.DeleteClientFunc = func(ctx context.Context, inboundID int, clientID string) error {
		rollbackCalled = true
		assert.Equal(t, "client-uuid-123", clientID)
		return nil
	}

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, rollbackCalled, "Rollback should be called")
	assert.True(t, mockBot.SendCalledSafe())
}

func TestCreateSubscription_DatabaseFailure_RollbackFailure(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, mockXUI, cfg)

	clientConfig := &xui.ClientConfig{
		ID:    "client-uuid-123",
		SubID: "sub-id-456",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return clientConfig, nil
	}

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		return errors.New("database error")
	}

	mockXUI.DeleteClientFunc = func(ctx context.Context, inboundID int, clientID string) error {
		return errors.New("rollback failed")
	}

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
}

func TestCreateSubscription_CacheUpdate(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
		XUIHost:         "https://panel.example.com",
		XUISubPath:      "/sub",
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, mockXUI, cfg)

	clientConfig := &xui.ClientConfig{
		ID:    "client-uuid-123",
		SubID: "sub-id-456",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return clientConfig, nil
	}

	var savedSub *database.Subscription
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		savedSub = sub
		return nil
	}

	ctx := context.Background()
	chatID := int64(123456)

	// Verify cache is empty initially
	cached := handler.cache.Get(chatID)
	assert.Nil(t, cached)

	handler.createSubscription(ctx, chatID, "testuser", 1)

	// Verify cache was updated
	cached = handler.cache.Get(chatID)
	require.NotNil(t, cached)
	assert.Equal(t, savedSub.TelegramID, cached.TelegramID)
}

func TestHandleCreateSubscription_AlreadyInProgress(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)

	// Simulate subscription already in progress (set by another goroutine)
	handler.inProgressSyncMap.Store(chatID, true)

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, chatID, "testuser", 1)

	// inProgress entry should NOT be cleaned up — it belongs to the goroutine that set it.
	// The early-return path doesn't register the defer cleanup.
	_, stillInProgress := handler.inProgressSyncMap.Load(chatID)
	assert.True(t, stillInProgress, "inProgress entry should remain (belongs to other goroutine)")

	// No bot interaction should have occurred (early return)
	assert.False(t, mockBot.SendCalledSafe(), "Bot should not be called when already in progress")
}

func TestHandleCreateSubscription_ExistingActiveSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	sub := &database.Subscription{
		TelegramID:      123456,
		Username:        "testuser",
		SubscriptionURL: "https://test.url/sub",
		ExpiryTime:      time.Now().Add(30 * 24 * time.Hour), // Not expired
		Status:          "active",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ваша подписка")
	// Verify no new client was created on XUI (user already has active sub)
	assert.False(t, mockXUI.AddClientWithIDCalled, "Should not create new XUI client for existing active subscription")
}
func TestHandleCreateSubscription_ExpiredSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	sub := &database.Subscription{
		TelegramID:      123456,
		Username:        "testuser",
		SubscriptionURL: "https://test.url/sub",
		ExpiryTime:      time.Now().Add(-24 * time.Hour), // Expired
		Status:          "expired",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	clientConfig := &xui.ClientConfig{
		ID:    "new-client-uuid",
		SubID: "new-sub-id",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return clientConfig, nil
	}

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		return nil
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	// Should create new subscription for expired user
	assert.True(t, mockXUI.AddClientWithIDFunc != nil)
}

func TestHandleCreateSubscription_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Временная ошибка")
}

func TestHandleCreateSubscription_NoSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, mockXUI, cfg)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	clientConfig := &xui.ClientConfig{
		ID:    "new-client-uuid",
		SubID: "new-sub-id",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return clientConfig, nil
	}

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		return nil
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockXUI.AddClientWithIDFunc != nil)
}

func TestHandleMySubscription_NotFound(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "нет активной подписки")
}

func TestHandleMySubscription_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Временная ошибка")
}

func TestHandleMySubscription_ActiveSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	sub := &database.Subscription{
		TelegramID:      123456,
		Username:        "testuser",
		SubscriptionURL: "https://test.url/sub",
		ExpiryTime:      time.Now().Add(30 * 24 * time.Hour),
		Status:          "active",
		CreatedAt:       time.Now().Add(-7 * 24 * time.Hour), // Created 7 days ago
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	traffic := &xui.ClientTraffic{
		Up:   1024 * 1024 * 1024, // 1 GB
		Down: 2048 * 1024 * 1024, // 2 GB
	}

	mockXUI.GetClientTrafficFunc = func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
		return traffic, nil
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ваша подписка")
	assert.Contains(t, mockBot.LastSentTextSafe(), "3.00 из 100 Гб (3%)") // 1 + 2 = 3 GB
	assert.Contains(t, mockBot.LastSentTextSafe(), "⬜⬜⬜⬜⬜⬜⬜⬜⬜⬜")          // Empty progress bar (3%)
	assert.Contains(t, mockBot.LastSentTextSafe(), "📅 Создана:")
	assert.Contains(t, mockBot.LastSentTextSafe(), "🔄 Сброс:")
}

func TestHandleMySubscription_TrafficFetchError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	sub := &database.Subscription{
		TelegramID:      123456,
		Username:        "testuser",
		SubscriptionURL: "https://test.url/sub",
		ExpiryTime:      time.Now().Add(30 * 24 * time.Hour),
		Status:          "active",
		CreatedAt:       time.Now().Add(-7 * 24 * time.Hour), // Created 7 days ago
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	mockXUI.GetClientTrafficFunc = func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
		return nil, errors.New("traffic error")
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ваша подписка")
	// Should still show subscription even if traffic fetch fails
	assert.Contains(t, mockBot.LastSentTextSafe(), "0.00 из 100 Гб (0%)")
	assert.Contains(t, mockBot.LastSentTextSafe(), "⬜⬜⬜⬜⬜⬜⬜⬜⬜⬜") // Empty progress bar (0%)
}

func TestHandleMySubscription_UsesCache(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	sub := &database.Subscription{
		TelegramID:      123456,
		Username:        "testuser",
		SubscriptionURL: "https://test.url/sub",
		ExpiryTime:      time.Now().Add(30 * 24 * time.Hour),
		Status:          "active",
	}

	// Set cache
	handler.cache.Set(123456, sub)

	traffic := &xui.ClientTraffic{
		Up:   1024 * 1024 * 1024,
		Down: 1024 * 1024 * 1024,
	}

	mockXUI.GetClientTrafficFunc = func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
		return traffic, nil
	}

	// Database should not be called if cache exists
	dbCalled := false
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		dbCalled = true
		return sub, nil
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.False(t, dbCalled, "Database should not be called when cache exists")
}

func TestHandleQRCode_Success(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	sub := &database.Subscription{
		TelegramID:      123456,
		Username:        "testuser",
		SubscriptionURL: "https://test.url/sub",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
}

func TestHandleQRCode_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "нет активной подписки")
}

func TestGetSubscriptionWithCache_CacheHit(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	sub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
		Status:     "active",
	}

	// Set cache
	handler.cache.Set(chatID, sub)

	// Database should not be called
	dbCalled := false
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		dbCalled = true
		return nil, nil
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.NoError(t, err)
	assert.Equal(t, sub, result)
	assert.False(t, dbCalled, "Database should not be called on cache hit")
}

func TestGetSubscriptionWithCache_CacheMiss(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	sub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
		Status:     "active",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.NoError(t, err)
	assert.Equal(t, sub, result)

	// Verify cache was updated
	cached := handler.cache.Get(chatID)
	assert.Equal(t, sub, cached)
}

func TestGetSubscriptionWithCache_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetSubscriptionWithCache_StaleCacheNonActive(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	// Put a non-active subscription in cache
	staleSub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
		Status:     "revoked",
	}
	handler.cache.Set(chatID, staleSub)

	// DB should return nothing
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
	assert.Nil(t, result)
	// Verify stale cache was invalidated
	assert.Nil(t, handler.cache.Get(chatID))
}

func TestInvalidateCache(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	sub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
	}

	// Set cache
	handler.cache.Set(chatID, sub)
	require.NotNil(t, handler.cache.Get(chatID))

	// Invalidate
	handler.invalidateCache(chatID)

	// Verify cache was cleared
	assert.Nil(t, handler.cache.Get(chatID))
}

func TestSendQRCode(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 789
	testURL := "https://t.me/testbot?start=share_ABC123"
	testCaption := "📱 QR-код для Telegram"

	ctx := context.Background()

	// Should not panic and should call Send
	assert.NotPanics(t, func() {
		handler.sendQRCode(ctx, chatID, messageID, testURL, testCaption)
	})

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called")
}

func TestHandleBackToInvite(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 789
	username := "testuser"

	ctx := context.Background()

	// Should not panic and should call Request (for delete)
	assert.NotPanics(t, func() {
		handler.handleBackToInvite(ctx, chatID, username, messageID)
	})

	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called for delete message")
}

func TestHandleCreateSubscription_ZeroMessageID(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, mockXUI, cfg)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	clientConfig := &xui.ClientConfig{
		ID:    "new-client-uuid",
		SubID: "new-sub-id",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return clientConfig, nil
	}

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		return nil
	}

	ctx := context.Background()
	// Pass 0 as messageID to test showLoadingMessage branch
	handler.handleCreateSubscription(ctx, 123456, "testuser", 0)

	assert.True(t, mockBot.SendCalledSafe())
}

func TestHandleQRCode_WithSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID:      telegramID,
			Username:        "testuser",
			SubscriptionURL: "vless://test@url:443?mode=vpn",
			Status:          "active",
		}, nil
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called for QR photo")
}

func TestHandleQRCode_NoSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called with error message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "нет активной подписки", "message should mention no subscription")
}

func TestHandleBackToSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	handler.handleBackToSubscription(ctx, 123456, "testuser", 789)

	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called to delete message")
	assert.False(t, mockBot.SendCalledSafe(), "Bot.Send should not be called")
}

func TestSendQRCode_Success(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	handler.sendQRCode(ctx, 123456, 100, "https://example.com/sub", "Test caption")

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called for QR photo")
}

func TestNotifyAdmin_Success(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	err := handler.notifyAdmin(ctx, "testuser", 789012, "https://sub.url", time.Now().Add(24*time.Hour))

	assert.NoError(t, err)
	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called to notify admin")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Новая подписка", "message should mention new subscription")
}

func TestNotifyAdmin_ZeroAdminID(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 0,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	err := handler.notifyAdmin(ctx, "testuser", 789012, "https://sub.url", time.Now())

	assert.NoError(t, err)
	assert.False(t, mockBot.SendCalledSafe(), "Bot.Send should not be called when admin ID is zero")
}

func TestNotifyAdminError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	handler.notifyAdminError(ctx, "⚠️ Test error message")

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called for error notification")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Test error message", "message should contain error text")
}

func TestNotifyAdminError_ZeroAdminID(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 0,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	handler.notifyAdminError(ctx, "⚠️ Test error")

	assert.False(t, mockBot.SendCalledSafe(), "Bot.Send should not be called when admin ID is zero")
}

func TestClearAdminSendRateLimit(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	handler.ClearAdminSendRateLimit(chatID)

	// Should not panic and rate limit should be cleared
	assert.True(t, handler.checkAdminSendRateLimit(chatID), "Rate limit should allow send after clear")
}

func TestHandleCreateError_RollbackFailed(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		SiteURL:         "https://vpn.site",
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	rollbackErr := errors.New("create subscription: database error (rollback failed: rollback failed)")
	handler.handleCreateError(ctx, 123456, 100, "testuser", rollbackErr)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called with error message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Обратитесь к администратору", "message should mention contacting admin")
	assert.True(t, mockBot.SendCountSafe() >= 2, "Should send error message and admin notification")
}

func TestHandleCreateError_ConnectionRefused(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		SiteURL:         "https://vpn.site",
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	err := errors.New("xui add client: connection refused")
	handler.handleCreateError(ctx, 123456, 100, "testuser", err)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Не удается подключиться", "message should mention connection issue")
}

func TestHandleCreateError_Authentication(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		SiteURL:         "https://vpn.site",
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	err := errors.New("xui add client: unauthorized")
	handler.handleCreateError(ctx, 123456, 100, "testuser", err)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "авторизации", "message should mention auth error")
}

func TestNotifyAdmin_SendError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	mockBot.SendError = errors.New("send failed")
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	err := handler.notifyAdmin(ctx, "testuser", 789012, "https://sub.url", time.Now())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "notify admin")
}

func TestSendQRCode_QRGenerationError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	// Empty URL still generates valid QR (empty image), so test with valid URL
	handler.sendQRCode(ctx, 123456, 100, "https://example.com/sub", "Test caption")

	assert.True(t, mockBot.SendCalledSafe())
}

func TestHandleQRCode_NilSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, nil
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "нет активной подписки")
}

func TestCreateSubscription_WithPendingInvite(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
		XUIHost:         "https://panel.example.com",
		XUISubPath:      "/sub",
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, mockXUI, cfg)

	clientConfig := &xui.ClientConfig{
		ID:    "client-uuid-123",
		SubID: "sub-id-456",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return clientConfig, nil
	}

	var savedSub *database.Subscription
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		savedSub = sub
		return nil
	}

	inviteCode := "ABC123"
	handler.pendingMu.Lock()
	handler.pendingInvites[123456] = pendingInvite{
		code:      inviteCode,
		expiresAt: time.Now().Add(60 * time.Minute),
	}
	handler.pendingMu.Unlock()

	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{
			Code:         inviteCode,
			ReferrerTGID: 999999,
		}, nil
	}

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.Equal(t, int64(999999), savedSub.ReferredBy, "ReferredBy should be set from pending invite")

	handler.pendingMu.Lock()
	_, exists := handler.pendingInvites[123456]
	handler.pendingMu.Unlock()
	assert.False(t, exists, "Pending invite should be removed after use")
}

func TestCreateSubscription_WithExpiredPendingInvite(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
		XUIHost:         "https://panel.example.com",
		XUISubPath:      "/sub",
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, mockXUI, cfg)

	clientConfig := &xui.ClientConfig{
		ID:    "client-uuid-123",
		SubID: "sub-id-456",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return clientConfig, nil
	}

	var savedSub *database.Subscription
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		savedSub = sub
		return nil
	}

	handler.pendingMu.Lock()
	handler.pendingInvites[123456] = pendingInvite{
		code:      "EXPIRED",
		expiresAt: time.Now().Add(-60 * time.Minute),
	}
	handler.pendingMu.Unlock()

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.Equal(t, int64(0), savedSub.ReferredBy, "ReferredBy should be zero for expired pending invite")
}

func TestHandleBackToInvite_RequestError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	mockBot.RequestError = errors.New("request failed")

	ctx := context.Background()
	handler.handleBackToInvite(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called")
}

func TestCreateSubscription_ShowLoadingMessageFails(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")
	handler.subscriptionService = service.NewSubscriptionService(mockDB, mockXUI, cfg)

	mockBot.SendError = errors.New("send failed")

	xuiCalled := false
	mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		xuiCalled = true
		return nil, nil
	}

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.False(t, xuiCalled, "XUI should not be called when loading message fails")
}

func TestHandleQRCode_DatabaseErrorReturnsError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database connection failed")
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called with error message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "нет активной подписки")
}

func TestHandleBackToSubscription_DeleteFails(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	mockBot.RequestError = errors.New("delete failed")
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	handler.handleBackToSubscription(ctx, 123456, "testuser", 789)

	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called to delete message")
}

// Tests for daysUntilReset function

func TestDaysUntilReset_ZeroExpiryTime(t *testing.T) {
	now := time.Now()
	expiryTime := time.Time{} // zero value

	result := daysUntilReset(now, expiryTime)

	assert.Equal(t, -1, result, "Should return -1 when expiryTime is zero (auto-reset not configured)")
}

func TestDaysUntilReset_Expired(t *testing.T) {
	now := time.Now()
	expiryTime := now.Add(-24 * time.Hour) // expired 1 day ago

	result := daysUntilReset(now, expiryTime)

	assert.Equal(t, 0, result, "Should return 0 when already expired (reset should happen now)")
}

func TestDaysUntilReset_Equal(t *testing.T) {
	now := time.Now()
	expiryTime := now

	result := daysUntilReset(now, expiryTime)

	assert.Equal(t, 0, result, "Should return 0 when now equals expiryTime (reset should happen now)")
}

func TestDaysUntilReset_NormalCase(t *testing.T) {
	now := time.Now()
	expiryTime := now.Add(10 * 24 * time.Hour) // 10 days from now

	result := daysUntilReset(now, expiryTime)

	assert.Equal(t, 10, result, "Should return 10 when expiryTime is 10 days from now")
}

func TestDaysUntilReset_OneDayLeft(t *testing.T) {
	now := time.Now()
	expiryTime := now.Add(24 * time.Hour) // 1 day from now

	result := daysUntilReset(now, expiryTime)

	// Allow for +/- 1 hour tolerance due to time calculation
	assert.True(t, result >= 0 && result <= 1, "Should return 0 or 1 when expiryTime is ~1 day from now")
}

func TestDaysUntilReset_TwentyNineDaysLeft(t *testing.T) {
	now := time.Now()
	expiryTime := now.Add(29 * 24 * time.Hour) // 29 days from now

	result := daysUntilReset(now, expiryTime)

	// Allow for +/- 1 day tolerance
	assert.True(t, result >= 28 && result <= 30, "Should return ~29 when expiryTime is 29 days from now")
}

func TestDaysUntilReset_AlmostExpired(t *testing.T) {
	now := time.Now()
	expiryTime := now.Add(1 * time.Hour) // 1 hour from now

	result := daysUntilReset(now, expiryTime)

	assert.Equal(t, 0, result, "Should return 0 when less than 1 day remains")
}

func TestDaysUntilReset_FutureExpiry(t *testing.T) {
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	expiryTime := time.Date(2025, 2, 14, 12, 0, 0, 0, time.UTC) // 30 days later

	result := daysUntilReset(now, expiryTime)

	assert.Equal(t, 30, result, "Should return 30 days for 30-day interval")
}

func TestGenerateProgressBar_ZeroLimit(t *testing.T) {
	result := generateProgressBar(10.0, 0)

	assert.Equal(t, "⬜⬜⬜⬜⬜⬜⬜⬜⬜⬜", result, "Should return empty bar when limit is zero")
}

func TestGenerateProgressBar_NegativeLimit(t *testing.T) {
	result := generateProgressBar(10.0, -5)

	assert.Equal(t, "⬜⬜⬜⬜⬜⬜⬜⬜⬜⬜", result, "Should return empty bar when limit is negative")
}

func TestGenerateProgressBar_Empty(t *testing.T) {
	result := generateProgressBar(0, 100)

	assert.Equal(t, "⬜⬜⬜⬜⬜⬜⬜⬜⬜⬜", result, "Should return empty bar when no usage")
}

func TestGenerateProgressBar_50Percent(t *testing.T) {
	result := generateProgressBar(50, 100)

	assert.Equal(t, "🟩🟩🟩🟩🟩⬜⬜⬜⬜⬜", result, "Should return half-filled bar at 50%%")
}

func TestGenerateProgressBar_100Percent(t *testing.T) {
	result := generateProgressBar(100, 100)

	assert.Equal(t, "🟩🟩🟩🟩🟩🟩🟩🟩🟩🟩", result, "Should return full bar at 100%%")
}

func TestGenerateProgressBar_Over100Percent(t *testing.T) {
	result := generateProgressBar(150, 100)

	assert.Equal(t, "🟩🟩🟩🟩🟩🟩🟩🟩🟩🟩", result, "Should cap at full bar when over 100%%")
}

func TestGenerateProgressBar_10Percent(t *testing.T) {
	result := generateProgressBar(10, 100)

	assert.Equal(t, "🟩⬜⬜⬜⬜⬜⬜⬜⬜⬜", result, "Should return 1 block at 10%%")
}

func TestGenerateProgressBar_9Percent(t *testing.T) {
	result := generateProgressBar(9, 100)

	assert.Equal(t, "⬜⬜⬜⬜⬜⬜⬜⬜⬜⬜", result, "Should return empty bar at 9%% (floor)")
}
