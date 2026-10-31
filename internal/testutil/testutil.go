// Package testutil provides reusable fakes and fixtures for repository and bot tests.
package testutil

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

var ErrRecordNotFound = gorm.ErrRecordNotFound

func PtrString(v string) *string {
	return new(v)
}

func PtrInt64(v int64) *int64 {
	return new(v)
}

func PtrTime(t time.Time) *time.Time {
	return new(t)
}

const (
	DefaultTelegramID = int64(123456789)
	DefaultUsername   = "testuser"
	DefaultTrafficGB  = 100
	AdminTelegramID   = int64(999999)
)

func InitLogger(t any) error {
	_, err := logger.Init("", "error")
	return err
}

func NewTestDatabaseService(t any) (*database.Service, error) {
	type testInterface interface {
		TempDir() string
	}

	var tmpDir string
	if ti, ok := t.(testInterface); ok {
		tmpDir = ti.TempDir()
	} else {
		tmpDir = "/tmp"
	}

	dbPath := filepath.Join(tmpDir, "test_service.db")

	return database.NewService(dbPath)
}

func CreateTestSubscription(telegramID int64, username string, status string, expiry *time.Time) *database.Subscription {
	// PlanID 2 is the seeded free plan: NewService seeds trial then free
	// (ids 1, 2), and per-consumer fakes mirror this via their storage maps. The test
	// DSN enables SQLite foreign-key enforcement, so a subscription must
	// reference an existing plan to be insertable into a real database.
	return &database.Subscription{
		TelegramID:     telegramID,
		Username:       username,
		ClientID:       "test-client-id-" + username,
		SubscriptionID: username,
		ExpiresAt:      expiry,
		Status:         status,
		PlanID:         2,
	}
}

type XUIClient struct {
	mu                      sync.Mutex
	PingFunc                func(ctx context.Context) error
	AddClientFunc           func(ctx context.Context, inboundIDs []int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error)
	AddClientWithIDFunc     func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error)
	UpdateClientFunc        func(ctx context.Context, req xui.ClientRequest) error
	DeleteClientFunc        func(ctx context.Context, email string) error
	GetClientTrafficFunc    func(ctx context.Context, email string) (*xui.ClientTraffic, error)
	GetClientByEmailFunc    func(ctx context.Context, email string) (*xui.ClientRecord, error)
	GetSubscriptionLinkFunc func(host, subID, subPath string) string
	GetExternalURLFunc      func(host string) string

	// Call tracking
	AddClientCalled       bool
	AddClientWithIDCalled bool
	DeleteClientCalled    bool
	UpdateClientCalled    bool
	ResetTrafficCalled    bool
}

func (m *XUIClient) Ping(ctx context.Context) error {
	if m.PingFunc != nil {
		return m.PingFunc(ctx)
	}

	return nil
}

func (m *XUIClient) AddClient(ctx context.Context, inboundIDs []int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error) {
	m.mu.Lock()
	m.AddClientCalled = true
	m.mu.Unlock()

	if m.AddClientFunc != nil {
		return m.AddClientFunc(ctx, inboundIDs, email, trafficBytes, expiryTime)
	}

	return &xui.ClientConfig{
		ID:        "test-client-id",
		Email:     email,
		TotalGB:   trafficBytes,
		ExpiresAt: expiryTime.UnixMilli(),
		Enable:    true,
	}, nil
}

func (m *XUIClient) AddClientWithID(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
	m.mu.Lock()
	m.AddClientWithIDCalled = true
	m.mu.Unlock()

	if m.AddClientWithIDFunc != nil {
		return m.AddClientWithIDFunc(ctx, req)
	}

	return &xui.ClientConfig{
		ID:        req.ClientID,
		Email:     req.Email,
		TotalGB:   req.TrafficBytes,
		ExpiresAt: req.ExpiryTime.UnixMilli(),
		Enable:    true,
		SubID:     req.SubID,
	}, nil
}

func (m *XUIClient) UpdateClient(ctx context.Context, req xui.ClientRequest) error {
	m.mu.Lock()
	m.UpdateClientCalled = true
	m.mu.Unlock()

	if m.UpdateClientFunc != nil {
		return m.UpdateClientFunc(ctx, req)
	}

	return nil
}

func (m *XUIClient) DeleteClient(ctx context.Context, email string) error {
	m.mu.Lock()
	m.DeleteClientCalled = true
	m.mu.Unlock()

	if m.DeleteClientFunc != nil {
		return m.DeleteClientFunc(ctx, email)
	}

	return nil
}

func (m *XUIClient) ResetTraffic(_ context.Context, _ string) error {
	m.mu.Lock()
	m.ResetTrafficCalled = true
	m.mu.Unlock()

	return nil
}

func (m *XUIClient) GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error) {
	if m.GetClientTrafficFunc != nil {
		return m.GetClientTrafficFunc(ctx, email)
	}

	return &xui.ClientTraffic{
		Up:   1024 * 1024 * 100,
		Down: 1024 * 1024 * 200,
	}, nil
}

func (m *XUIClient) GetClientByEmail(ctx context.Context, email string) (*xui.ClientRecord, error) {
	if m.GetClientByEmailFunc != nil {
		return m.GetClientByEmailFunc(ctx, email)
	}

	return nil, xui.ErrClientNotFound
}

