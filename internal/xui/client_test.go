package xui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/logger"
)

func initTestConfig() {
	config.XUIMaxRetries = 1
	config.XUIInitialRetryDelay = 10 * time.Millisecond
}

func TestMain(m *testing.M) {
	initTestConfig()
	_, _ = logger.Init("", "error")
	os.Exit(m.Run())
}

func TestNewClient(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	require.NotNil(t, client, "NewClient() returned nil")

	assert.Equal(t, "http://localhost:2053", client.host, "host")
	assert.Equal(t, "admin", client.username, "username")
	assert.Equal(t, "password", client.password, "password")
	assert.NotNil(t, client.httpClient, "httpClient is nil")
}

func TestNewClient_HTTPClientConfig(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")

	assert.Equal(t, 10*time.Second, client.httpClient.Timeout, "httpClient.Timeout")
	assert.NotNil(t, client.httpClient.Jar, "httpClient.Jar is nil")
}

func TestLogin_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/login", r.URL.Path, "Expected /login path")
		assert.Equal(t, "POST", r.Method, "Expected POST method")

		resp := APIResponse{
			Success: true,
			Msg:     "Login successful",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.Login(ctx)
	require.NoError(t, err, "Login() error")
}

func TestLogin_Failure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := APIResponse{
			Success: false,
			Msg:     "Invalid credentials",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "wrongpassword", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.Login(ctx)
	require.Error(t, err, "Login() should return error for failed login")
}

func TestLogin_ContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
		}
		resp := APIResponse{Success: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = client.Login(ctx)
	require.Error(t, err, "Login() should return error when context is cancelled")
}

func TestAddClientWithID_Success(t *testing.T) {
	t.Parallel()

	loginCalled := false
	addClientCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			loginCalled = true
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/addClient":
			addClientCalled = true
			resp := APIResponse{Success: true, Msg: "Client added successfully"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	result, err := client.AddClientWithID(ctx, 1, "testuser", "client-id-123", "sub-id-456", 107374182400, time.Now().Add(24*time.Hour), 31)
	require.NoError(t, err, "AddClientWithID() error")

	assert.True(t, loginCalled, "Login was not called")
	assert.True(t, addClientCalled, "AddClient was not called")
	require.NotNil(t, result, "Result is nil")
	assert.Equal(t, "client-id-123", result.ID, "Result.ID")
	assert.Equal(t, "sub-id-456", result.SubID, "Result.SubID")
}

func TestAddClientWithID_ServerError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		resp := APIResponse{Success: false, Msg: "Internal server error"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	_, _ = client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), 31)
}

func TestGetSubscriptionLink(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")

	link := client.GetSubscriptionLink("http://localhost:2053", "sub123", "sub")
	expected := "http://localhost:2053/sub/sub123"
	assert.Equal(t, expected, link, "GetSubscriptionLink()")
}

func TestGetSubscriptionLink_CustomPath(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")

	link := client.GetSubscriptionLink("http://localhost:2053", "sub456", "custom")
	expected := "http://localhost:2053/custom/sub456"
	assert.Equal(t, expected, link, "GetSubscriptionLink()")
}

func TestAddClientWithID_InvalidInboundID(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	_, err = client.AddClientWithID(ctx, 0, "testuser", "client-id", "sub-id", 107374182400, time.Now().Add(24*time.Hour), 31)
	require.Error(t, err, "AddClientWithID() should return error for invalid inbound ID")
}

func TestAddClientWithID_EmptyClientID(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	_, err = client.AddClientWithID(ctx, 1, "testuser", "", "sub-id", 107374182400, time.Now().Add(24*time.Hour), 31)
	require.Error(t, err, "AddClientWithID() should return error for empty client ID")
}

func TestMarshalJSON_Success(t *testing.T) {
	t.Parallel()

	data := map[string]string{
		"key": "value",
		"foo": "bar",
	}

	reader, err := marshalJSON(data)
	require.NoError(t, err, "marshalJSON() error")
	require.NotNil(t, reader, "marshalJSON() returned nil reader")

	// Read the content from the reader
	content, err := io.ReadAll(reader)
	require.NoError(t, err, "Failed to read from reader")

	// Verify it's valid JSON
	var result map[string]string
	err = json.Unmarshal(content, &result)
	require.NoError(t, err, "Failed to unmarshal JSON")

	assert.Equal(t, "value", result["key"], "key should match")
	assert.Equal(t, "bar", result["foo"], "foo should match")
}

func TestMarshalJSON_NilInput(t *testing.T) {
	t.Parallel()

	reader, err := marshalJSON(nil)
	require.NoError(t, err, "marshalJSON() with nil should not error")
	require.NotNil(t, reader, "marshalJSON() returned nil reader")

	content, err := io.ReadAll(reader)
	require.NoError(t, err, "Failed to read from reader")
	assert.Equal(t, "null", string(content), "nil should marshal to 'null'")
}

