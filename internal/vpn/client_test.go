package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sentinel-классификация теперь живёт в internal/xui (classify.go): тексты
// панели распознаются адаптером, а vpn маппит sentinel'ы (mapPanelError).
// Эти тесты проверяют поведение — какой sentinel получается, — а не текст
// классификатора; контрактные тексты панели закреплены в
// internal/xui/client_test.go.
func TestThreeXUIClient_CreateSubscription_AlreadyExists(t *testing.T) {
	t.Parallel()

	fake := &fakeXUIClient{addErr: fmt.Errorf("add client failed: %w", xui.ErrClientAlreadyExists)}
	client := NewThreeXUIClient(fake, []int{1})

	err := client.CreateSubscription(context.Background(), SubscriptionProvision{ClientID: "uuid", Username: "user", SubID: "sub"})
	assert.ErrorIs(t, err, ErrSubscriptionAlreadyExists)
}

func TestThreeXUIClient_UpdateSubscription_NotFound(t *testing.T) {
	t.Parallel()

	// Панель отвечает на update отсутствующего клиента "record not found";
	// классификатор xui обязан дать ErrClientNotFound, а маппинг vpn —
	// ErrSubscriptionNotFound (раньше update промахивался мимо not-found).
	fake := &fakeXUIClient{updateErr: fmt.Errorf("update client failed: %w", xui.ErrClientNotFound)}
	client := NewThreeXUIClient(fake, []int{1})

	err := client.UpdateSubscription(context.Background(), SubscriptionProvision{ClientID: "uuid", Username: "user", SubID: "sub"})
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestThreeXUIClient_DeleteSubscription_NotFound(t *testing.T) {
	t.Parallel()

	fake := &fakeXUIClient{deleteErr: fmt.Errorf("delete client failed: %w", xui.ErrClientNotFound)}
	client := NewThreeXUIClient(fake, []int{1})

	err := client.DeleteSubscription(context.Background(), SubscriptionProvision{ClientID: "uuid", Username: "user", SubID: "sub"})
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestThreeXUIClient_OtherError_PassesThrough(t *testing.T) {
	t.Parallel()

	original := errors.New("connection refused")
	fake := &fakeXUIClient{addErr: original, deleteErr: original, updateErr: original}
	client := NewThreeXUIClient(fake, []int{1})

	err := client.CreateSubscription(context.Background(), SubscriptionProvision{ClientID: "uuid", Username: "user", SubID: "sub"})
	assert.ErrorIs(t, err, original)
	assert.NotErrorIs(t, err, ErrSubscriptionAlreadyExists)

	err = client.UpdateSubscription(context.Background(), SubscriptionProvision{ClientID: "uuid", Username: "user", SubID: "sub"})
	assert.ErrorIs(t, err, original)
	assert.NotErrorIs(t, err, ErrSubscriptionNotFound)

	err = client.DeleteSubscription(context.Background(), SubscriptionProvision{ClientID: "uuid", Username: "user", SubID: "sub"})
	assert.ErrorIs(t, err, original)
	assert.NotErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestNewClient_3xUI_RequiresXUIClient(t *testing.T) {
	_, err := NewClient(Config{Type: database.NodeType3xUI})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "xui client is required")
}

func TestNewClient_3xUI_Success(t *testing.T) {
	mockXUI := &fakeXUIClient{}
	client, err := NewClient(Config{
		Type:       database.NodeType3xUI,
		XUIClient:  mockXUI,
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

func TestNewClient_Fetch_Success(t *testing.T) {
	t.Parallel()

	client, err := NewClient(Config{Type: database.NodeTypeFetch})
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.IsType(t, &FetchClient{}, client)
}

func TestFetchClient_AllNoOps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	provision := SubscriptionProvision{
		ClientID: "client-123",
		Username: "testuser",
		SubID:    "sub-456",
	}

	client := NewFetchClient()
	assert.NoError(t, client.CreateSubscription(ctx, provision))
	assert.NoError(t, client.UpdateSubscription(ctx, provision))
	assert.NoError(t, client.DeleteSubscription(ctx, provision))
	assert.NoError(t, client.Close())
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type errReadCloser struct {
	err error
}

func (e errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (e errReadCloser) Close() error             { return nil }

func TestProxmanClient_CreateSubscription_SendsWebhook(t *testing.T) {
	t.Parallel()

	var (
		gotToken string
		gotBody  string
	)

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
		ClientID:     "client-123",
		Username:     "testuser",
		SubID:        "sub-456",
		TrafficBytes: 1024,
		ExpiryTime:   time.Now().Add(24 * time.Hour),
		ResetDays:    -1,
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

func TestProxmanClient_UpdateSubscription_NoOp(t *testing.T) {
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
	assert.Equal(t, 0, calls)
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

func TestProxmanClient_ResponseTooLarge(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := io.WriteString(w, strings.Repeat("x", config.MaxResponseSize+1))
		require.NoError(t, err)
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "test-token")
	err := client.CreateSubscription(context.Background(), SubscriptionProvision{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response exceeds")
}

func TestProxmanClient_ResponseReadError(t *testing.T) {
	t.Parallel()

	client := NewProxmanClient("http://proxman.invalid", "test-token")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       errReadCloser{err: errors.New("read failed")},
			Header:     make(http.Header),
		}, nil
	})}

	err := client.CreateSubscription(context.Background(), SubscriptionProvision{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read proxman response")
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
