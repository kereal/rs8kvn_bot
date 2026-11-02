package vpn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capturedAddClientWithID struct {
	inboundIDs   []int
	email        string
	clientID     string
	subID        string
	trafficBytes int64
	expiryTime   time.Time
	resetDays    int
}

type capturedDeleteClient struct {
	email string
}

type capturedUpdateClient struct {
	inboundIDs   []int
	currentEmail string
	email        string
	clientID     string
	subID        string
	trafficBytes int64
	expiryTime   time.Time
	resetDays    int
}

type fakeXUIClient struct {
	addCalled     bool
	deleteCalled  bool
	updateCalled  bool
	addCapture    *capturedAddClientWithID
	deleteCapture *capturedDeleteClient
	updateCapture *capturedUpdateClient
	addErr        error
	updateErr     error
	deleteErr     error
}

func (f *fakeXUIClient) Ping(ctx context.Context) error {
	return nil
}

func (f *fakeXUIClient) AddClient(ctx context.Context, inboundIDs []int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error) {
	return nil, nil
}

func (f *fakeXUIClient) AddClientWithID(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
	f.addCalled = true
	f.addCapture = &capturedAddClientWithID{
		inboundIDs:   req.InboundIDs,
		email:        req.Email,
		clientID:     req.ClientID,
		subID:        req.SubID,
		trafficBytes: req.TrafficBytes,
		expiryTime:   req.ExpiryTime,
		resetDays:    req.ResetDays,
	}

	return &xui.ClientConfig{ID: req.ClientID, SubID: req.SubID, Email: req.Email}, f.addErr
}

func (f *fakeXUIClient) UpdateClient(ctx context.Context, req xui.ClientRequest) error {
	f.updateCalled = true
	f.updateCapture = &capturedUpdateClient{
		inboundIDs:   req.InboundIDs,
		currentEmail: req.CurrentEmail,
		email:        req.Email,
		clientID:     req.ClientID,
		subID:        req.SubID,
		trafficBytes: req.TrafficBytes,
		expiryTime:   req.ExpiryTime,
		resetDays:    req.ResetDays,
	}

	return f.updateErr
}

func (f *fakeXUIClient) DeleteClient(ctx context.Context, email string) error {
	f.deleteCalled = true
	f.deleteCapture = &capturedDeleteClient{email: email}

	return f.deleteErr
}

func (f *fakeXUIClient) ResetTraffic(_ context.Context, _ string) error {
	return nil
}

func (f *fakeXUIClient) GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error) {
	return nil, nil
}

func (f *fakeXUIClient) GetClientByEmail(ctx context.Context, email string) (*xui.ClientRecord, error) {
	return nil, nil
}

func (f *fakeXUIClient) GetSubscriptionLink(host, subID, subPath string) string {
	return ""
}

func (f *fakeXUIClient) GetExternalURL(host string) string {
	return ""
}

func (f *fakeXUIClient) Close() error {
	return nil
}

func TestThreeXUIClient_CreateSubscription_CallsAddClientWithID(t *testing.T) {
	t.Parallel()

	fake := &fakeXUIClient{}
	client := NewThreeXUIClient(fake, []int{1, 2})

	ctx := context.Background()
	provision := SubscriptionProvision{
		ClientID:     "test-uuid-123",
		Username:     "testuser",
		SubID:        "sub-123",
		TrafficBytes: 1024,
		ExpiryTime:   time.Unix(1700000000, 0).UTC(),
		ResetDays:    7,
	}
	err := client.CreateSubscription(ctx, provision)

	require.NoError(t, err)
	assert.True(t, fake.addCalled, "CreateSubscription must call AddClientWithID on the underlying xui client")
	require.NotNil(t, fake.addCapture)
	assert.Equal(t, []int{1, 2}, fake.addCapture.inboundIDs)
	assert.Equal(t, provision.Username, fake.addCapture.email)
	assert.Equal(t, provision.ClientID, fake.addCapture.clientID)
	assert.Equal(t, provision.SubID, fake.addCapture.subID)
	assert.Equal(t, provision.TrafficBytes, fake.addCapture.trafficBytes)
	assert.Equal(t, provision.ExpiryTime, fake.addCapture.expiryTime)
	assert.Equal(t, provision.ResetDays, fake.addCapture.resetDays)
}