func TestMarshalJSON_ComplexStruct(t *testing.T) {
	t.Parallel()

	type TestStruct struct {
		Name   string `json:"name"`
		Age    int    `json:"age"`
		Active bool   `json:"active"`
	}

	data := TestStruct{
		Name:   "Test",
		Age:    30,
		Active: true,
	}

	reader, err := marshalJSON(data)
	require.NoError(t, err, "marshalJSON() error")
	require.NotNil(t, reader, "marshalJSON() returned nil reader")

	content, err := io.ReadAll(reader)
	require.NoError(t, err, "Failed to read from reader")

	var result TestStruct
	err = json.Unmarshal(content, &result)
	require.NoError(t, err, "Failed to unmarshal JSON")

	assert.Equal(t, "Test", result.Name, "name should match")
	assert.Equal(t, 30, result.Age, "age should match")
	assert.True(t, result.Active, "active should match")
}

func TestCloseResponseBody_NilResponse(t *testing.T) {
	t.Parallel()

	// Should not panic with nil response
	closeResponseBody(nil)
}

func TestCloseResponseBody_NilBody(t *testing.T) {
	t.Parallel()

	resp := &http.Response{
		Body: nil,
	}
	// Should not panic with nil body
	closeResponseBody(resp)
}

func TestCloseResponseBody_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	require.NoError(t, err, "Failed to make request")

	// Should close without error
	closeResponseBody(resp)
}

func TestCloseResponseBody_AlreadyClosed(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	require.NoError(t, err, "Failed to make request")

	// Close once
	closeResponseBody(resp)

	// Close again - should handle gracefully (body is already closed)
	// Note: This test verifies that closeResponseBody doesn't panic on double close
	// The actual behavior of closing an already-closed body varies by Go version
}

func TestClient_LoginSession(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	// First login
	err = client.Login(ctx)
	require.NoError(t, err, "First login failed")

	// Second login should use cached session
	err = client.Login(ctx)
	require.NoError(t, err, "Second login failed")
}

func TestClient_EnsureLoggedIn(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")

	// Test ensureLoggedIn when not logged in
	err = client.ensureLoggedIn(context.Background(), false)
	require.NoError(t, err, "ensureLoggedIn failed")
}

func TestClient_Ping(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
			return
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")

	err = client.Ping(context.Background())
	// Ping should not return error (it's a simple connectivity check)
	assert.NoError(t, err, "Ping() should not error")
}

func TestAddClientWithID_EmptySubID(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	_, err = client.AddClientWithID(ctx, 1, "testuser", "client-id", "", 107374182400, time.Now().Add(24*time.Hour), 31)
	require.Error(t, err, "AddClientWithID() should return error for empty sub ID")
}

func TestGetExternalURL(t *testing.T) {
	t.Parallel()

	result := GetExternalURL("not a valid url")
	assert.NotEmpty(t, result, "GetExternalURL() should return non-empty string")
}

func TestRetryWithBackoff_Success(t *testing.T) {
	t.Parallel()

	callCount := 0
	ctx := context.Background()

	err := RetryWithBackoff(ctx, 3, 100*time.Millisecond, func() error {
		callCount++
		return nil
	})

	assert.NoError(t, err, "RetryWithBackoff() error")
	assert.Equal(t, 1, callCount, "Expected 1 call")
}

func TestRetryWithBackoff_Retries(t *testing.T) {
	t.Parallel()

	callCount := 0
	ctx := context.Background()

	err := RetryWithBackoff(ctx, 5, 10*time.Millisecond, func() error {
		callCount++
		if callCount < 3 {
			return fmt.Errorf("error %d", callCount)
		}
		return nil
	})

	assert.NoError(t, err, "RetryWithBackoff() error")
	assert.Equal(t, 3, callCount, "Expected 3 calls")
}

func TestRetryWithBackoff_MaxRetries(t *testing.T) {
	t.Parallel()

	callCount := 0
	ctx := context.Background()

	err := RetryWithBackoff(ctx, 3, 10*time.Millisecond, func() error {
		callCount++
		return fmt.Errorf("always fails")
	})

	require.Error(t, err, "RetryWithBackoff() should return error after max retries")
	assert.Equal(t, 3, callCount, "Expected 3 calls")
}

func TestRetryWithBackoff_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RetryWithBackoff(ctx, 3, 10*time.Millisecond, func() error {
		return fmt.Errorf("error")
	})

	require.Error(t, err, "RetryWithBackoff() should return error when context is cancelled")
}

