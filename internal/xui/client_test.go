package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"rs8kvn_bot/internal/logger"
)

func TestMain(m *testing.M) {
	_, _ = logger.Init("", "error")
	os.Exit(m.Run())
}

const testAPIToken = "test-api-token"

func setupTestServer(handler http.HandlerFunc) (*httptest.Server, *Client) {
	server := httptest.NewServer(handler)
	client, err := NewClient(server.URL, testAPIToken)
	if err != nil {
		panic(err)
	}
	return server, client
}

func TestNewClient(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		client, err := NewClient("http://localhost:2053", testAPIToken)
		require.NoError(t, err)
		require.NotNil(t, client)
		assert.Equal(t, "http://localhost:2053", client.host)
		assert.Equal(t, testAPIToken, client.apiToken)
	})

	t.Run("trailing slash stripped", func(t *testing.T) {
		client, err := NewClient("http://localhost:2053/", testAPIToken)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:2053", client.host)
	})

	t.Run("multiple trailing slashes stripped", func(t *testing.T) {
		client, err := NewClient("http://localhost:2053///", testAPIToken)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:2053", client.host)
	})

	t.Run("no trailing slash", func(t *testing.T) {
		client, err := NewClient("http://localhost:2053", testAPIToken)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:2053", client.host)
	})
}

func TestPing(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		server, client := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/panel/api/server/status", r.URL.Path)
			assert.Equal(t, "Bearer "+testAPIToken, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
		})
		defer server.Close()
		defer client.Close()

		err := client.Ping(context.Background())
		assert.NoError(t, err)
	})
}

func TestMarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("valid input", func(t *testing.T) {
		input := map[string]string{"key": "value"}
		reader, err := marshalJSON(input)
		require.NoError(t, err)
		require.NotNil(t, reader)

		var result map[string]string
		err = json.NewDecoder(reader).Decode(&result)
		require.NoError(t, err)
		assert.Equal(t, "value", result["key"])
	})

	t.Run("nil input", func(t *testing.T) {
		reader, err := marshalJSON(nil)
		require.NoError(t, err)
		assert.NotNil(t, reader)

		var decoded interface{}
		err = json.NewDecoder(reader).Decode(&decoded)
		require.NoError(t, err)
		assert.Nil(t, decoded)
	})
}

func TestTruncateString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "hello", truncateString("hello", 10))
	assert.Equal(t, "hello...", truncateString("hello world", 5))
	assert.Equal(t, "", truncateString("", 5))
}

func TestGetExternalURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"https://example.com:2053/path", "https://example.com:2053"},
		{"http://localhost:2053", "http://localhost:2053"},
		{"invalid-url", "invalid-url"},
	{"https://example.com", "https://example.com"},
	{"", ""},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, GetExternalURL(tt.input))
	}
}

func TestGetSubscriptionLink(t *testing.T) {
	t.Parallel()

	client, err := NewClient("https://example.com", testAPIToken)
	require.NoError(t, err)

	result := client.GetSubscriptionLink("https://example.com", "sub123", "sub")
	assert.Equal(t, "https://example.com/sub/sub123", result)
}

func TestClientClose(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", testAPIToken)
	require.NoError(t, err)

	err = client.Close()
	assert.NoError(t, err)
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		retryable bool
	}{
		{"nil", nil, true},
		{"timeout", http.ErrHandlerTimeout, true},
		{"dns error", &net.DNSError{Err: "dns", Name: "example.com"}, false},
		{"no such host", fmt.Errorf("no such host"), false},
		{"temporary failure", fmt.Errorf("temporary failure in name resolution"), false},
		{"connection refused", fmt.Errorf("connection refused"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.retryable, isRetryable(tt.err))
		})
	}
}



