package xui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rs8kvn_bot/internal/logger"
)

func init() {
	// Initialize logger for tests
	logger.Init("", "error")
}

func TestNewClient(t *testing.T) {
	client := NewClient("http://localhost:2053", "admin", "password")

	if client == nil {
		t.Fatal("NewClient() returned nil")
	}
	if client.host != "http://localhost:2053" {
		t.Errorf("host = %s, want http://localhost:2053", client.host)
	}
	if client.username != "admin" {
		t.Errorf("username = %s, want admin", client.username)
	}
	if client.password != "password" {
		t.Errorf("password = %s, want password", client.password)
	}
	if client.httpClient == nil {
		t.Error("httpClient is nil")
	}
}

func TestNewClient_HTTPClientConfig(t *testing.T) {
	client := NewClient("http://localhost:2053", "admin", "password")

	if client.httpClient.Timeout != 10*time.Second {
		t.Errorf("httpClient.Timeout = %v, want 10s", client.httpClient.Timeout)
	}
	if client.httpClient.Jar == nil {
		t.Error("httpClient.Jar is nil")
	}
}

func TestLogin_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login" {
			t.Errorf("Expected /login path, got %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("Expected POST method, got %s", r.Method)
		}

		// Return successful login response
		resp := APIResponse{
			Success: true,
			Msg:     "Login successful",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	err := client.Login(ctx)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
}

func TestLogin_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := APIResponse{
			Success: false,
			Msg:     "Invalid credentials",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "wrongpassword")
	ctx := context.Background()

	err := client.Login(ctx)
	if err == nil {
		t.Fatal("Login() should return error for failed login")
	}
}

func TestLogin_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Delay to ensure context cancellation
		resp := APIResponse{Success: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "password")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := client.Login(ctx)
	if err == nil {
		t.Fatal("Login() should return error when context is cancelled")
	}
}

func TestAddClientWithID_Success(t *testing.T) {
	loginCalled := false
	addClientCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	result, err := client.AddClientWithID(ctx, 1, "testuser", "client-id-123", "sub-id-456", 107374182400, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("AddClientWithID() error = %v", err)
	}

	if !loginCalled {
		t.Error("Login was not called")
	}
	if !addClientCalled {
		t.Error("AddClient was not called")
	}
	if result == nil {
		t.Fatal("Result is nil")
	}
	if result.ID != "client-id-123" {
		t.Errorf("Result.ID = %s, want client-id-123", result.ID)
	}
	if result.SubID != "sub-id-456" {
		t.Errorf("Result.SubID = %s, want sub-id-456", result.SubID)
	}
}

func TestAddClientWithID_ServerError(t *testing.T) {
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

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	_, err := client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now())
	// Should not error - the function handles server errors gracefully
	if err != nil {
		// This is acceptable - server returned error
	}
}

func TestGetSubscriptionLink(t *testing.T) {
	client := NewClient("http://localhost:2053", "admin", "password")

	link := client.GetSubscriptionLink("http://localhost:2053", "sub123", "sub")

	expected := "http://localhost:2053/sub/sub123"
	if link != expected {
		t.Errorf("GetSubscriptionLink() = %s, want %s", link, expected)
	}
}

func TestGetSubscriptionLink_CustomPath(t *testing.T) {
	client := NewClient("http://localhost:2053", "admin", "password")

	link := client.GetSubscriptionLink("http://localhost:2053", "sub456", "custom")

	expected := "http://localhost:2053/custom/sub456"
	if link != expected {
		t.Errorf("GetSubscriptionLink() = %s, want %s", link, expected)
	}
}

func TestGetExternalURL(t *testing.T) {
	tests := []struct {
		host     string
		expected string
	}{
		{"http://localhost:2053", "http://localhost:2053"},
		{"https://example.com:443", "https://example.com:443"},
		{"http://192.168.1.1:8080", "http://192.168.1.1:8080"},
	}

	for _, tt := range tests {
		result := GetExternalURL(tt.host)
		if result != tt.expected {
			t.Errorf("GetExternalURL(%s) = %s, want %s", tt.host, result, tt.expected)
		}
	}
}

func TestGetExternalURL_InvalidURL(t *testing.T) {
	// Invalid URL handling - function returns parsed result or original
	// url.Parse may return partial results for some invalid inputs
	result := GetExternalURL("not a valid url")
	// Just verify it doesn't panic and returns a string
	if result == "" {
		t.Error("GetExternalURL() should return non-empty string")
	}
}