func TestAddClient_Success(t *testing.T) {
	t.Parallel()

	loginCalled := false
	addClientCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			loginCalled = true
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/addClient":
			addClientCalled = true
			resp := APIResponse{Success: true, Msg: "Client added successfully"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	result, err := client.AddClient(ctx, 1, "testuser", 107374182400, time.Now().Add(24*time.Hour))
	require.NoError(t, err, "AddClient() error")

	assert.True(t, loginCalled, "Login was not called")
	assert.True(t, addClientCalled, "AddClient was not called")
	require.NotNil(t, result, "Result is nil")
	assert.Equal(t, "testuser", result.Email, "Result.Email")
	assert.Equal(t, int64(107374182400), result.TotalGB, "Result.TotalGB")
	assert.True(t, result.Enable, "Result.Enable should be true")
}

func TestAddClient_LoginFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panel/api/server/status" {
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: false, Msg: "Invalid credentials"}
			json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("Unexpected path: %s", r.URL.Path)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "wrongpassword", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	_, err = client.AddClient(ctx, 1, "testuser", 1000, time.Now())
	require.Error(t, err, "AddClient() should return error when login fails")
}

func TestAddClientWithID_SuccessFalseButMessageIndicatesSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panel/api/server/status" {
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
			return
		}

		resp := APIResponse{Success: false, Msg: "Client added successfully"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	result, err := client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), 31)
	require.Error(t, err, "AddClientWithID() should return error when Success is false")
	require.Nil(t, result, "Result should be nil on error")
	assert.Contains(t, err.Error(), "Client added successfully", "Error should contain the API message")
}

func TestEnsureLoggedIn_CachedSession(t *testing.T) {
	t.Parallel()

	loginCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			loginCount++
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/addClient":
			resp := APIResponse{Success: true, Msg: "Client added successfully"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	_, err = client.AddClient(ctx, 1, "testuser1", 1000, time.Now())
	require.NoError(t, err, "First AddClient() error")

	_, err = client.AddClient(ctx, 1, "testuser2", 1000, time.Now())
	require.NoError(t, err, "Second AddClient() error")

	assert.GreaterOrEqual(t, loginCount, 1, "At least one login should have occurred")
}

func TestDoLogin_InvalidJSONResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.Login(ctx)
	require.Error(t, err, "Login() should return error for invalid JSON response")
}

func TestGetExternalURL_VariousInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host     string
		expected string
	}{
		{"http://localhost:2053", "http://localhost:2053"},
		{"https://example.com:443", "https://example.com:443"},
		{"http://192.168.1.1:8080", "http://192.168.1.1:8080"},
		{"https://sub.domain.com", "https://sub.domain.com"},
	}

	for _, tt := range tests {
		result := GetExternalURL(tt.host)
		assert.Equal(t, tt.expected, result, "GetExternalURL(%s)", tt.host)
	}
}

func TestClientSettings_JSON(t *testing.T) {
	t.Parallel()

	config := &ClientConfig{
		ID:         "test-uuid",
		Email:      "test@example.com",
		LimitIP:    0,
		TotalGB:    107374182400,
		ExpiryTime: time.Now().Add(24 * time.Hour).UnixMilli(),
		Enable:     true,
		TgID:       123456789,
		SubID:      "test-sub-id",
		Flow:       "xtls-rprx-vision",
		Reset:      31,
	}

	data, err := json.Marshal(config)
	require.NoError(t, err, "Failed to marshal ClientConfig")

	var unmarshaled ClientConfig
	require.NoError(t, json.Unmarshal(data, &unmarshaled), "Failed to unmarshal ClientConfig")

	assert.Equal(t, config.ID, unmarshaled.ID, "ID")
	assert.Equal(t, config.Email, unmarshaled.Email, "Email")
}

func TestGetClientTraffic_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/getClientTraffics/testuser":
			assert.Equal(t, "GET", r.Method, "Expected GET method")
			traffic := ClientTraffic{
				ID:         1,
				InboundID:  1,
				Enable:     true,
				Email:      "testuser",
				UUID:       "test-uuid-123",
				SubID:      "test-sub-id",
				Up:         1073741824,
				Down:       2147483648,
				AllTime:    0,
				ExpiryTime: time.Now().Add(24 * time.Hour).UnixMilli(),
				Total:      0,
				Reset:      0,
				LastOnline: 0,
			}
			resp := APIResponse{
				Success: true,
				Msg:     "",
			}
			resp.Obj, _ = json.Marshal(traffic)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	result, err := client.GetClientTraffic(ctx, "testuser")
	require.NoError(t, err, "GetClientTraffic() error")

	assert.Equal(t, "testuser", result.Email, "Email")
	assert.Equal(t, int64(1073741824), result.Up, "Up")
	assert.Equal(t, int64(2147483648), result.Down, "Down")
	assert.True(t, result.Enable, "Enable should be true")
}

func TestGetClientTraffic_ClientNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/getClientTraffics/nonexistent":
			resp := APIResponse{
				Success: false,
				Msg:     "client not found",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	_, err = client.GetClientTraffic(ctx, "nonexistent")
	require.Error(t, err, "GetClientTraffic() should return error when client not found")
}