func TestRetryWithBackoff_Success(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	err := RetryWithBackoff(context.Background(), 3, time.Millisecond, func() error {
		calls.Add(1)
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

func TestRetryWithBackoff_Retries(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	err := RetryWithBackoff(context.Background(), 3, time.Millisecond, func() error {
		calls.Add(1)
		if calls.Load() < 3 {
			return fmt.Errorf("transient error")
		}
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, int32(3), calls.Load())
}

func TestRetryWithBackoff_Exhausted(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	err := RetryWithBackoff(context.Background(), 3, time.Millisecond, func() error {
		calls.Add(1)
		return fmt.Errorf("persistent error")
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "persistent error")
	assert.Equal(t, int32(3), calls.Load())
}

func TestRetryWithBackoff_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RetryWithBackoff(ctx, 3, time.Millisecond, func() error {
		return fmt.Errorf("error")
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context cancelled")
}

func TestRetryWithBackoff_NonRetryable(t *testing.T) {
	var calls atomic.Int32
	err := RetryWithBackoff(context.Background(), 3, time.Millisecond, func() error {
		calls.Add(1)
		return &net.DNSError{Err: "dns", Name: "example.com"}
	})

	assert.Error(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

func TestGetExpiryTimeMillis(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int64(0), getExpiryTimeMillis(time.Time{}))
	assert.Equal(t, int64(0), getExpiryTimeMillis(time.Time{}))

	now := time.Now()
	result := getExpiryTimeMillis(now)
	assert.InDelta(t, now.UnixMilli(), result, 1000)
}

func TestDoHTTPRequest(t *testing.T) {
	t.Parallel()

	t.Run("successful GET", func(t *testing.T) {
		server, client := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "Bearer "+testAPIToken, r.Header.Get("Authorization"))
			assert.Equal(t, "application/json", r.Header.Get("Accept"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		})
		defer server.Close()
		defer client.Close()

		body, err := client.doHTTPRequest(context.Background(), http.MethodGet, server.URL+"/test", nil)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"success":true`)
	})

	t.Run("successful POST with body", func(t *testing.T) {
		server, client := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			body, _ := io.ReadAll(r.Body)
			assert.Contains(t, string(body), `"test"`)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true}`)
		})
		defer server.Close()
		defer client.Close()

		body, err := client.doHTTPRequest(context.Background(), http.MethodPost, server.URL+"/test", func() (io.Reader, error) {
			return strings.NewReader(`{"test":"data"}`), nil
		})
		require.NoError(t, err)
		assert.Contains(t, string(body), `"success":true`)
	})

	t.Run("non-200 status code", func(t *testing.T) {
		server, client := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `internal error`)
		})
		defer server.Close()
		defer client.Close()

		body, err := client.doHTTPRequest(context.Background(), http.MethodGet, server.URL+"/test", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP 500")
		assert.Contains(t, string(body), "internal error")
	})

	t.Run("context cancellation", func(t *testing.T) {
		server, client := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		})
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()

		_, err := client.doHTTPRequest(ctx, http.MethodGet, server.URL+"/test", nil)
		assert.Error(t, err)
	})

	t.Run("body function error", func(t *testing.T) {
		client, err := NewClient("http://localhost:2053", testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		_, err = client.doHTTPRequest(context.Background(), http.MethodPost, "http://localhost:2053/test", func() (io.Reader, error) {
			return nil, fmt.Errorf("body error")
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "body error")
	})

	t.Run("request creation error", func(t *testing.T) {
		client, err := NewClient("http://localhost:2053", testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		_, err = client.doHTTPRequest(context.Background(), http.MethodGet, "://invalid-url", nil)
		assert.Error(t, err)
	})

	t.Run("bodyFn marshal", func(t *testing.T) {
		server, client := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true}`)
		})
		defer server.Close()
		defer client.Close()

		body, err := client.doHTTPRequest(context.Background(), http.MethodPost, server.URL+"/test", func() (io.Reader, error) {
			return marshalJSON(map[string]string{"key": "value"})
		})
		require.NoError(t, err)
		assert.Contains(t, string(body), `"success":true`)
	})
}

func TestInbound_GetTransport(t *testing.T) {
	t.Parallel()

	t.Run("empty stream settings", func(t *testing.T) {
		in := &Inbound{StreamSettings: ""}
		assert.Equal(t, "", in.GetTransport())
	})

	t.Run("valid stream settings", func(t *testing.T) {
		in := &Inbound{StreamSettings: `{"network":"ws"}`}
		assert.Equal(t, "ws", in.GetTransport())
	})

	t.Run("invalid JSON", func(t *testing.T) {
		in := &Inbound{StreamSettings: `{invalid`}
		assert.Equal(t, "", in.GetTransport())
	})
}

func TestInbound_GetRequiredFlow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		transport string
		expected  string
	}{
		{"xhttp", ""},
		{"h2", ""},
		{"ws", ""},
		{"grpc", ""},
		{"grpcs", ""},
		{"tcp", "xtls-rprx-vision"},
		{"", "xtls-rprx-vision"},
	}

	for _, tt := range tests {
		t.Run(tt.transport, func(t *testing.T) {
			in := &Inbound{}
			if tt.transport != "" {
				in.StreamSettings = fmt.Sprintf(`{"network":"%s"}`, tt.transport)
			}
			assert.Equal(t, tt.expected, in.GetRequiredFlow())
		})
	}
}

func TestCloseResponseBody(t *testing.T) {
	t.Parallel()

	t.Run("nil response", func(t *testing.T) {
		closeResponseBody(nil)
	})

	t.Run("nil body", func(t *testing.T) {
		closeResponseBody(&http.Response{})
	})

	t.Run("valid body", func(t *testing.T) {
		closeResponseBody(&http.Response{
			Body: io.NopCloser(bytes.NewReader(nil)),
		})
	})
}

func TestAddClient(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "get/") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{"success":true,"obj":{"id":1,"streamSettings":"{\"network\":\"tcp\"}"}}`)
				return
			}
			assert.Equal(t, "/panel/api/inbounds/addClient", r.URL.Path)
			assert.Equal(t, "Bearer "+testAPIToken, r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		result, err := client.AddClient(context.Background(), 1, "test@example.com", 1<<30, time.Now().Add(24*time.Hour))
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "test@example.com", result.Email)
		assert.True(t, result.Enable)
	})

	t.Run("error on add", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "get/") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{"success":true,"obj":{"id":1}}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":false,"msg":"inbound not found"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		_, err = client.AddClient(context.Background(), 1, "test@example.com", 1<<30, time.Now().Add(24*time.Hour))
		assert.Error(t, err)
	})
}

