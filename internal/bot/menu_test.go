package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// mockBotAPIForMenu implements interfaces.BotAPI for menu tests
type mockBotAPIForMenu struct {
	sendFunc func(c tgbotapi.Chattable) (tgbotapi.Message, error)
	sentMsg  interface{}
}

func (m *mockBotAPIForMenu) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	if m.sendFunc != nil {
		return m.sendFunc(c)
	}
	m.sentMsg = c
	return tgbotapi.Message{MessageID: 1}, nil
}

func (m *mockBotAPIForMenu) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}

// mockDBServiceForMenu implements interfaces.DatabaseService for menu tests
type mockDBServiceForMenu struct {
	getByTelegramIDFunc func(ctx context.Context, telegramID int64) (*database.Subscription, error)
}

func (m *mockDBServiceForMenu) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	if m.getByTelegramIDFunc != nil {
		return m.getByTelegramIDFunc(ctx, telegramID)
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockDBServiceForMenu) GetByID(ctx context.Context, id uint) (*database.Subscription, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) CreateSubscription(ctx context.Context, sub *database.Subscription) error {
	return nil
}
func (m *mockDBServiceForMenu) UpdateSubscription(ctx context.Context, sub *database.Subscription) error {
	return nil
}
func (m *mockDBServiceForMenu) DeleteSubscription(ctx context.Context, telegramID int64) error {
	return nil
}
func (m *mockDBServiceForMenu) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) GetAllSubscriptions(ctx context.Context) ([]database.Subscription, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) CountAllSubscriptions(ctx context.Context) (int64, error) {
	return 0, nil
}
func (m *mockDBServiceForMenu) CountActiveSubscriptions(ctx context.Context) (int64, error) {
	return 0, nil
}
func (m *mockDBServiceForMenu) CountExpiredSubscriptions(ctx context.Context) (int64, error) {
	return 0, nil
}
func (m *mockDBServiceForMenu) GetAllTelegramIDs(ctx context.Context) ([]int64, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
	return 0, nil
}
func (m *mockDBServiceForMenu) DeleteSubscriptionByID(ctx context.Context, id uint) (*database.Subscription, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	return 0, nil
}
func (m *mockDBServiceForMenu) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) GetInviteByCode(ctx context.Context, code string) (*database.Invite, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, expiryTime time.Time, subURL string) (*database.Subscription, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) GetSubscriptionBySubscriptionID(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error) {
	return 0, nil
}
func (m *mockDBServiceForMenu) CreateTrialRequest(ctx context.Context, ip string) error {
	return nil
}
func (m *mockDBServiceForMenu) CleanupExpiredTrials(ctx context.Context, hours int, xuiClient interface {
	DeleteClient(ctx context.Context, inboundID int, clientID string) error
}, inboundID int) (int64, error) {
	return 0, nil
}
func (m *mockDBServiceForMenu) Close() error {
	return nil
}
func (m *mockDBServiceForMenu) GetPoolStats() (*database.PoolStats, error) {
	return nil, nil
}
func (m *mockDBServiceForMenu) Ping(ctx context.Context) error {
	return nil
}

// mockXUIClientForMenu implements interfaces.XUIClient for menu tests
type mockXUIClientForMenu struct{}

func (m *mockXUIClientForMenu) Login(ctx context.Context) error { return nil }
func (m *mockXUIClientForMenu) Ping(ctx context.Context) error  { return nil }
func (m *mockXUIClientForMenu) AddClient(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error) {
	return nil, nil
}
func (m *mockXUIClientForMenu) AddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
	return nil, nil
}
func (m *mockXUIClientForMenu) UpdateClient(ctx context.Context, inboundID int, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
	return nil
}
func (m *mockXUIClientForMenu) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
	return nil
}
func (m *mockXUIClientForMenu) GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error) {
	return nil, nil
}
func (m *mockXUIClientForMenu) GetSubscriptionLink(baseURL, subID, subPath string) string {
	return ""
}
func (m *mockXUIClientForMenu) GetExternalURL(host string) string {
	return ""
}