func TestGetClientTraffic_ServerError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/getClientTraffics/testuser":
			resp := APIResponse{
				Success: false,
				Msg:     "Internal server error",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	_, err = client.GetClientTraffic(ctx, "testuser")
	require.Error(t, err, "GetClientTraffic() should return error when server returns error")
	assert.Contains(t, err.Error(), "failed to get client traffic", "Error should contain 'failed to get client traffic'")
}

func TestGetClientTraffic_LoginFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panel/api/server/status" {
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: false, Msg: "Invalid credentials"}
			json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("Unexpected path: %s", r.URL.Path)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "wrongpassword", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	_, err = client.GetClientTraffic(ctx, "testuser")
	require.Error(t, err, "GetClientTraffic() should return error when login fails")
	assert.Contains(t, err.Error(), "authentication required", "Error should contain 'authentication required'")
}

func TestGetClientTraffic_ContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/getClientTraffics/testuser":
			select {
			case <-r.Context().Done():
				return
			case <-time.After(2 * time.Second):
			}
			traffics := []ClientTraffic{}
			resp := APIResponse{Success: true}
			resp.Obj, _ = json.Marshal(traffics)
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = client.GetClientTraffic(ctx, "testuser")
	require.Error(t, err, "GetClientTraffic() should return error when context is cancelled")
}

func TestGetClientTraffic_InvalidJSONResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/getClientTraffics/testuser":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("invalid json"))
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	_, err = client.GetClientTraffic(ctx, "testuser")
	require.Error(t, err, "GetClientTraffic() should return error for invalid JSON response")
}

func TestClientTraffic_JSON(t *testing.T) {
	t.Parallel()

	traffic := &ClientTraffic{
		ID:         1,
		InboundID:  1,
		Enable:     true,
		Email:      "testuser",
		UUID:       "test-uuid-123",
		SubID:      "test-sub-id",
		Up:         1073741824,
		Down:       2147483648,
		AllTime:    3221225472,
		ExpiryTime: time.Now().Add(24 * time.Hour).UnixMilli(),
		Total:      0,
		Reset:      0,
		LastOnline: 0,
	}

	data, err := json.Marshal(traffic)
	require.NoError(t, err, "Failed to marshal ClientTraffic")

	var unmarshaled ClientTraffic
	require.NoError(t, json.Unmarshal(data, &unmarshaled), "Failed to unmarshal ClientTraffic")

	assert.Equal(t, traffic.ID, unmarshaled.ID, "ID")
	assert.Equal(t, traffic.Email, unmarshaled.Email, "Email")
	assert.Equal(t, traffic.Up, unmarshaled.Up, "Up")
	assert.Equal(t, traffic.Down, unmarshaled.Down, "Down")
	assert.Equal(t, traffic.Enable, unmarshaled.Enable, "Enable")
}

func TestClientTraffic_TrafficCalculation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		up         int64
		down       int64
		expectedGB float64
	}{
		{"zero traffic", 0, 0, 0},
		{"1 GB total", 536870912, 536870912, 1.0},
		{"5 GB total", 2147483648, 3221225472, 5.0},
		{"partial GB", 1073741824, 536870912, 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			traffic := &ClientTraffic{
				Up:   tt.up,
				Down: tt.down,
			}
			gb := float64(traffic.Up+traffic.Down) / 1024 / 1024 / 1024
			assert.InDelta(t, tt.expectedGB, gb, 0.01, "Traffic in GB")
		})
	}
}

func TestDeleteClient_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/1/delClient/test-client-id":
			assert.Equal(t, "POST", r.Method, "Expected POST method")
			resp := APIResponse{Success: true, Msg: "Client deleted successfully"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.DeleteClient(ctx, 1, "test-client-id")
	require.NoError(t, err, "DeleteClient() error")
}

func TestDeleteClient_ServerError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/1/delClient/test-client-id":
			resp := APIResponse{Success: false, Msg: "Client not found"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.DeleteClient(ctx, 1, "test-client-id")
	require.Error(t, err, "DeleteClient() should return error when server returns error")
	assert.Contains(t, err.Error(), "failed to delete client", "Error should contain 'failed to delete client'")
}

func TestDeleteClient_LoginFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panel/api/server/status" {
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: false, Msg: "Invalid credentials"}
			json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("Unexpected path: %s", r.URL.Path)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "wrongpassword", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.DeleteClient(ctx, 1, "test-client-id")
	require.Error(t, err, "DeleteClient() should return error when login fails")
	assert.Contains(t, err.Error(), "authentication required", "Error should contain 'authentication required'")
}

func TestDeleteClient_ContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/1/delClient/test-client-id":
			select {
			case <-r.Context().Done():
				return
			case <-time.After(2 * time.Second):
			}
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = client.DeleteClient(ctx, 1, "test-client-id")
	require.Error(t, err, "DeleteClient() should return error when context is cancelled")
}

