package vpn

import (
	"context"
	"errors"
	"fmt"
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

type fakeXUIClient struct {
	addCalled     bool
	deleteCalled  bool
	updateCalled  bool
	addCapture    *capturedAddClientWithID
	deleteCapture *capturedDeleteClient
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
	return f.updateErr
}

func (f *fakeXUIClient) DeleteClient(ctx context.Context, email string) error {
	f.deleteCalled = true
	f.deleteCapture = &capturedDeleteClient{email: email}
	return f.deleteErr
}

func (f *fakeXUIClient) GetClientTraffic(ctx context.Context, email string) (*xui.ClientTraffic, error) {
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

func TestThreeXUIClient_CreateSubscription_WrapsError(t *testing.T) {
	t.Parallel()

	fake := &fakeXUIClient{addErr: fmt.Errorf("client already exists")}
	client := NewThreeXUIClient(fake, []int{1})

	err := client.CreateSubscription(context.Background(), SubscriptionProvision{ClientID: "uuid", Username: "user", SubID: "sub"})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrSubscriptionAlreadyExists))
}

func TestThreeXUIClient_DeleteSubscription_WrapsError(t *testing.T) {
	t.Parallel()

	fake := &fakeXUIClient{deleteErr: fmt.Errorf("client not found")}
	client := NewThreeXUIClient(fake, []int{1})

	err := client.DeleteSubscription(context.Background(), SubscriptionProvision{ClientID: "uuid", Username: "user", SubID: "sub"})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrSubscriptionNotFound))
}