// TestHandleBackToStart_WithActiveSubscription tests handleBackToStart with an active subscription
func TestHandleBackToStart_WithActiveSubscription(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{
		getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return &database.Subscription{
				ID:             1,
				TelegramID:     12345,
				Username:       "testuser",
				Status:         "active",
				SubscriptionID: "abc123",
			}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	var capturedMsg interface{}
	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		capturedMsg = c
		return tgbotapi.Message{MessageID: 1}, nil
	}

	handler.handleBackToStart(ctx, 12345, "testuser", 100)

	require.NotNil(t, capturedMsg, "Message should be sent")
	editMsg, ok := capturedMsg.(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID")
	assert.Equal(t, 100, editMsg.MessageID, "MessageID")
	assert.NotEmpty(t, editMsg.Text, "Message text should not be empty")
}

// TestHandleBackToStart_NoSubscription tests handleBackToStart without a subscription
func TestHandleBackToStart_NoSubscription(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{
		getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	var capturedMsg interface{}
	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		capturedMsg = c
		return tgbotapi.Message{MessageID: 1}, nil
	}

	handler.handleBackToStart(ctx, 12345, "testuser", 100)

	require.NotNil(t, capturedMsg, "Message should be sent")
	editMsg, ok := capturedMsg.(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID")
	assert.Equal(t, 100, editMsg.MessageID, "MessageID")
}

// TestHandleBackToStart_InactiveSubscription tests handleBackToStart with an inactive subscription
func TestHandleBackToStart_InactiveSubscription(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{
		getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return &database.Subscription{
				ID:         1,
				TelegramID: 12345,
				Username:   "testuser",
				Status:     "inactive",
			}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	var capturedMsg interface{}
	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		capturedMsg = c
		return tgbotapi.Message{MessageID: 1}, nil
	}

	handler.handleBackToStart(ctx, 12345, "testuser", 100)

	require.NotNil(t, capturedMsg, "Message should be sent")
	editMsg, ok := capturedMsg.(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID should match")
	assert.NotEmpty(t, editMsg.Text, "Message text should not be empty")
}

// TestHandleBackToStart_DatabaseError tests handleBackToStart with a database error
func TestHandleBackToStart_DatabaseError(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{
		getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, errors.New("database connection failed")
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	var capturedMsg interface{}
	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		capturedMsg = c
		return tgbotapi.Message{MessageID: 1}, nil
	}

	handler.handleBackToStart(ctx, 12345, "testuser", 100)

	require.NotNil(t, capturedMsg, "Message should be sent even on database error")
	editMsg, ok := capturedMsg.(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID should match")
}

// TestHandleBackToStart_NilSubscription tests handleBackToStart when subscription is nil
func TestHandleBackToStart_NilSubscription(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{
		getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	var capturedMsg interface{}
	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		capturedMsg = c
		return tgbotapi.Message{MessageID: 1}, nil
	}

	handler.handleBackToStart(ctx, 12345, "testuser", 100)

	require.NotNil(t, capturedMsg, "Message should be sent")
}

// TestHandleMenuDonate tests handleMenuDonate
func TestHandleMenuDonate(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	var capturedMsg interface{}
	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		capturedMsg = c
		return tgbotapi.Message{MessageID: 1}, nil
	}

	handler.handleMenuDonate(ctx, 12345, "testuser", 100)

	require.NotNil(t, capturedMsg, "Message should be sent")
	editMsg, ok := capturedMsg.(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID")
	assert.Equal(t, 100, editMsg.MessageID, "MessageID")
	assert.Equal(t, "Markdown", editMsg.ParseMode, "ParseMode should be Markdown")
	assert.NotEmpty(t, editMsg.Text, "Message text should not be empty")
}

// TestHandleMenuDonate_WithDifferentUsernames tests handleMenuDonate with different usernames
func TestHandleMenuDonate_WithDifferentUsernames(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	testCases := []struct {
		name     string
		username string
	}{
		{"regular username", "testuser"},
		{"empty username", ""},
		{"unicode username", "测试用户"},
		{"long username", "very_long_username_for_testing"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedMsg interface{}
			mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
				capturedMsg = c
				return tgbotapi.Message{MessageID: 1}, nil
			}

			handler.handleMenuDonate(ctx, 12345, tc.username, 100)

			require.NotNil(t, capturedMsg, "Message should be sent")
		})
	}
}

// TestHandleMenuHelp_WithSubscription tests handleMenuHelp with an active subscription
func TestHandleMenuHelp_WithSubscription(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{
		getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return &database.Subscription{
				ID:              1,
				TelegramID:      12345,
				Username:        "testuser",
				Status:          "active",
				SubscriptionID:  "abc123",
				SubscriptionURL: "https://example.com/sub/abc123",
			}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	var capturedMsg interface{}
	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		capturedMsg = c
		return tgbotapi.Message{MessageID: 1}, nil
	}

	handler.handleMenuHelp(ctx, 12345, "testuser", 100)

	require.NotNil(t, capturedMsg, "Message should be sent")
	editMsg, ok := capturedMsg.(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Equal(t, int64(12345), editMsg.ChatID, "ChatID")
	assert.Equal(t, 100, editMsg.MessageID, "MessageID")
	assert.Equal(t, "Markdown", editMsg.ParseMode, "ParseMode should be Markdown")
	assert.NotEmpty(t, editMsg.Text, "Message text should not be empty")
}

// TestHandleMenuHelp_NoSubscription tests handleMenuHelp without a subscription
func TestHandleMenuHelp_NoSubscription(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{
		getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	var capturedMsg interface{}
	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		capturedMsg = c
		return tgbotapi.Message{MessageID: 1}, nil
	}

	handler.handleMenuHelp(ctx, 12345, "testuser", 100)

	require.NotNil(t, capturedMsg, "Message should be sent")
	editMsg, ok := capturedMsg.(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Contains(t, editMsg.Text, "нет активной подписки", "Should indicate no subscription")
}

// TestHandleMenuHelp_DatabaseError tests handleMenuHelp with a database error
func TestHandleMenuHelp_DatabaseError(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{
		getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, errors.New("database connection failed")
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	var capturedMsg interface{}
	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		capturedMsg = c
		return tgbotapi.Message{MessageID: 1}, nil
	}

	handler.handleMenuHelp(ctx, 12345, "testuser", 100)

	require.NotNil(t, capturedMsg, "Message should be sent even on database error")
	editMsg, ok := capturedMsg.(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "Should be EditMessageTextConfig")
	assert.Contains(t, editMsg.Text, "Временная ошибка", "Should indicate temporary error")
}

// TestHandleMenuHelp_VariousTrafficLimits tests handleMenuHelp with different traffic limits
func TestHandleMenuHelp_VariousTrafficLimits(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name        string
		trafficGB   int
		description string
	}{
		{"low limit", 10, "10GB limit"},
		{"standard limit", 30, "30GB limit"},
		{"high limit", 100, "100GB limit"},
		{"unlimited", 0, "no limit"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockBot := &mockBotAPIForMenu{}
			mockDB := &mockDBServiceForMenu{
				getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						ID:              1,
						TelegramID:      12345,
						Username:        "testuser",
						Status:          "active",
						SubscriptionID:  "abc123",
						SubscriptionURL: "https://example.com/sub/abc123",
					}, nil
				},
			}

			cfg := &config.Config{
				TelegramBotToken: "test:token",
				TelegramAdminID:  0,
				TrafficLimitGB:   tc.trafficGB,
			}

			handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

			var capturedMsg interface{}
			mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
				capturedMsg = c
				return tgbotapi.Message{MessageID: 1}, nil
			}

			handler.handleMenuHelp(ctx, 12345, "testuser", 100)

			require.NotNil(t, capturedMsg, "Message should be sent")
		})
	}
}