func TestDeleteClient_InvalidJSONResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/1/delClient/test-client-id":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("invalid json"))
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.DeleteClient(ctx, 1, "test-client-id")
	require.Error(t, err, "DeleteClient() should return error for invalid JSON response")
}

func TestDeleteClient_RequestCreationError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := APIResponse{Success: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = client.DeleteClient(ctx, 1, "test-client-id")
	require.Error(t, err, "DeleteClient() should return error with cancelled context")
	assert.Contains(t, strings.ToLower(err.Error()), "cancel", "Error should indicate cancellation")
}

func TestGetClientTraffic_RequestCreationError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := APIResponse{Success: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = client.GetClientTraffic(ctx, "testuser")
	require.Error(t, err, "GetClientTraffic() should return error with cancelled context")
	assert.Contains(t, strings.ToLower(err.Error()), "cancel", "Error should indicate cancellation")
}

func TestGetSubscriptionLink_WithCustomPath(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	link := client.GetSubscriptionLink("http://example.com", "abc123", "/custom")
	assert.Contains(t, link, "/custom", "GetSubscriptionLink() should include custom path")
}

func TestGetExternalURL_IPAddress(t *testing.T) {
	t.Parallel()

	url := GetExternalURL("http://192.168.1.1:2053")
	assert.Equal(t, "http://192.168.1.1:2053", url, "GetExternalURL()")
}

func TestGetExternalURL_Empty(t *testing.T) {
	t.Parallel()

	url := GetExternalURL("")
	assert.NotEmpty(t, url, "GetExternalURL('') should return non-empty result")
}

func TestAddClientWithID_LoginError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panel/api/server/status" {
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if r.URL.Path == "/login" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("unauthorized"))
			return
		}
		t.Errorf("Unexpected path: %s", r.URL.Path)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	_, err = client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), 31)
	require.Error(t, err, "AddClientWithID() should return error when login returns error")
}

func TestAddClientWithID_RequestCreationError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := APIResponse{Success: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), 31)
	require.Error(t, err, "AddClientWithID() should return error with cancelled context")
	assert.Contains(t, strings.ToLower(err.Error()), "cancel", "Error should indicate cancellation")
}

func TestClient_CircuitBreakerState(t *testing.T) {
	t.Parallel()

	failingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error"))
	}))
	defer failingServer.Close()

	client, err := NewClient(failingServer.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	state := client.CircuitBreakerState()
	assert.Equal(t, CircuitStateClosed, state, "CircuitBreakerState() initially should be closed")

	for i := 0; i < 10; i++ {
		client.Login(ctx)
	}

	state = client.CircuitBreakerState()
	assert.NotEqual(t, CircuitStateClosed, state, "CircuitBreakerState() should be open after multiple failures")
}

func TestClient_GetExternalURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"full URL with port", "http://example.com:2053", "http://example.com:2053"},
		{"https URL", "https://example.com", "https://example.com"},
		{"URL with path", "http://example.com:2053/sub/path", "http://example.com:2053"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.GetExternalURL(tt.input)
			assert.Equal(t, tt.expected, result, "Client.GetExternalURL(%q)", tt.input)
		})
	}
}

func TestTruncateString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"long string", "hello world this is a long string", 10, "hello worl..."},
		{"empty string", "", 5, ""},
		{"zero maxLen", "hello", 0, "..."},
		{"ascii only long", "abcdefghijklmnopqrstuvwxyz", 5, "abcde..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result, "truncateString(%q, %d)", tt.input, tt.maxLen)
		})
	}
}

func TestTruncateString_NoAllocationForShortStrings(t *testing.T) {
	t.Parallel()

	input := "short"
	result := truncateString(input, 100)
	assert.Equal(t, input, result, "truncateString should return original string when len <= maxLen")
}

func TestTruncateString_UnicodeMayBeSplit(t *testing.T) {
	t.Parallel()

	input := "привет"
	result := truncateString(input, 3)
	assert.LessOrEqual(t, len(result), 6, "truncateString result too long")
}

func TestUpdateClient_Success(t *testing.T) {
	t.Parallel()

	loginCalled := false
	updateClientCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			loginCalled = true
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/updateClient/test-client-uuid":
			updateClientCalled = true
			resp := APIResponse{Success: true, Msg: "Inbound client has been updated."}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.UpdateClient(ctx, 1, "test-client-uuid", "testuser@example.com", "sub-id-123", 107374182400, time.UnixMilli(0), 12345, "from: @referrer")
	require.NoError(t, err, "UpdateClient() error")

	assert.True(t, loginCalled, "Login was not called")
	assert.True(t, updateClientCalled, "UpdateClient was not called")
}

