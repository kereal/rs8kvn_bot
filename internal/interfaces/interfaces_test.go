package interfaces

import (
	"context"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/xui"
)

type mockDatabaseService struct {
	subscriptions map[int64]*database.Subscription
	getByIDErr    error
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
	if err != nil {
		t.Fatalf("CreateSubscription() error = %v", err)
	}

	retrieved, err := svc.GetByTelegramID(context.Background(), 123)
	if err != nil {
		t.Fatalf("GetByTelegramID() error = %v", err)
	}
	if retrieved == nil {
		t.Fatal("GetByTelegramID() returned nil")
	}
	if retrieved.Username != "testuser" {
		t.Errorf("Username = %s, want testuser", retrieved.Username)
	}
}

func TestMockDatabaseService_GetByID(t *testing.T) {
	svc := &mockDatabaseService{
		subscriptions: make(map[int64]*database.Subscription),
	}

	_, err := svc.GetByID(context.Background(), 1)
	if err != nil {
		t.Errorf("GetByID() error = %v", err)
	}
}

type mockXUIClient struct {
	loginErr        error
	addClientErr    error
	deleteClientErr error
	getTrafficErr   error
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
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	config, err := client.AddClient(context.Background(), 1, "test", 1000, time.Now())
	if err != nil {
		t.Fatalf("AddClient() error = %v", err)
	}
	if config.ID != "test-id" {
		t.Errorf("ID = %s, want test-id", config.ID)
	}

	traffic, err := client.GetClientTraffic(context.Background(), "test")
	if err != nil {
		t.Fatalf("GetClientTraffic() error = %v", err)
	}
	if traffic.Up != 1000 {
		t.Errorf("Up = %d, want 1000", traffic.Up)
	}
}

func TestMockXUIClient_GetSubscriptionLink(t *testing.T) {
	client := &mockXUIClient{}
	link := client.GetSubscriptionLink("http://localhost", "sub123", "sub")
	expected := "http://localhost/sub/sub123"
	if link != expected {
		t.Errorf("GetSubscriptionLink() = %s, want %s", link, expected)
	}
}
