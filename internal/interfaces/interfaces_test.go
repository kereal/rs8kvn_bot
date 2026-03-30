package interfaces

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/xui"
)

type mockDatabaseService struct {
	subscriptions       map[int64]*database.Subscription
	invites             map[string]*database.Invite
	trialRequests       []*database.TrialRequest
	getByIDErr          error
	pingErr             error
	createTrialSubErr   error
	bindTrialSubErr     error
	cleanupExpiredCount int64
}

func (m *mockDatabaseService) GetByTelegramID(ctx context.Context, telegramID int64) (*database.Subscription, error) {
	if sub, ok := m.subscriptions[telegramID]; ok {
		return sub, nil
	}
	return nil, nil
}

func (m *mockDatabaseService) GetByID(ctx context.Context, id uint) (*database.Subscription, error) {
	return nil, m.getByIDErr
}

func (m *mockDatabaseService) CreateSubscription(ctx context.Context, sub *database.Subscription) error {
	m.subscriptions[sub.TelegramID] = sub
	return nil
}

func (m *mockDatabaseService) UpdateSubscription(ctx context.Context, sub *database.Subscription) error {
	m.subscriptions[sub.TelegramID] = sub
	return nil
}

func (m *mockDatabaseService) DeleteSubscription(ctx context.Context, telegramID int64) error {
	delete(m.subscriptions, telegramID)
	return nil
}