func TestAddClientWithID_Validation(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	t.Run("invalid inbound ID", func(t *testing.T) {
		_, err := client.AddClientWithID(ctx, 0, "test@example.com", "uuid", "subid", 100, time.Now(), -1)
		assert.Error(t, err)
	})

	t.Run("empty client ID", func(t *testing.T) {
		_, err := client.AddClientWithID(ctx, 1, "test@example.com", "", "subid", 100, time.Now(), -1)
		assert.Error(t, err)
	})

	t.Run("empty sub ID", func(t *testing.T) {
		_, err := client.AddClientWithID(ctx, 1, "test@example.com", "uuid", "", 100, time.Now(), -1)
		assert.Error(t, err)
	})
}

func TestAddClientWithID(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		var callCount atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount.Add(1)
			if strings.Contains(r.URL.Path, "get/") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{"success":true,"obj":{"id":1}}`)
				return
			}

			assert.Equal(t, "/panel/api/inbounds/addClient", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		result, err := client.AddClientWithID(context.Background(), 1, "test@example.com", "some-uuid", "sub-123", 1<<30, time.Now().Add(24*time.Hour), 0)
		require.NoError(t, err)
		assert.Equal(t, "test@example.com", result.Email)
		assert.Equal(t, "some-uuid", result.ID)
		assert.Equal(t, "sub-123", result.SubID)
	})
}

func TestDeleteClient(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/panel/api/inbounds/1/delClient/test-uuid", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		err = client.DeleteClient(context.Background(), 1, "test-uuid")
		assert.NoError(t, err)
	})

	t.Run("api error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":false,"msg":"client not found"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		err = client.DeleteClient(context.Background(), 1, "nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client not found")
	})
}

func TestUpdateClient(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "get/") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{"success":true,"obj":{"id":1}}`)
				return
			}
			assert.Equal(t, "/panel/api/inbounds/updateClient/test-uuid", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		err = client.UpdateClient(context.Background(), 1, "test-uuid", "new@email.com", "sub-456", 1<<30, time.Now().Add(48*time.Hour), 12345, "test comment")
		assert.NoError(t, err)
	})

	t.Run("empty client ID", func(t *testing.T) {
		client, err := NewClient("http://localhost:2053", testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		err = client.UpdateClient(context.Background(), 1, "", "email", "sub", 0, time.Time{}, 0, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client ID cannot be empty")
	})

	t.Run("api error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "get/") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{"success":true,"obj":{"id":1}}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":false,"msg":"update failed"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		err = client.UpdateClient(context.Background(), 1, "test-uuid", "email", "sub", 0, time.Time{}, 0, "")
		assert.Error(t, err)
	})
}