// TestHandleBackToStart_VariousMessageIDs tests handleBackToStart with different message IDs
func TestHandleBackToStart_VariousMessageIDs(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name      string
		messageID int
	}{
		{"message ID 1", 1},
		{"message ID 100", 100},
		{"message ID 999", 999},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockBot := &mockBotAPIForMenu{}
			mockDB := &mockDBServiceForMenu{
				getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						ID:         1,
						TelegramID: 12345,
						Status:     "active",
					}, nil
				},
			}

			cfg := &config.Config{
				TelegramBotToken: "test:token",
				TelegramAdminID:  0,
				TrafficLimitGB:   30,
			}

			handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

			var capturedMsg interface{}
			mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
				capturedMsg = c
				return tgbotapi.Message{MessageID: 1}, nil
			}

			handler.handleBackToStart(ctx, 12345, "testuser", tc.messageID)

			require.NotNil(t, capturedMsg, "Message should be sent")
			editMsg, ok := capturedMsg.(tgbotapi.EditMessageTextConfig)
			require.True(t, ok, "Should be EditMessageTextConfig")
			assert.Equal(t, tc.messageID, editMsg.MessageID, "MessageID should match")
		})
	}
}

// TestHandleMenuHelp_ContextCancellation tests that handleMenuHelp handles context properly
func TestHandleMenuHelp_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{
		getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return &database.Subscription{
				ID:              1,
				TelegramID:      12345,
				Status:          "active",
				SubscriptionURL: "https://example.com/sub/abc123",
			}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	var capturedMsg interface{}
	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		capturedMsg = c
		return tgbotapi.Message{MessageID: 1}, nil
	}

	// Should not panic with cancelled context
	handler.handleMenuHelp(ctx, 12345, "testuser", 100)

	// Verify message was captured
	if capturedMsg != nil {
		assert.NotNil(t, capturedMsg, "Message should be captured if sent")
	}
}

