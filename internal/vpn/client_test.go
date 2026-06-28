package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestProxmanClient_CreateSubscription_SendsWebhook(t *testing.T) {
	t.Parallel()

	var gotToken string
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "test-token")
	provision := SubscriptionProvision{
		ClientID:       "client-123",
		Username:       "testuser",
		SubID:          "sub-456",
		TrafficBytes:   1024,
		ExpiryTime:     time.Now().Add(24 * time.Hour),
		ResetDays:      -1,
	}

	err := client.CreateSubscription(context.Background(), provision)
	require.NoError(t, err)
	assert.Equal(t, "Bearer test-token", gotToken)

	var event ProxmanEvent
	require.NoError(t, json.Unmarshal([]byte(gotBody), &event))
	assert.Equal(t, "subscription.create", event.Event)
	assert.Equal(t, "client-123", event.ClientID)
	assert.Equal(t, "testuser", event.Email)
	assert.Equal(t, "sub-456", event.SubscriptionID)
	assert.NotEmpty(t, event.EventID)
}

func TestProxmanClient_CreateSubscription_Duplicate(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("duplicate"))
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "test-token")
	err := client.CreateSubscription(context.Background(), SubscriptionProvision{})
	require.ErrorIs(t, err, ErrSubscriptionAlreadyExists)
}

func TestProxmanClient_DeleteSubscription_SendsWebhook(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "test-token")
	err := client.DeleteSubscription(context.Background(), SubscriptionProvision{
		ClientID: "client-123",
		Username: "testuser",
		SubID:    "sub-456",
	})
	require.NoError(t, err)
}


func TestProxmanClient_DeleteSubscription_NotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("client not found"))
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "test-token")
	err := client.DeleteSubscription(context.Background(), SubscriptionProvision{
		ClientID: "client-123",
		Username: "testuser",
		SubID:    "sub-456",
	})
	require.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestProxmanClient_UpdateSubscription_DeleteThenCreate(t *testing.T) {
	t.Parallel()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "test-token")
	err := client.UpdateSubscription(context.Background(), SubscriptionProvision{
		ClientID: "client-123",
		Username: "testuser",
		SubID:    "sub-456",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

func TestProxmanClient_Unauthorized(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "bad-token")
	err := client.CreateSubscription(context.Background(), SubscriptionProvision{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unauthorized")
}

func TestProxmanClient_BadRequest(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("unknown event"))
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "test-token")
	err := client.CreateSubscription(context.Background(), SubscriptionProvision{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad request")
}

func TestProxmanClient_NotImplemented(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "test-token")
	err := client.CreateSubscription(context.Background(), SubscriptionProvision{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
}