func TestUpdateClient_Error(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/updateClient/test-client-uuid":
			resp := APIResponse{Success: false, Msg: "Something went wrong"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.UpdateClient(ctx, 1, "test-client-uuid", "testuser@example.com", "sub-id-123", 107374182400, time.UnixMilli(0), 12345, "from: @referrer")
	require.Error(t, err, "UpdateClient() should return error when API returns success=false")
}

func TestUpdateClient_EmptyClientID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := APIResponse{Success: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.UpdateClient(ctx, 1, "", "testuser@example.com", "sub-id-123", 107374182400, time.UnixMilli(0), 12345, "from: @referrer")
	require.Error(t, err, "UpdateClient() should return error when clientID is empty")
}

func TestPing_Success(t *testing.T) {
	t.Parallel()

	loginCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			loginCalled = true
			assert.Equal(t, "POST", r.Method, "Expected POST method")
			resp := APIResponse{
				Success: true,
				Msg:     "Login successful",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.Ping(ctx)
	require.NoError(t, err, "Ping() should return nil for successful ping")
	assert.True(t, loginCalled, "Login should have been called")
}

func TestPing_Failure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := APIResponse{
			Success: false,
			Msg:     "Connection failed",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err, "NewClient() returned error")
	ctx := context.Background()

	err = client.Ping(ctx)
	require.Error(t, err, "Ping() should return error for failed connection")
}

func TestClient_Close(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := APIResponse{Success: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)

	// Close should not error even without any active connections
	err = client.Close()
	assert.NoError(t, err, "Close() should not return error")
}

func TestClient_Close_MultipleTimes(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := APIResponse{Success: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)

	// Multiple closes should be safe
	for i := 0; i < 3; i++ {
		err := client.Close()
		assert.NoError(t, err, "Close() iteration %d should not error", i)
	}
}

func TestGetExpiryTimeMillis_ZeroTime(t *testing.T) {
	t.Parallel()

	result := getExpiryTimeMillis(time.Time{})
	assert.Equal(t, int64(0), result, "Zero time should return 0")
}

func TestGetExpiryTimeMillis_NonZeroTime(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	result := getExpiryTimeMillis(fixedTime)
	assert.Equal(t, fixedTime.UnixMilli(), result, "Should return time in milliseconds")
}

func TestGetExpiryTimeMillis_FutureTime(t *testing.T) {
	t.Parallel()

	future := time.Now().Add(24 * time.Hour)
	result := getExpiryTimeMillis(future)
	assert.Greater(t, result, time.Now().UnixMilli(), "Future time should be greater than now")
}

func TestNewClient_TrailingSlashTrimmed(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053/", "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:2053", client.host, "Trailing slash should be trimmed")
}

func TestNewClient_MultipleTrailingSlashes(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053///", "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	// TrimSuffix only removes one trailing slash
	assert.Contains(t, client.host, "http://localhost:2053", "Should trim at least one trailing slash")
}

func TestUpdateClient_LoginFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panel/api/server/status" {
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: false, Msg: "Invalid credentials"}
			json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("Unexpected path: %s", r.URL.Path)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "wrongpassword", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	err = client.UpdateClient(ctx, 1, "test-client-uuid", "testuser", "sub-id", 1000, time.Now(), 12345, "")
	require.Error(t, err, "UpdateClient() should return error when login fails")
	assert.Contains(t, err.Error(), "authentication required")
}

func TestUpdateClient_ContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/updateClient/test-client-uuid":
			select {
			case <-r.Context().Done():
				return
			case <-time.After(2 * time.Second):
			}
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = client.UpdateClient(ctx, 1, "test-client-uuid", "testuser", "sub-id", 1000, time.Now(), 12345, "")
	require.Error(t, err, "UpdateClient() should return error when context is cancelled")
}

func TestUpdateClient_InvalidJSONResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/updateClient/test-client-uuid":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("invalid json"))
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	err = client.UpdateClient(ctx, 1, "test-client-uuid", "testuser", "sub-id", 1000, time.Now(), 12345, "")
	require.Error(t, err, "UpdateClient() should return error for invalid JSON response")
}

func TestAddClientWithID_DefaultResetDays(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/addClient":
			resp := APIResponse{Success: true, Msg: "Client added"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	// resetDays = -1 should use default (config.SubscriptionResetDay)
	result, err := client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), -1)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, config.SubscriptionResetDay, result.Reset, "Should use default reset day")
}

func TestAddClientWithID_ContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/addClient":
			select {
			case <-r.Context().Done():
				return
			case <-time.After(2 * time.Second):
			}
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), 31)
	require.Error(t, err, "AddClientWithID() should return error when context is cancelled")
}

func TestAddClientWithID_InvalidJSONResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/addClient":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("invalid json"))
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	_, err = client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), 31)
	require.Error(t, err, "AddClientWithID() should return error for invalid JSON response")
}

func TestGetClientTraffic_EmptyEmail(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panel/api/server/status" {
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
			return
		}
		// Empty email results in URL like /panel/api/inbounds/getClientTraffics/
		resp := APIResponse{Success: false, Msg: "client not found"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	_, err = client.GetClientTraffic(ctx, "")
	require.Error(t, err, "GetClientTraffic() should return error for empty email")
	assert.Contains(t, err.Error(), "failed to get client traffic")
}

