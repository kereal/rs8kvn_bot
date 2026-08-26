package xui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetTraffic_EmptyEmail(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.ResetTraffic(context.Background(), "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "email cannot be empty")
}

func TestResetTraffic_Success(t *testing.T) {
	t.Parallel()

	var gotPath, gotMethod, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.ResetTraffic(context.Background(), "user/name")
	assert.NoError(t, err)
	assert.Equal(t, "/panel/api/clients/resetTraffic/user%2Fname", gotPath, "email must be path-escaped (slash becomes %2F)")
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "Bearer "+testAPIToken, gotAuth)
}

func TestResetTraffic_PanelErrorSurfacesMsg(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"success":false,"msg":"client not found"}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.ResetTraffic(context.Background(), "missing@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to reset traffic")
	assert.Contains(t, err.Error(), "client not found")
}

func TestResetTraffic_InvalidJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `not-json`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.ResetTraffic(context.Background(), "user@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse resetTraffic response")
}

// TestResetTraffic_HTTPError ensures a non-2xx upstream response is retried and
// finally surfaced. The 2s initial backoff makes this slow in short mode, so it
// is skipped there like the other network tests in this package.
func TestResetTraffic_HTTPError(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.ResetTraffic(context.Background(), "user@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-200", "the upstream HTTP failure must be surfaced")
}
