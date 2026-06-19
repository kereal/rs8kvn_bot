package vpn

import (
	"context"
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
	addCalled    bool
	deleteCalled bool
	addCapture   *capturedAddClientWithID
	deleteCapture *capturedDeleteClient
	addErr       error
	deleteErr   error
}

func (f *fakeXUIClient) Ping(ctx context.Context) error {
	return nil
}

func (f *fakeXUIClient) AddClient(ctx context.Context, inboundIDs []int, email string, trafficBytes int64, expiryTime time.Time) (*xui.ClientConfig, error) {
	return nil, nil
}

func (f *fakeXUIClient) AddClientWithID(ctx context.Context, inboundIDs []int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
	f.addCalled = true
	f.addCapture = &capturedAddClientWithID{
		inboundIDs:   inboundIDs,
		email:        email,
		clientID:     clientID,
		subID:        subID,
		trafficBytes: trafficBytes,
		expiryTime:   expiryTime,
		resetDays:    resetDays,
	}
	return &xui.ClientConfig{ID: clientID, SubID: subID, Email: email}, f.addErr
}

func (f *fakeXUIClient) UpdateClient(ctx context.Context, inboundIDs []int, currentEmail, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
	return nil
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
	uuid := "test-uuid-123"
	username := "testuser"
	err := client.CreateSubscription(ctx, uuid, username)

	require.NoError(t, err)
	assert.True(t, fake.addCalled, "CreateSubscription must call AddClientWithID on the underlying xui client")
	require.NotNil(t, fake.addCapture)
	assert.Equal(t, []int{1, 2}, fake.addCapture.inboundIDs)
	assert.Equal(t, username, fake.addCapture.email)
	assert.Equal(t, uuid, fake.addCapture.clientID)
	assert.Equal(t, uuid, fake.addCapture.subID)
}

func TestThreeXUIClient_DeleteSubscription_CallsDeleteClient(t *testing.T) {
	t.Parallel()

	fake := &fakeXUIClient{}
	client := NewThreeXUIClient(fake, []int{1})

	ctx := context.Background()
	uuid := "del-uuid"
	username := "deluser"
	err := client.DeleteSubscription(ctx, uuid, username)

	require.NoError(t, err)
	assert.True(t, fake.deleteCalled, "DeleteSubscription must call DeleteClient on the underlying xui client")
	require.NotNil(t, fake.deleteCapture)
	assert.Equal(t, username, fake.deleteCapture.email)
}

func TestThreeXUIClient_CreateSubscription_WrapsError(t *testing.T) {
	t.Parallel()

	fake := &fakeXUIClient{addErr: assert.AnError}
	client := NewThreeXUIClient(fake, []int{1})

	err := client.CreateSubscription(context.Background(), "uuid", "user")
	assert.Error(t, err)
}

func TestThreeXUIClient_DeleteSubscription_WrapsError(t *testing.T) {
	t.Parallel()

	fake := &fakeXUIClient{deleteErr: assert.AnError}
	client := NewThreeXUIClient(fake, []int{1})

	err := client.DeleteSubscription(context.Background(), "uuid", "user")
	assert.Error(t, err)
}
