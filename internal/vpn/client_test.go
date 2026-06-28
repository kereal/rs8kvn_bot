package vpn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/xui"
	"github.com/stretchr/testify/assert"
)

func TestClassifyCreateSubscriptionError_AlreadyExists(t *testing.T) {
	err := classifyCreateSubscriptionError(errors.New("client ALREADY EXISTS"))
	assert.ErrorIs(t, err, ErrSubscriptionAlreadyExists)
	assert.ErrorContains(t, err, "client ALREADY EXISTS")
}

func TestClassifyCreateSubscriptionError_Duplicate(t *testing.T) {
	err := classifyCreateSubscriptionError(errors.New("duplicate entry"))
	assert.ErrorIs(t, err, ErrSubscriptionAlreadyExists)
}

func TestClassifyCreateSubscriptionError_AlreadyAdded(t *testing.T) {
	err := classifyCreateSubscriptionError(errors.New("already added to inbound"))
	assert.ErrorIs(t, err, ErrSubscriptionAlreadyExists)
}

func TestClassifyCreateSubscriptionError_OtherError(t *testing.T) {
	original := errors.New("connection refused")
	err := classifyCreateSubscriptionError(original)
	assert.ErrorIs(t, err, original)
	assert.Nil(t, errors.Unwrap(err))
}

func TestClassifyCreateSubscriptionError_Nil(t *testing.T) {
	err := classifyCreateSubscriptionError(nil)
	assert.NoError(t, err)
}

func TestClassifyDeleteSubscriptionError_NotFound(t *testing.T) {
	err := classifyDeleteSubscriptionError(errors.New("client NOT FOUND"))
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestClassifyDeleteSubscriptionError_DoesNotExist(t *testing.T) {
	err := classifyDeleteSubscriptionError(errors.New("does not exist"))
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestClassifyDeleteSubscriptionError_OtherError(t *testing.T) {
	original := errors.New("connection timeout")
	err := classifyDeleteSubscriptionError(original)
	assert.ErrorIs(t, err, original)
}

func TestClassifyDeleteSubscriptionError_Nil(t *testing.T) {
	err := classifyDeleteSubscriptionError(nil)
	assert.NoError(t, err)
}

func TestNewClient_3xUI_RequiresXUIClient(t *testing.T) {
	_, err := NewClient(Config{Type: database.NodeType3xUI})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "xui client is required")
}

func TestNewClient_3xUI_Success(t *testing.T) {
	mockXUI := &mockXUIClient{}
	client, err := NewClient(Config{
		Type:      database.NodeType3xUI,
		XUIClient: mockXUI,
		InboundIDs: []int{1},
	})
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.IsType(t, &ThreeXUIClient{}, client)
}

func TestNewClient_Proxman_Success(t *testing.T) {
	client, err := NewClient(Config{Type: database.NodeTypeProxman})
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.IsType(t, &ProxmanClient{}, client)
}

func TestNewClient_UnknownType(t *testing.T) {
	_, err := NewClient(Config{Type: database.NodeType("unknown")})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "unsupported node type")
}

type mockXUIClient struct{}

func (m *mockXUIClient) Ping(ctx context.Context) error {
	return nil
}

func (m *mockXUIClient) GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error) {
	return nil, nil
}

func (m *mockXUIClient) AddClient(ctx context.Context, inboundIDs []int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error) {
	return nil, nil
}

func (m *mockXUIClient) AddClientWithID(ctx context.Context, inboundIDs []int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
	return &xui.ClientConfig{ID: clientID, SubID: subID}, nil
}

func (m *mockXUIClient) UpdateClient(ctx context.Context, inboundIDs []int, currentEmail, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, resetDays int, tgID int64, comment string) error {
	return nil
}

func (m *mockXUIClient) DeleteClient(ctx context.Context, email string) error {
	return nil
}

func (m *mockXUIClient) Close() error {
	return nil
}