func (m *XUIClient) GetSubscriptionLink(host, subID, subPath string) string {
	if m.GetSubscriptionLinkFunc != nil {
		return m.GetSubscriptionLinkFunc(host, subID, subPath)
	}

	return host + "/" + subPath + "/" + subID
}

func (m *XUIClient) GetExternalURL(host string) string {
	if m.GetExternalURLFunc != nil {
		return m.GetExternalURLFunc(host)
	}

	return host
}

func (m *XUIClient) Close() error {
	return nil
}

func NewXUIClient() *XUIClient {
	return &XUIClient{}
}

// BotAPI is a mock implementation of the Telegram Bot API for testing.
type BotAPI struct {
	mu              sync.RWMutex
	sendCalled      bool
	requestCalled   bool
	LastSentText    string
	LastChatID      int64
	SendCount       int
	SendError       error
	RequestError    error
	LastChattable   tgbotapi.Chattable
	LastRequest     tgbotapi.Chattable
	SendFunc        func(c tgbotapi.Chattable) (tgbotapi.Message, error)
	AllSentMessages []SentMessage
	// DeletedMessageIDs captures the message ids passed to DeleteMessage.
	DeletedMessageIDs []int
}

// SentMessage represents a captured message
type SentMessage struct {
	ChatID int64
	Text   string
}

func NewBotAPI() *BotAPI {
	return &BotAPI{}
}

func (m *BotAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sendCalled = true
	m.SendCount++
	m.LastChattable = c

	var msg SentMessage

	// Extract text and chat ID from various message types
	switch v := c.(type) {
	case tgbotapi.MessageConfig:
		m.LastSentText = v.Text
		m.LastChatID = v.ChatID
		msg = SentMessage{ChatID: v.ChatID, Text: v.Text}
	case tgbotapi.EditMessageTextConfig:
		m.LastSentText = v.Text
		m.LastChatID = v.ChatID
		msg = SentMessage{ChatID: v.ChatID, Text: v.Text}
	case tgbotapi.EditMessageReplyMarkupConfig:
		m.LastChatID = v.ChatID
	case tgbotapi.DeleteMessageConfig:
		m.LastChatID = v.ChatID
	}

	m.AllSentMessages = append(m.AllSentMessages, msg)

	// Use custom send function if provided
	if m.SendFunc != nil {
		return m.SendFunc(c)
	}

	if m.SendError != nil {
		return tgbotapi.Message{}, m.SendError
	}

	return tgbotapi.Message{MessageID: 1}, nil
}

func (m *BotAPI) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requestCalled = true
	m.LastRequest = c

	if dm, ok := c.(tgbotapi.DeleteMessageConfig); ok {
		m.DeletedMessageIDs = append(m.DeletedMessageIDs, dm.MessageID)
	}

	if m.RequestError != nil {
		return nil, m.RequestError
	}

	return &tgbotapi.APIResponse{Ok: true}, nil
}

// SendCountSafe returns the number of Send calls (thread-safe).
func (m *BotAPI) SendCountSafe() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.SendCount
}

// SendCalledSafe returns whether Send was called (thread-safe).
func (m *BotAPI) SendCalledSafe() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.sendCalled
}

// RequestCalledSafe returns whether Request was called (thread-safe).
func (m *BotAPI) RequestCalledSafe() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.requestCalled
}

// LastRequestSafe returns the last Request Chattable (thread-safe).
func (m *BotAPI) LastRequestSafe() tgbotapi.Chattable {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.LastRequest
}

// DeletedMessageIDsSafe returns the captured DeleteMessage ids (thread-safe).
func (m *BotAPI) DeletedMessageIDsSafe() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]int, len(m.DeletedMessageIDs))
	copy(out, m.DeletedMessageIDs)

	return out
}

// LastSentTextSafe returns the last sent text (thread-safe).
func (m *BotAPI) LastSentTextSafe() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.LastSentText
}

// LastChattableSafe returns the last sent Chattable (thread-safe).
func (m *BotAPI) LastChattableSafe() tgbotapi.Chattable {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.LastChattable
}

// GetAllSentMessages returns all captured messages (thread-safe).
func (m *BotAPI) GetAllSentMessages() []SentMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]SentMessage, len(m.AllSentMessages))
	copy(out, m.AllSentMessages)

	return out
}

// SetSendCalled sets the sendCalled flag (thread-safe).
func (m *BotAPI) SetSendCalled(b bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sendCalled = b
}

// SetRequestCalled sets the requestCalled flag (thread-safe).
func (m *BotAPI) SetRequestCalled(b bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requestCalled = b
}

// SetSendCount sets the send count (thread-safe).
func (m *BotAPI) SetSendCount(c int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.SendCount = c
}

func (m *BotAPI) GetUpdatesChan(config tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel {
	ch := make(chan tgbotapi.Update)
	close(ch)

	return ch
}

func (m *BotAPI) StopReceivingUpdates() {
	// No-op for mock
}

func (m *BotAPI) Self() *tgbotapi.User {
	return &tgbotapi.User{
		ID:                      123456789,
		FirstName:               "TestBot",
		UserName:                "testbot",
		IsBot:                   true,
		CanJoinGroups:           false,
		CanReadAllGroupMessages: false,
		SupportsInlineQueries:   false,
	}
}