func (m *mockDatabaseService) GetLatestSubscriptions(ctx context.Context, limit int) ([]database.Subscription, error) {
	var result []database.Subscription
	for _, sub := range m.subscriptions {
		if sub.Status == "active" {
			result = append(result, *sub)
		}
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (m *mockDatabaseService) GetAllSubscriptions(ctx context.Context) ([]database.Subscription, error) {
	var result []database.Subscription
	for _, sub := range m.subscriptions {
		result = append(result, *sub)
	}
	return result, nil
}

func (m *mockDatabaseService) CountActiveSubscriptions(ctx context.Context) (int64, error) {
	var count int64
	for _, sub := range m.subscriptions {
		if sub.Status == "active" && !sub.IsExpired() {
			count++
		}
	}
	return count, nil
}

func (m *mockDatabaseService) CountExpiredSubscriptions(ctx context.Context) (int64, error) {
	var count int64
	for _, sub := range m.subscriptions {
		if sub.Status == "active" && sub.IsExpired() {
			count++
		}
	}
	return count, nil
}

func (m *mockDatabaseService) GetAllTelegramIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	for id := range m.subscriptions {
		ids = append(ids, id)
	}
	return ids, nil
}

func (m *mockDatabaseService) GetTelegramIDByUsername(ctx context.Context, username string) (int64, error) {
	for _, sub := range m.subscriptions {
		if sub.Username == username {
			return sub.TelegramID, nil
		}
	}
	return 0, nil
}

func (m *mockDatabaseService) DeleteSubscriptionByID(ctx context.Context, id uint) (*database.Subscription, error) {
	return nil, nil
}

func (m *mockDatabaseService) Ping(ctx context.Context) error {
	return m.pingErr
}

func (m *mockDatabaseService) CountAllSubscriptions(ctx context.Context) (int64, error) {
	return int64(len(m.subscriptions)), nil
}

func (m *mockDatabaseService) GetOrCreateInvite(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
	if m.invites == nil {
		m.invites = make(map[string]*database.Invite)
	}
	if invite, ok := m.invites[code]; ok {
		return invite, nil
	}
	invite := &database.Invite{
		Code:         code,
		ReferrerTGID: referrerTGID,
	}
	m.invites[code] = invite
	return invite, nil
}

func (m *mockDatabaseService) GetInviteByCode(ctx context.Context, code string) (*database.Invite, error) {
	if m.invites == nil {
		return nil, nil
	}
	return m.invites[code], nil
}

func (m *mockDatabaseService) CreateTrialSubscription(ctx context.Context, inviteCode, subscriptionID, clientID string, inboundID int, trafficBytes int64, expiryTime time.Time, subURL string) (*database.Subscription, error) {
	if m.createTrialSubErr != nil {
		return nil, m.createTrialSubErr
	}
	sub := &database.Subscription{
		SubscriptionID:  subscriptionID,
		ClientID:        clientID,
		InboundID:       inboundID,
		TrafficLimit:    trafficBytes,
		ExpiryTime:      expiryTime,
		SubscriptionURL: subURL,
		InviteCode:      inviteCode,
		IsTrial:         true,
		Status:          "active",
	}
	return sub, nil
}

func (m *mockDatabaseService) GetSubscriptionBySubscriptionID(ctx context.Context, subscriptionID string) (*database.Subscription, error) {
	for _, sub := range m.subscriptions {
		if sub.SubscriptionID == subscriptionID {
			return sub, nil
		}
	}
	return nil, nil
}

func (m *mockDatabaseService) BindTrialSubscription(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
	if m.bindTrialSubErr != nil {
		return nil, m.bindTrialSubErr
	}
	for _, sub := range m.subscriptions {
		if sub.SubscriptionID == subscriptionID {
			sub.TelegramID = telegramID
			sub.Username = username
			sub.IsTrial = false
			return sub, nil
		}
	}
	return nil, fmt.Errorf("trial subscription not found")
}

func (m *mockDatabaseService) CountTrialRequestsByIPLastHour(ctx context.Context, ip string) (int, error) {
	if m.trialRequests == nil {
		return 0, nil
	}
	count := 0
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	for _, req := range m.trialRequests {
		if req.IP == ip && req.CreatedAt.After(oneHourAgo) {
			count++
		}
	}
	return count, nil
}

func (m *mockDatabaseService) CreateTrialRequest(ctx context.Context, ip string) error {
	if m.trialRequests == nil {
		m.trialRequests = []*database.TrialRequest{}
	}
	m.trialRequests = append(m.trialRequests, &database.TrialRequest{
		IP:        ip,
		CreatedAt: time.Now(),
	})
	return nil
}

func (m *mockDatabaseService) CleanupExpiredTrials(ctx context.Context, hours int, xuiClient interface {
	DeleteClient(ctx context.Context, inboundID int, clientID string) error
}, inboundID int) (int64, error) {
	return m.cleanupExpiredCount, nil
}

func (m *mockDatabaseService) GetTelegramIDsBatch(ctx context.Context, offset, limit int) ([]int64, error) {
	var ids []int64
	for id := range m.subscriptions {
		ids = append(ids, id)
	}
	if offset >= len(ids) {
		return []int64{}, nil
	}
	end := offset + limit
	if end > len(ids) {
		end = len(ids)
	}
	return ids[offset:end], nil
}

func (m *mockDatabaseService) GetTotalTelegramIDCount(ctx context.Context) (int64, error) {
	return int64(len(m.subscriptions)), nil
}

func (m *mockDatabaseService) Close() error {
	return nil
}

func (m *mockDatabaseService) GetPoolStats() (*database.PoolStats, error) {
	return &database.PoolStats{}, nil
}

func TestMockDatabaseService(t *testing.T) {
	svc := &mockDatabaseService{
		subscriptions: make(map[int64]*database.Subscription),
	}

	sub := &database.Subscription{
		TelegramID: 123,
		Username:   "testuser",
		Status:     "active",
		ExpiryTime: time.Now().Add(24 * time.Hour),
	}

	err := svc.CreateSubscription(context.Background(), sub)
	require.NoError(t, err, "CreateSubscription() error")

	retrieved, err := svc.GetByTelegramID(context.Background(), 123)
	require.NoError(t, err, "GetByTelegramID() error")
	require.NotNil(t, retrieved, "GetByTelegramID() returned nil")
	assert.Equal(t, "testuser", retrieved.Username, "Username")
}

func TestMockDatabaseService_GetByID(t *testing.T) {
	svc := &mockDatabaseService{
		subscriptions: make(map[int64]*database.Subscription),
	}

	_, err := svc.GetByID(context.Background(), 1)
	assert.NoError(t, err, "GetByID() error")
}

type mockXUIClient struct {
	loginErr        error
	addClientErr    error
	deleteClientErr error
	getTrafficErr   error
	updateClientErr error
	pingErr         error
	clientConfig    *xui.ClientConfig
	clientTraffic   *xui.ClientTraffic
}

func (m *mockXUIClient) Login(ctx context.Context) error {
	return m.loginErr
}

func (m *mockXUIClient) AddClient(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error) {
	if m.addClientErr != nil {
		return nil, m.addClientErr
	}
	return m.clientConfig, nil
}

func (m *mockXUIClient) AddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
	if m.addClientErr != nil {
		return nil, m.addClientErr
	}
	return m.clientConfig, nil
}

func (m *mockXUIClient) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
	return m.deleteClientErr
}

func (m *mockXUIClient) GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error) {
	return m.clientTraffic, m.getTrafficErr
}

func (m *mockXUIClient) GetSubscriptionLink(baseURL, subID, subPath string) string {
	return baseURL + "/" + subPath + "/" + subID
}

func (m *mockXUIClient) GetExternalURL(host string) string {
	return host
}

func (m *mockXUIClient) UpdateClient(ctx context.Context, inboundID int, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
	return m.updateClientErr
}

func (m *mockXUIClient) Ping(ctx context.Context) error {
	return m.pingErr
}