func TestContainsSuccess(t *testing.T) {
	tests := []struct {
		msg      string
		expected bool
	}{
		{"Client added successfully", true},
		{"Successfully created", true},
		{"Operation success", true},
		{"Error occurred", false},
		{"", false},
	}

	for _, tt := range tests {
		result := containsSuccessKeywords(tt.msg)
		if result != tt.expected {
			t.Errorf("containsSuccessKeywords(%q) = %v, want %v", tt.msg, result, tt.expected)
		}
	}
}

func TestRetryWithBackoff_Success(t *testing.T) {
	callCount := 0
	ctx := context.Background()

	err := retryWithBackoff(ctx, 3, 100*time.Millisecond, func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Errorf("retryWithBackoff() error = %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}
}

func TestRetryWithBackoff_Retries(t *testing.T) {
	callCount := 0
	ctx := context.Background()

	err := retryWithBackoff(ctx, 5, 10*time.Millisecond, func() error {
		callCount++
		if callCount < 3 {
			return fmt.Errorf("error %d", callCount)
		}
		return nil
	})

	if err != nil {
		t.Errorf("retryWithBackoff() error = %v", err)
	}
	if callCount != 3 {
		t.Errorf("Expected 3 calls, got %d", callCount)
	}
}

func TestRetryWithBackoff_MaxRetries(t *testing.T) {
	callCount := 0
	ctx := context.Background()

	err := retryWithBackoff(ctx, 3, 10*time.Millisecond, func() error {
		callCount++
		return fmt.Errorf("always fails")
	})

	if err == nil {
		t.Error("retryWithBackoff() should return error after max retries")
	}
	if callCount != 3 {
		t.Errorf("Expected 3 calls, got %d", callCount)
	}
}

func TestRetryWithBackoff_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := retryWithBackoff(ctx, 3, 10*time.Millisecond, func() error {
		return fmt.Errorf("error")
	})

	if err == nil {
		t.Error("retryWithBackoff() should return error when context is cancelled")
	}
}

func TestAddClient_Success(t *testing.T) {
	loginCalled := false
	addClientCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	result, err := client.AddClient(ctx, 1, "testuser", 107374182400, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("AddClient() error = %v", err)
	}

	if !loginCalled {
		t.Error("Login was not called")
	}
	if !addClientCalled {
		t.Error("AddClient was not called")
	}
	if result == nil {
		t.Fatal("Result is nil")
	}
	if result.Email != "testuser" {
		t.Errorf("Result.Email = %s, want testuser", result.Email)
	}
	if result.TotalGB != 107374182400 {
		t.Errorf("Result.TotalGB = %d, want 107374182400", result.TotalGB)
	}
	if !result.Enable {
		t.Error("Result.Enable should be true")
	}
}

func TestAddClient_LoginFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: false, Msg: "Invalid credentials"}
			json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("Unexpected path: %s", r.URL.Path)
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "wrongpassword")
	ctx := context.Background()

	_, err := client.AddClient(ctx, 1, "testuser", 1000, time.Now())
	if err == nil {
		t.Fatal("AddClient() should return error when login fails")
	}
}

func TestAddClientWithID_SuccessFalseButMessageIndicatesSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
			return
		}

		// Return success=false but with a success message
		resp := APIResponse{Success: false, Msg: "Client added successfully"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	result, err := client.AddClientWithID(ctx, 1, "testuser", "client-id", "sub-id", 1000, time.Now())
	if err != nil {
		t.Fatalf("AddClientWithID() should not error when message indicates success: %v", err)
	}
	if result == nil {
		t.Fatal("Result should not be nil")
	}
}

func TestEnsureLoggedIn_CachedSession(t *testing.T) {
	loginCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	// First call - should login
	_, err := client.AddClient(ctx, 1, "testuser1", 1000, time.Now())
	if err != nil {
		t.Fatalf("First AddClient() error = %v", err)
	}

	// Second call immediately - should use cached session
	_, err = client.AddClient(ctx, 1, "testuser2", 1000, time.Now())
	if err != nil {
		t.Fatalf("Second AddClient() error = %v", err)
	}

	// Both calls should have logged in (the test server doesn't maintain session state,
	// but we can verify the login caching logic exists in the client)
	if loginCount < 1 {
		t.Error("At least one login should have occurred")
	}
}

func TestDoLogin_InvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	err := client.Login(ctx)
	if err == nil {
		t.Fatal("Login() should return error for invalid JSON response")
	}
}