func TestGetClientTraffic(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Contains(t, r.URL.Path, "test@example.com")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"obj":{"id":1,"inboundId":1,"enable":true,"email":"test@example.com","up":1000,"down":2000,"total":1073741824,"expiryTime":1893456000000}}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		traffic, err := client.GetClientTraffic(context.Background(), "test@example.com")
		require.NoError(t, err)
		assert.Equal(t, "test@example.com", traffic.Email)
		assert.Equal(t, int64(1000), traffic.Up)
		assert.Equal(t, int64(2000), traffic.Down)
	})

	t.Run("client not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":false,"msg":"client not found"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		_, err = client.GetClientTraffic(context.Background(), "nonexistent@example.com")
		assert.Error(t, err)
	})

	t.Run("invalid JSON response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `not json`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		_, err = client.GetClientTraffic(context.Background(), "test@example.com")
		assert.Error(t, err)
	})
}

func TestGetInbound(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/panel/api/inbounds/get/1", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"obj":{"id":1,"remark":"test","enable":true,"port":443,"protocol":"vless","streamSettings":"{\"network\":\"ws\"}"}}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		inbound, err := client.GetInbound(context.Background(), 1)
		require.NoError(t, err)
		assert.Equal(t, 1, inbound.ID)
		assert.Equal(t, "test", inbound.Remark)
		assert.True(t, inbound.Enable)
		assert.Equal(t, 443, inbound.Port)
		assert.Equal(t, "ws", inbound.GetTransport())
	})

	t.Run("not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":false,"msg":"inbound not found"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		_, err = client.GetInbound(context.Background(), 999)
		assert.Error(t, err)
	})
}

func TestGetRequiredFlow_Fallback(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	// When getInbound fails, should return default flow
	// Use guaranteed-unresolvable host to trigger error path
	client, err := NewClient("http://nonexistent.invalid:2053", testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	flow, err := client.getRequiredFlow(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "xtls-rprx-vision", flow)
}

func TestAPIResponseParsing(t *testing.T) {
	t.Parallel()

	t.Run("valid response", func(t *testing.T) {
		resp := APIResponse{Success: true, Msg: "ok", Obj: json.RawMessage(`{"key":"value"}`)}
		assert.True(t, resp.Success)
		assert.Equal(t, "ok", resp.Msg)
	})

	t.Run("error response", func(t *testing.T) {
		resp := APIResponse{Success: false, Msg: "error message"}
		assert.False(t, resp.Success)
		assert.Equal(t, "error message", resp.Msg)
	})
}

func TestHTTPStatusHandling(t *testing.T) {
	t.Parallel()

	t.Run("401 unauthorized", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `unauthorized`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		_, err = client.GetInbound(context.Background(), 1)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP 401")
	})

	t.Run("403 forbidden", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `forbidden`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		_, err = client.GetInbound(context.Background(), 1)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP 403")
	})

	t.Run("500 server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `server error`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		_, err = client.GetInbound(context.Background(), 1)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP 500")
	})
}