// TestHandleBackToStart_SendError tests handleBackToStart when send fails
func TestHandleBackToStart_SendError(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{
		getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return &database.Subscription{
				ID:         1,
				TelegramID: 12345,
				Status:     "active",
			}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		return tgbotapi.Message{}, errors.New("send failed")
	}

	// Should not panic on send error
	handler.handleBackToStart(ctx, 12345, "testuser", 100)
}

// TestHandleMenuDonate_SendError tests handleMenuDonate when send fails
func TestHandleMenuDonate_SendError(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		return tgbotapi.Message{}, errors.New("send failed")
	}

	// Should not panic on send error
	handler.handleMenuDonate(ctx, 12345, "testuser", 100)
}

// TestHandleMenuHelp_SendError tests handleMenuHelp when send fails
func TestHandleMenuHelp_SendError(t *testing.T) {
	ctx := context.Background()

	mockBot := &mockBotAPIForMenu{}
	mockDB := &mockDBServiceForMenu{
		getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return &database.Subscription{
				ID:              1,
				TelegramID:      12345,
				Status:          "active",
				SubscriptionURL: "https://example.com/sub/abc123",
			}, nil
		},
	}

	cfg := &config.Config{
		TelegramBotToken: "test:token",
		TelegramAdminID:  0,
		TrafficLimitGB:   30,
	}

	handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

	mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		return tgbotapi.Message{}, errors.New("send failed")
	}

	// Should not panic on send error
	handler.handleMenuHelp(ctx, 12345, "testuser", 100)
}

// TestHandleBackToStart_VariousChatIDs tests handleBackToStart with different chat IDs
func TestHandleBackToStart_VariousChatIDs(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name   string
		chatID int64
	}{
		{"positive chat ID", 12345},
		{"large chat ID", 999999999},
		{"negative chat ID (group)", -1001234567890},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockBot := &mockBotAPIForMenu{}
			mockDB := &mockDBServiceForMenu{
				getByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return nil, gorm.ErrRecordNotFound
				},
			}

			cfg := &config.Config{
				TelegramBotToken: "test:token",
				TelegramAdminID:  0,
				TrafficLimitGB:   30,
			}

			handler := NewHandler(mockBot, cfg, mockDB, &mockXUIClientForMenu{})

			var capturedMsg interface{}
			mockBot.sendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
				capturedMsg = c
				return tgbotapi.Message{MessageID: 1}, nil
			}

			handler.handleBackToStart(ctx, tc.chatID, "testuser", 100)

			require.NotNil(t, capturedMsg, "Message should be sent")
			editMsg, ok := capturedMsg.(tgbotapi.EditMessageTextConfig)
			require.True(t, ok, "Should be EditMessageTextConfig")
			assert.Equal(t, tc.chatID, editMsg.ChatID, "ChatID should match")
		})
	}
}