func TestThreeXUIClient_DeleteSubscription_CallsDeleteClient(t *testing.T) {
	t.Parallel()

	fake := &fakeXUIClient{}
	client := NewThreeXUIClient(fake, []int{1})

	ctx := context.Background()
	provision := SubscriptionProvision{ClientID: "del-uuid", Username: "deluser", SubID: "sub-del"}
	err := client.DeleteSubscription(ctx, provision)

	require.NoError(t, err)
	assert.True(t, fake.deleteCalled, "DeleteSubscription must call DeleteClient on the underlying xui client")
	require.NotNil(t, fake.deleteCapture)
	assert.Equal(t, provision.Username, fake.deleteCapture.email)
}

// Маппинг полей update: Username уезжает в Email (новое имя), CurrentEmail —
// в CurrentEmail (адрес существующей записи для rename-семантики панели).
func TestThreeXUIClient_UpdateSubscription_MapsFields(t *testing.T) {
	t.Parallel()

	fake := &fakeXUIClient{}
	client := NewThreeXUIClient(fake, []int{3, 4})

	ctx := context.Background()
	provision := SubscriptionProvision{
		ClientID:     "upd-uuid",
		Username:     "newname",
		CurrentEmail: "oldname",
		SubID:        "sub-upd",
		TrafficBytes: 2048,
		ExpiryTime:   time.Unix(1800000000, 0).UTC(),
		ResetDays:    14,
	}
	err := client.UpdateSubscription(ctx, provision)

	require.NoError(t, err)
	assert.True(t, fake.updateCalled, "UpdateSubscription must call UpdateClient on the underlying xui client")
	require.NotNil(t, fake.updateCapture)
	assert.Equal(t, []int{3, 4}, fake.updateCapture.inboundIDs)
	assert.Equal(t, "oldname", fake.updateCapture.currentEmail, "CurrentEmail must carry the existing email for rename")
	assert.Equal(t, "newname", fake.updateCapture.email, "Username must be mapped to the new Email")
	assert.Equal(t, provision.ClientID, fake.updateCapture.clientID)
	assert.Equal(t, provision.SubID, fake.updateCapture.subID)
	assert.Equal(t, provision.TrafficBytes, fake.updateCapture.trafficBytes)
	assert.Equal(t, provision.ExpiryTime, fake.updateCapture.expiryTime)
	assert.Equal(t, provision.ResetDays, fake.updateCapture.resetDays)
}

// Поведение: сетевой сбой proxman (обрыв соединения) ретраится с бэкоффом,
// клиент в итоге получает успешный результат. События идемпотентны (EventID),
// поэтому повтор безопасен.
func TestProxmanClient_RetriesNetworkFailures(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			// обрываем соединение без ответа — сетевая ошибка на клиенте
			hj, ok := w.(http.Hijacker)
			require.True(t, ok)
			conn, _, err := hj.Hijack()
			require.NoError(t, err)
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "token")
	client.retryMax = 3
	client.retryDelay = time.Millisecond

	err := client.CreateSubscription(context.Background(), SubscriptionProvision{
		ClientID: "uuid", Username: "user", SubID: "sub",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(3), attempts.Load())
}

// Не-сетевые ошибки (не-200 ответ) не ретраятся: один запрос — один отказ.
func TestProxmanClient_DoesNotRetryHTTPStatusErrors(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "token")
	client.retryMax = 3
	client.retryDelay = time.Millisecond

	err := client.CreateSubscription(context.Background(), SubscriptionProvision{
		ClientID: "uuid", Username: "user", SubID: "sub",
	})
	require.Error(t, err)
	assert.Equal(t, int32(1), attempts.Load())
}