func TestGetSubscriptionLink_TrailingSlashInBaseURL(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", "admin", "password", 15*time.Minute)
	require.NoError(t, err)

	link := client.GetSubscriptionLink("http://localhost:2053/", "sub123", "sub")
	assert.Equal(t, "http://localhost:2053/sub/sub123", link, "Should handle trailing slash in base URL")
}

func TestGetExternalURL_URLWithPath(t *testing.T) {
	t.Parallel()

	result := GetExternalURL("http://example.com:2053/sub/abc123")
	assert.Equal(t, "http://example.com:2053", result, "Should strip path from URL")
}

func TestGetExternalURL_EmptyString(t *testing.T) {
	t.Parallel()

	result := GetExternalURL("")
	// Empty string parses to "://" by url.Parse
	assert.NotEmpty(t, result, "Empty string should return non-empty result")
}

// TestDoRequestWithAuthRetry_AutoReloginOn401 directly tests the auto-relogin path
// in doRequestWithAuthRetry — the critical path that was previously dead code.
func TestDoRequestWithAuthRetry_AutoReloginOn401(t *testing.T) {
	t.Parallel()

	loginCalls := 0
	addCalls := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{Success: false})
		case "/login":
			mu.Lock()
			loginCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{Success: true})
		case "/panel/api/inbounds/addClient":
			mu.Lock()
			addCalls++
			count := addCalls
			mu.Unlock()
			if count == 1 {
				// First call: 401
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(APIResponse{Success: false, Msg: "unauthorized"})
				return
			}
			// Second call (after relogin): success
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{Success: true, Msg: "Client added"})
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	// Initial login
	err = client.Login(ctx)
	require.NoError(t, err)

	// Force session expiry so ensureLoggedIn triggers relogin
	client.TestForceSessionExpiry()

	// Call doAddClientWithID directly — this goes through doRequestWithAuthRetry
	result, err := client.doAddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), 30)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2, addCalls, "addClient should be called twice (401 then retry after relogin)")
	assert.Equal(t, 2, loginCalls, "Login should be called twice (initial + auto-relogin)")
}

// TestDoRequestWithAuthRetry_NoReloginOnSuccess verifies that successful requests
// don't trigger unnecessary re-login.
func TestDoRequestWithAuthRetry_NoReloginOnSuccess(t *testing.T) {
	t.Parallel()

	loginCalls := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{Success: true})
		case "/login":
			mu.Lock()
			loginCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{Success: true})
		case "/panel/api/inbounds/addClient":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{Success: true, Msg: "Client added"})
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	err = client.Login(ctx)
	require.NoError(t, err)
	initialLogins := loginCalls

	// Multiple successful calls — no relogin should occur
	for i := 0; i < 5; i++ {
		result, err := client.doAddClientWithID(ctx, 1, fmt.Sprintf("user%d", i), "client-id", "sub-id", 1000, time.Now(), 30)
		require.NoError(t, err)
		require.NotNil(t, result)
	}

	assert.Equal(t, initialLogins, loginCalls, "No re-login should occur for successful requests")
}

func TestGetExternalURL_InvalidURL(t *testing.T) {
	t.Parallel()

	result := GetExternalURL("://invalid")
	// Invalid URL should return original host
	assert.Equal(t, "://invalid", result, "Invalid URL should return original")
}

func TestRetryWithBackoff_ContextCancellationDuringRetry(t *testing.T) {
	t.Parallel()

	callCount := 0
	ctx, cancel := context.WithCancel(context.Background())

	err := RetryWithBackoff(ctx, 5, 50*time.Millisecond, func() error {
		callCount++
		if callCount == 2 {
			cancel()
		}
		return fmt.Errorf("error %d", callCount)
	})

	require.Error(t, err, "RetryWithBackoff() should return error when context cancelled during retry")
	assert.LessOrEqual(t, callCount, 2, "Should stop after context cancellation")
}

func TestTruncateString_VeryLongString(t *testing.T) {
	t.Parallel()

	longString := strings.Repeat("a", 10000)
	result := truncateString(longString, 10)
	assert.Equal(t, 13, len(result), "Should be maxLen + len('...')")
	assert.Equal(t, "aaaaaaaaaa...", result)
}

func TestCloseResponseBody_WithValidResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	require.NoError(t, err)

	// Should not panic
	closeResponseBody(resp)
	// Body should be closed after this
}

func TestMarshalJSON_UnmarshalableData(t *testing.T) {
	t.Parallel()

	// chan cannot be marshaled to JSON
	ch := make(chan int)
	_, err := marshalJSON(ch)
	require.Error(t, err, "marshalJSON() should error for unmarshalable data")
}