func TestMockXUIClient(t *testing.T) {
	client := &mockXUIClient{
		clientConfig: &xui.ClientConfig{
			ID:    "test-id",
			Email: "test@example.com",
		},
		clientTraffic: &xui.ClientTraffic{
			Up:   1000,
			Down: 2000,
		},
	}

	err := client.Login(context.Background())
	require.NoError(t, err, "Login() error")

	config, err := client.AddClient(context.Background(), 1, "test", 1000, time.Now())
	require.NoError(t, err, "AddClient() error")
	assert.Equal(t, "test-id", config.ID, "ID")

	traffic, err := client.GetClientTraffic(context.Background(), "test")
	require.NoError(t, err, "GetClientTraffic() error")
	assert.Equal(t, int64(1000), traffic.Up, "Up")
}

func TestMockXUIClient_GetSubscriptionLink(t *testing.T) {
	client := &mockXUIClient{}
	link := client.GetSubscriptionLink("http://localhost", "sub123", "sub")
	expected := "http://localhost/sub/sub123"
	assert.Equal(t, expected, link, "GetSubscriptionLink()")
}

func TestMockDatabaseService_Ping(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc := &mockDatabaseService{}
		err := svc.Ping(context.Background())
		assert.NoError(t, err, "Ping() should not error")
	})

	t.Run("error", func(t *testing.T) {
		svc := &mockDatabaseService{pingErr: fmt.Errorf("connection refused")}
		err := svc.Ping(context.Background())
		assert.Error(t, err, "Ping() should error")
	})
}

func TestMockDatabaseService_CountAllSubscriptions(t *testing.T) {
	svc := &mockDatabaseService{
		subscriptions: map[int64]*database.Subscription{
			1: {TelegramID: 1},
			2: {TelegramID: 2},
		},
	}

	count, err := svc.CountAllSubscriptions(context.Background())
	require.NoError(t, err, "CountAllSubscriptions() error")
	assert.Equal(t, int64(2), count, "CountAllSubscriptions()")
}

func TestMockDatabaseService_GetOrCreateInvite(t *testing.T) {
	t.Run("create new invite", func(t *testing.T) {
		svc := &mockDatabaseService{}
		invite, err := svc.GetOrCreateInvite(context.Background(), 123, "ABC12345")
		require.NoError(t, err, "GetOrCreateInvite() error")
		assert.Equal(t, "ABC12345", invite.Code, "Code")
		assert.Equal(t, int64(123), invite.ReferrerTGID, "ReferrerTGID")
	})

	t.Run("get existing invite", func(t *testing.T) {
		svc := &mockDatabaseService{
			invites: map[string]*database.Invite{
				"EXISTING": {Code: "EXISTING", ReferrerTGID: 999},
			},
		}
		invite, err := svc.GetOrCreateInvite(context.Background(), 123, "EXISTING")
		require.NoError(t, err, "GetOrCreateInvite() error")
		assert.Equal(t, int64(999), invite.ReferrerTGID, "Should return existing invite")
	})
}

func TestMockDatabaseService_GetInviteByCode(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		svc := &mockDatabaseService{
			invites: map[string]*database.Invite{
				"TESTCODE": {Code: "TESTCODE", ReferrerTGID: 123},
			},
		}
		invite, err := svc.GetInviteByCode(context.Background(), "TESTCODE")
		require.NoError(t, err, "GetInviteByCode() error")
		require.NotNil(t, invite, "Invite should not be nil")
		assert.Equal(t, int64(123), invite.ReferrerTGID, "ReferrerTGID")
	})

	t.Run("not found", func(t *testing.T) {
		svc := &mockDatabaseService{}
		invite, err := svc.GetInviteByCode(context.Background(), "NOTEXIST")
		require.NoError(t, err, "GetInviteByCode() error")
		assert.Nil(t, invite, "Invite should be nil")
	})
}

func TestMockDatabaseService_CreateTrialSubscription(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc := &mockDatabaseService{}
		sub, err := svc.CreateTrialSubscription(
			context.Background(),
			"INVITE1",
			"sub123",
			"client456",
			1,
			107374182400,
			time.Now().Add(24*time.Hour),
			"https://example.com/sub",
		)
		require.NoError(t, err, "CreateTrialSubscription() error")
		assert.Equal(t, "sub123", sub.SubscriptionID, "SubscriptionID")
		assert.Equal(t, "client456", sub.ClientID, "ClientID")
		assert.True(t, sub.IsTrial, "IsTrial")
	})

	t.Run("error", func(t *testing.T) {
		svc := &mockDatabaseService{createTrialSubErr: fmt.Errorf("database error")}
		_, err := svc.CreateTrialSubscription(
			context.Background(),
			"INVITE1",
			"sub123",
			"client456",
			1,
			107374182400,
			time.Now().Add(24*time.Hour),
			"https://example.com/sub",
		)
		assert.Error(t, err, "CreateTrialSubscription() should error")
	})
}

