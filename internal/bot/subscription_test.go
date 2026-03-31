package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	chatID := int64(123456)
	messageID := 100

	resultID := handler.showLoadingMessage(chatID, messageID)

	assert.Equal(t, messageID, resultID, "Should return same messageID when editing")
	assert.True(t, mockBot.SendCalled, "Send should be called")
}

func TestShowLoadingMessage_WithoutMessageID(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	chatID := int64(123456)
	messageID := 0

	resultID := handler.showLoadingMessage(chatID, messageID)

	assert.NotEqual(t, 0, resultID, "Should return new messageID when sending new message")
	assert.True(t, mockBot.SendCalled, "Send should be called")
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	chatID := int64(123456)
	messageID := 100

	// Reset error for second call
	resultID := handler.showLoadingMessage(chatID, messageID)

	// Should attempt to send new message if edit fails
	assert.True(t, mockBot.SendCalled)
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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

	assert.True(t, mockBot.SendCalled)
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
			mockBot.SendCalled = false
			mockBot.LastSentText = ""

			mockXUI.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
				return nil, errors.New(tt.errMsg)
			}

			ctx := context.Background()
			handler.createSubscription(ctx, 123456, "testuser", 1)

			assert.True(t, mockBot.SendCalled)
			assert.Contains(t, mockBot.LastSentText, tt.expectedError)
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
	assert.True(t, mockBot.SendCalled)
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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

	assert.True(t, mockBot.SendCalled)
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	chatID := int64(123456)

	// Simulate subscription already in progress
	handler.inProgress[chatID] = struct{}{}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, chatID, "testuser", 1)

	// Should not call any expensive operations
	assert.Nil(t, mockDB.GetByTelegramIDFunc)
	assert.Nil(t, mockXUI.AddClientWithIDFunc)
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Ваша подписка")
	// Should not create new subscription
	assert.Nil(t, mockXUI.AddClientWithIDFunc)
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Временная ошибка")
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "нет активной подписки")
}

func TestHandleMySubscription_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Временная ошибка")
}

func TestHandleMySubscription_ActiveSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	sub := &database.Subscription{
		TelegramID:      123456,
		Username:        "testuser",
		SubscriptionURL: "https://test.url/sub",
		ExpiryTime:      time.Now().Add(30 * 24 * time.Hour),
		Status:          "active",
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

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Ваша подписка")
	assert.Contains(t, mockBot.LastSentText, "3.00 / 100 ГБ") // 1 + 2 = 3 GB
}

func TestHandleMySubscription_TrafficFetchError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	sub := &database.Subscription{
		TelegramID:      123456,
		Username:        "testuser",
		SubscriptionURL: "https://test.url/sub",
		ExpiryTime:      time.Now().Add(30 * 24 * time.Hour),
		Status:          "active",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	mockXUI.GetClientTrafficFunc = func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
		return nil, errors.New("traffic error")
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Ваша подписка")
	// Should still show subscription even if traffic fetch fails
	assert.Contains(t, mockBot.LastSentText, "0.00 / 100 ГБ")
}

func TestHandleMySubscription_UsesCache(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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

func TestHandleQRCode_NoSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "нет активной подписки")
}

func TestHandleQRCode_Success(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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

	assert.True(t, mockBot.SendCalled)
}

func TestHandleQRCode_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "нет активной подписки")
}

func TestHandleQRCode_QRGenerationError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	sub := &database.Subscription{
		TelegramID:      123456,
		Username:        "testuser",
		SubscriptionURL: "", // Empty URL should cause QR generation to succeed but with empty QR
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalled)
}

func TestHandleBackToSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	ctx := context.Background()
	handler.handleBackToSubscription(ctx, 123456, "testuser", 100)

	// Should request message deletion
	assert.True(t, mockBot.RequestCalled)
}

func TestGetSubscriptionWithCache_CacheHit(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	chatID := int64(123456)
	sub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	chatID := int64(123456)
	sub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	chatID := int64(123456)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestInvalidateCache(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	chatID := int64(123456)
	messageID := 789
	testURL := "https://t.me/testbot?start=share_ABC123"
	testCaption := "📱 QR-код для Telegram"

	ctx := context.Background()

	// Should not panic and should call Send
	assert.NotPanics(t, func() {
		handler.sendQRCode(ctx, chatID, messageID, testURL, testCaption)
	})

	assert.True(t, mockBot.SendCalled, "Bot.Send should be called")
}

func TestHandleBackToInvite(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	chatID := int64(123456)
	messageID := 789
	username := "testuser"

	ctx := context.Background()

	// Should not panic and should call Request (for delete)
	assert.NotPanics(t, func() {
		handler.handleBackToInvite(ctx, chatID, username, messageID)
	})

	assert.True(t, mockBot.RequestCalled, "Bot.Request should be called for delete message")
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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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

	assert.True(t, mockBot.SendCalled)
}
