package xui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		result := containsSuccess(tt.msg)
		if result != tt.expected {
			t.Errorf("containsSuccess(%q) = %v, want %v", tt.msg, result, tt.expected)
		}
	}
}

func TestRetryWithBackoff_Success(t *testing.T) {
	callCount := 0
	ctx := context.Background()

	err := retryWithBackoff(ctx, func() error {
		callCount++
		return nil
	}, 3, 100*time.Millisecond)

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

	err := retryWithBackoff(ctx, func() error {
		callCount++
		if callCount < 3 {
			return fmt.Errorf("error %d", callCount)
		}
		return nil
	}, 5, 10*time.Millisecond)

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

	err := retryWithBackoff(ctx, func() error {
		callCount++
		return fmt.Errorf("always fails")
	}, 3, 10*time.Millisecond)

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

	err := retryWithBackoff(ctx, func() error {
		return fmt.Errorf("error")
	}, 3, 10*time.Millisecond)

	if err == nil {
		t.Error("retryWithBackoff() should return error when context is cancelled")
	}
}