func TestMockDatabaseService_BindTrialSubscription(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc := &mockDatabaseService{
			subscriptions: map[int64]*database.Subscription{
				0: {SubscriptionID: "trial123", IsTrial: true},
			},
		}
		sub, err := svc.BindTrialSubscription(context.Background(), "trial123", 123456, "testuser")
		require.NoError(t, err, "BindTrialSubscription() error")
		assert.Equal(t, int64(123456), sub.TelegramID, "TelegramID")
		assert.Equal(t, "testuser", sub.Username, "Username")
		assert.False(t, sub.IsTrial, "IsTrial should be false after binding")
	})

	t.Run("not found", func(t *testing.T) {
		svc := &mockDatabaseService{}
		_, err := svc.BindTrialSubscription(context.Background(), "notexist", 123456, "testuser")
		assert.Error(t, err, "BindTrialSubscription() should error")
	})
}

func TestMockDatabaseService_TrialRequests(t *testing.T) {
	svc := &mockDatabaseService{}

	// Create trial requests
	err := svc.CreateTrialRequest(context.Background(), "192.168.1.1")
	require.NoError(t, err, "CreateTrialRequest() error")

	err = svc.CreateTrialRequest(context.Background(), "192.168.1.1")
	require.NoError(t, err, "CreateTrialRequest() error")

	// Count trial requests
	count, err := svc.CountTrialRequestsByIPLastHour(context.Background(), "192.168.1.1")
	require.NoError(t, err, "CountTrialRequestsByIPLastHour() error")
	assert.Equal(t, 2, count, "CountTrialRequestsByIPLastHour()")

	// Different IP
	count, err = svc.CountTrialRequestsByIPLastHour(context.Background(), "10.0.0.1")
	require.NoError(t, err, "CountTrialRequestsByIPLastHour() error")
	assert.Equal(t, 0, count, "CountTrialRequestsByIPLastHour() for different IP")
}

func TestMockDatabaseService_CleanupExpiredTrials(t *testing.T) {
	svc := &mockDatabaseService{cleanupExpiredCount: 5}

	count, err := svc.CleanupExpiredTrials(context.Background(), 24, nil, 1)
	require.NoError(t, err, "CleanupExpiredTrials() error")
	assert.Equal(t, int64(5), count, "CleanupExpiredTrials()")
}

func TestMockXUIClient_Ping(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		client := &mockXUIClient{}
		err := client.Ping(context.Background())
		assert.NoError(t, err, "Ping() should not error")
	})

	t.Run("error", func(t *testing.T) {
		client := &mockXUIClient{pingErr: fmt.Errorf("connection refused")}
		err := client.Ping(context.Background())
		assert.Error(t, err, "Ping() should error")
	})
}

func TestMockXUIClient_UpdateClient(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		client := &mockXUIClient{}
		err := client.UpdateClient(context.Background(), 1, "client123", "test@example.com", "sub123", 1000, time.Now(), 123, "comment")
		assert.NoError(t, err, "UpdateClient() should not error")
	})

	t.Run("error", func(t *testing.T) {
		client := &mockXUIClient{updateClientErr: fmt.Errorf("update failed")}
		err := client.UpdateClient(context.Background(), 1, "client123", "test@example.com", "sub123", 1000, time.Now(), 123, "comment")
		assert.Error(t, err, "UpdateClient() should error")
	})
}

func TestMockDatabaseService_GetSubscriptionBySubscriptionID(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		svc := &mockDatabaseService{
			subscriptions: map[int64]*database.Subscription{
				123: {SubscriptionID: "sub123", TelegramID: 123},
			},
		}
		sub, err := svc.GetSubscriptionBySubscriptionID(context.Background(), "sub123")
		require.NoError(t, err, "GetSubscriptionBySubscriptionID() error")
		require.NotNil(t, sub, "Subscription should not be nil")
		assert.Equal(t, int64(123), sub.TelegramID, "TelegramID")
	})

	t.Run("not found", func(t *testing.T) {
		svc := &mockDatabaseService{}
		sub, err := svc.GetSubscriptionBySubscriptionID(context.Background(), "notexist")
		require.NoError(t, err, "GetSubscriptionBySubscriptionID() error")
		assert.Nil(t, sub, "Subscription should be nil")
	})
}

// Test that mockDatabaseService implements DatabaseService interface
func TestMockDatabaseService_ImplementsInterface(t *testing.T) {
	var _ DatabaseService = (*mockDatabaseService)(nil)
}

// Test that mockXUIClient implements XUIClient interface
func TestMockXUIClient_ImplementsInterface(t *testing.T) {
	var _ XUIClient = (*mockXUIClient)(nil)
}