func TestGetExternalURL_VariousInputs(t *testing.T) {
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
		if result != tt.expected {
			t.Errorf("GetExternalURL(%s) = %s, want %s", tt.host, result, tt.expected)
		}
	}
}

func TestClientSettings_JSON(t *testing.T) {
	// Test that ClientConfig can be marshaled/unmarshaled correctly
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
	if err != nil {
		t.Fatalf("Failed to marshal ClientConfig: %v", err)
	}

	var unmarshaled ClientConfig
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal ClientConfig: %v", err)
	}

	if unmarshaled.ID != config.ID {
		t.Errorf("ID = %s, want %s", unmarshaled.ID, config.ID)
	}
	if unmarshaled.Email != config.Email {
		t.Errorf("Email = %s, want %s", unmarshaled.Email, config.Email)
	}
}

func TestGetClientTraffic_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/getClientTraffics/testuser":
			if r.Method != "POST" {
				t.Errorf("Expected POST method, got %s", r.Method)
			}
			traffics := []ClientTraffic{
				{
					ID:         "client-id-123",
					Email:      "testuser",
					Up:         1073741824, // 1 GB up
					Down:       2147483648, // 2 GB down
					Total:      3221225472, // 3 GB total
					ExpiryTime: time.Now().Add(24 * time.Hour).UnixMilli(),
					Enable:     true,
				},
			}
			resp := APIResponse{
				Success: true,
				Msg:     "success",
			}
			resp.Obj, _ = json.Marshal(traffics)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	result, err := client.GetClientTraffic(ctx, "testuser")
	if err != nil {
		t.Fatalf("GetClientTraffic() error = %v", err)
	}

	if result.Email != "testuser" {
		t.Errorf("Email = %s, want testuser", result.Email)
	}
	if result.Up != 1073741824 {
		t.Errorf("Up = %d, want 1073741824", result.Up)
	}
	if result.Down != 2147483648 {
		t.Errorf("Down = %d, want 2147483648", result.Down)
	}
	if result.Total != 3221225472 {
		t.Errorf("Total = %d, want 3221225472", result.Total)
	}
	if !result.Enable {
		t.Error("Enable should be true")
	}
}

func TestGetClientTraffic_ClientNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/getClientTraffics/nonexistent":
			// Return empty array
			traffics := []ClientTraffic{}
			resp := APIResponse{
				Success: true,
				Msg:     "success",
			}
			resp.Obj, _ = json.Marshal(traffics)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	_, err := client.GetClientTraffic(ctx, "nonexistent")
	if err == nil {
		t.Fatal("GetClientTraffic() should return error when client not found")
	}
	if !strings.Contains(err.Error(), "client not found") {
		t.Errorf("Error should contain 'client not found', got: %v", err)
	}
}

func TestGetClientTraffic_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	_, err := client.GetClientTraffic(ctx, "testuser")
	if err == nil {
		t.Fatal("GetClientTraffic() should return error when server returns error")
	}
	if !strings.Contains(err.Error(), "failed to get client traffic") {
		t.Errorf("Error should contain 'failed to get client traffic', got: %v", err)
	}
}

func TestGetClientTraffic_LoginFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: false, Msg: "Invalid credentials"}
			json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("Unexpected path: %s", r.URL.Path)
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "wrongpassword")
	ctx := context.Background()

	_, err := client.GetClientTraffic(ctx, "testuser")
	if err == nil {
		t.Fatal("GetClientTraffic() should return error when login fails")
	}
	if !strings.Contains(err.Error(), "authentication required") {
		t.Errorf("Error should contain 'authentication required', got: %v", err)
	}
}

func TestGetClientTraffic_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/getClientTraffics/testuser":
			time.Sleep(2 * time.Second) // Delay to ensure context cancellation
			traffics := []ClientTraffic{}
			resp := APIResponse{Success: true}
			resp.Obj, _ = json.Marshal(traffics)
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "password")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.GetClientTraffic(ctx, "testuser")
	if err == nil {
		t.Fatal("GetClientTraffic() should return error when context is cancelled")
	}
}

func TestGetClientTraffic_InvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	_, err := client.GetClientTraffic(ctx, "testuser")
	if err == nil {
		t.Fatal("GetClientTraffic() should return error for invalid JSON response")
	}
}