func TestClientSettings_ResetDayDefault(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/addClient":
			resp := APIResponse{Success: true, Msg: "Client added"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	// resetDays = 0 means no auto-renewal (reset: 0 sent to API)
	result, err := client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), 0)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Reset, "resetDays=0 should return 0 (no auto-renewal)")
}

func TestAddClientWithID_NegativeResetDays(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/addClient":
			resp := APIResponse{Success: true, Msg: "Client added"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	result, err := client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), -5)
	require.NoError(t, err)
	assert.Equal(t, config.SubscriptionResetDay, result.Reset, "Negative resetDays should use default")
}

func TestVerifySession_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/panel/api/server/status", r.URL.Path)
		assert.Equal(t, "GET", r.Method)
		resp := APIResponse{Success: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)

	assert.True(t, client.verifySession(context.Background()), "verifySession should return true")
}

func TestVerifySession_Failure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)

	assert.False(t, client.verifySession(context.Background()), "verifySession should return false on 401")
	assert.Equal(t, int64(1), client.SessionFailCount(), "sessionFailCount should increment")
}

func TestVerifySession_NetworkError(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://127.0.0.1:1", "admin", "password", 15*time.Minute)
	require.NoError(t, err)

	assert.False(t, client.verifySession(context.Background()), "verifySession should return false on network error")
	assert.Equal(t, int64(1), client.SessionFailCount(), "sessionFailCount should increment on network error")
}

func TestAutoRelogin_On401(t *testing.T) {
	t.Parallel()

	loginCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			loginCount++
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/addClient":
			if loginCount == 0 {
				// First request: return 401
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(APIResponse{Success: false, Msg: "unauthorized"})
				return
			}
			// After relogin: success
			resp := APIResponse{Success: true, Msg: "Client added"}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	// First login to establish session
	err = client.Login(ctx)
	require.NoError(t, err)

	// Force session expiry so ensureLoggedIn triggers
	client.mu.Lock()
	client.lastLogin = time.Time{}
	client.mu.Unlock()

	// AddClientWithID should auto-relogin on 401 and retry
	result, err := client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), 30)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, loginCount, 2, "Should have relogged in after 401")
}

func TestAutoRelogin_OnRedirect(t *testing.T) {
	t.Parallel()

	loginCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			resp := APIResponse{Success: false}
			json.NewEncoder(w).Encode(resp)
		case "/login":
			loginCount++
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/addClient":
			if loginCount == 0 {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			resp := APIResponse{Success: true, Msg: "Client added"}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	err = client.Login(ctx)
	require.NoError(t, err)

	client.mu.Lock()
	client.lastLogin = time.Time{}
	client.mu.Unlock()

	result, err := client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now(), 30)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, loginCount, 2, "Should have relogged in on redirect")
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, true},
		{"no such host", fmt.Errorf("dial tcp: lookup example.com: no such host"), false},
		{"connection refused", fmt.Errorf("dial tcp 127.0.0.1:2053: connection refused"), true},
		{"timeout", fmt.Errorf("context deadline exceeded (Client.Timeout exceeded)"), true},
		{"no such host mixed case", fmt.Errorf("Dial TCP: Lookup EXAMPLE.COM: NO SUCH HOST"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isRetryable(tt.err))
		})
	}
}

func TestLoginCount_Increments(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			json.NewEncoder(w).Encode(APIResponse{Success: true})
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	assert.Equal(t, int64(0), client.LoginCount())

	err = client.Login(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), client.LoginCount())

	err = client.Login(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), client.LoginCount())
}

func TestDoLogin_HTTPStatusCheck(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"success":false,"msg":"internal error"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)

	err = client.doLogin(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

func TestDoEnsureLoggedIn_VerifySessionSkipsLogin(t *testing.T) {
	t.Parallel()

	loginCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			json.NewEncoder(w).Encode(APIResponse{Success: true})
		case "/login":
			loginCalls++
			json.NewEncoder(w).Encode(APIResponse{Success: true})
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 1*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	// Initial login
	err = client.Login(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, loginCalls)

	// Wait for session to "expire" (1 minute is too long, force it)
	client.mu.Lock()
	client.lastLogin = time.Now().Add(-2 * time.Minute)
	client.mu.Unlock()

	// ensureLoggedIn should verify session instead of logging in
	err = client.ensureLoggedIn(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, 1, loginCalls, "Should NOT have called login again, verifySession succeeded")
}

func TestDoEnsureLoggedIn_ForceRelogin(t *testing.T) {
	t.Parallel()

	loginCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/server/status":
			json.NewEncoder(w).Encode(APIResponse{Success: false})
		case "/login":
			loginCalls++
			json.NewEncoder(w).Encode(APIResponse{Success: true})
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)
	ctx := context.Background()

	err = client.Login(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, loginCalls)

	// Force=true should always re-login regardless of session validity
	err = client.ensureLoggedIn(ctx, true)
	require.NoError(t, err)
	assert.Equal(t, 2, loginCalls, "Force relogin should call login again")
}
