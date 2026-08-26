package xui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// updateMock returns a handler that responds to both the inbound flow lookup
// (GET /panel/api/inbounds/get/{id}) and the client update POST call.
func updateMock(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "get/") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"obj":{"id":1,"streamSettings":"{\"network\":\"tcp\"}"}}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"success":true,"msg":""}`))
}

func TestUpdateClient_EnableTrue(t *testing.T) {
	updated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		updateMock(w, r)
		if strings.Contains(r.URL.Path, "/panel/api/clients/update/") {
			updated = true
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token")
	require.NoError(t, err)

	enable := true
	req := ClientRequest{
		ClientID:     "uuid-123",
		CurrentEmail: "user",
		Email:        "user",
		InboundIDs:   []int{1},
		TrafficBytes: 1024,
		ResetDays:    30,
		Enable:       &enable,
	}
	require.NoError(t, client.UpdateClient(context.Background(), req))
	require.True(t, updated, "client update endpoint must be hit when Enable is set")
}

func TestUpdateClient_EnableFalse(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		updateMock(w, r)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token")
	require.NoError(t, err)

	enable := false
	req := ClientRequest{
		ClientID:     "uuid-456",
		CurrentEmail: "user",
		Email:        "user",
		InboundIDs:   []int{1},
		Enable:       &enable,
	}
	require.NoError(t, client.UpdateClient(context.Background(), req))
	require.True(t, called)
}

// TestGetClientTraffic_DecodesEnable ensures GetClientTraffic surfaces the
// panel's enabled flag, which the traffic worker relies on.
func TestGetClientTraffic_DecodesEnable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"obj":{"enable":false,"up":100,"down":200,"total":1000,"reset":30}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token")
	require.NoError(t, err)

	traffic, err := client.GetClientTraffic(context.Background(), "user")
	require.NoError(t, err)
	require.False(t, traffic.Enable)
	require.Equal(t, int64(100), traffic.Up)
}