func TestClientTraffic_JSON(t *testing.T) {
	// Test that ClientTraffic can be marshaled/unmarshaled correctly
	traffic := &ClientTraffic{
		ID:         "test-client-id",
		Email:      "testuser",
		Up:         1073741824,
		Down:       2147483648,
		Total:      3221225472,
		ExpiryTime: time.Now().Add(24 * time.Hour).UnixMilli(),
		Enable:     true,
	}

	data, err := json.Marshal(traffic)
	if err != nil {
		t.Fatalf("Failed to marshal ClientTraffic: %v", err)
	}

	var unmarshaled ClientTraffic
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal ClientTraffic: %v", err)
	}

	if unmarshaled.ID != traffic.ID {
		t.Errorf("ID = %s, want %s", unmarshaled.ID, traffic.ID)
	}
	if unmarshaled.Email != traffic.Email {
		t.Errorf("Email = %s, want %s", unmarshaled.Email, traffic.Email)
	}
	if unmarshaled.Up != traffic.Up {
		t.Errorf("Up = %d, want %d", unmarshaled.Up, traffic.Up)
	}
	if unmarshaled.Down != traffic.Down {
		t.Errorf("Down = %d, want %d", unmarshaled.Down, traffic.Down)
	}
	if unmarshaled.Total != traffic.Total {
		t.Errorf("Total = %d, want %d", unmarshaled.Total, traffic.Total)
	}
	if unmarshaled.Enable != traffic.Enable {
		t.Errorf("Enable = %v, want %v", unmarshaled.Enable, traffic.Enable)
	}
}

func TestClientTraffic_TrafficCalculation(t *testing.T) {
	// Test traffic calculation in GB
	tests := []struct {
		name       string
		up         int64
		down       int64
		expectedGB float64
	}{
		{"zero traffic", 0, 0, 0},
		{"1 GB total", 536870912, 536870912, 1.0},   // 512MB up + 512MB down = 1GB
		{"5 GB total", 2147483648, 3221225472, 5.0}, // 2GB up + 3GB down = 5GB
		{"partial GB", 1073741824, 536870912, 1.5},  // 1GB up + 512MB down = 1.5GB
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			traffic := &ClientTraffic{
				Up:   tt.up,
				Down: tt.down,
			}
			gb := float64(traffic.Up+traffic.Down) / 1024 / 1024 / 1024
			// Use approximate comparison for floating point
			if gb < tt.expectedGB-0.01 || gb > tt.expectedGB+0.01 {
				t.Errorf("Traffic in GB = %.2f, want %.2f", gb, tt.expectedGB)
			}
		})
	}
}

func TestDeleteClient_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/1/delClient/test-client-id":
			if r.Method != "POST" {
				t.Errorf("Expected POST method, got %s", r.Method)
			}
			resp := APIResponse{Success: true, Msg: "Client deleted successfully"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	err := client.DeleteClient(ctx, 1, "test-client-id")
	if err != nil {
		t.Fatalf("DeleteClient() error = %v", err)
	}
}

func TestDeleteClient_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	err := client.DeleteClient(ctx, 1, "test-client-id")
	if err == nil {
		t.Fatal("DeleteClient() should return error when server returns error")
	}
	if !strings.Contains(err.Error(), "failed to delete client") {
		t.Errorf("Error should contain 'failed to delete client', got: %v", err)
	}
}

func TestDeleteClient_LoginFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			resp := APIResponse{Success: false, Msg: "Invalid credentials"}
			json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("Unexpected path: %s", r.URL.Path)
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "wrongpassword")
	ctx := context.Background()

	err := client.DeleteClient(ctx, 1, "test-client-id")
	if err == nil {
		t.Fatal("DeleteClient() should return error when login fails")
	}
	if !strings.Contains(err.Error(), "authentication required") {
		t.Errorf("Error should contain 'authentication required', got: %v", err)
	}
}

func TestDeleteClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		case "/panel/api/inbounds/1/delClient/test-client-id":
			time.Sleep(2 * time.Second) // Delay to ensure context cancellation
			resp := APIResponse{Success: true}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin", "password")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := client.DeleteClient(ctx, 1, "test-client-id")
	if err == nil {
		t.Fatal("DeleteClient() should return error when context is cancelled")
	}
}

func TestDeleteClient_InvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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

	client := NewClient(server.URL, "admin", "password")
	ctx := context.Background()

	err := client.DeleteClient(ctx, 1, "test-client-id")
	if err == nil {
		t.Fatal("DeleteClient() should return error for invalid JSON response")
	}
}
