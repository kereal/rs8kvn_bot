package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	_, _ = logger.Init("", "error")

	goleak.VerifyTestMain(m)
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
}

func TestClient_RequestMetricsUseConfiguredNode(t *testing.T) {
	t.Parallel()

	server, client := setupTestServer(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()
	defer client.Close()

	client.SetNode("metrics-test-node")
	before := promtestutil.ToFloat64(metrics.XUIRequestsTotal.WithLabelValues("server.status", "success", "metrics-test-node"))
	require.NoError(t, client.Ping(context.Background()))
	require.Equal(t, before+1, promtestutil.ToFloat64(metrics.XUIRequestsTotal.WithLabelValues("server.status", "success", "metrics-test-node")))
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
		reader, err := marshalJSON[any](nil)
		require.NoError(t, err)
		assert.NotNil(t, reader)

		var decoded any

		err = json.NewDecoder(reader).Decode(&decoded)
		require.NoError(t, err)
		assert.Nil(t, decoded)
	})
}

func TestGetExpiresAtMillis(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int64(0), getExpiresAtMillis(time.Time{}))

	now := time.Now()
	result := getExpiresAtMillis(now)
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
		assert.Contains(t, err.Error(), "upstream returned non-200")
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
		in := &Inbound{StreamSettings: nil}
		assert.Equal(t, "", in.GetTransport())
	})

	t.Run("valid stream settings", func(t *testing.T) {
		in := &Inbound{StreamSettings: json.RawMessage(`{"network":"ws"}`)}
		assert.Equal(t, "ws", in.GetTransport())
	})

	t.Run("invalid JSON", func(t *testing.T) {
		in := &Inbound{StreamSettings: json.RawMessage(`{invalid`)}
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
		{"hysteria2", ""},
		{"shadowsocks", ""},
		{"tuic", ""},
		{"", "xtls-rprx-vision"},
	}

	for _, tt := range tests {
		t.Run(tt.transport, func(t *testing.T) {
			in := &Inbound{}
			if tt.transport != "" {
				in.StreamSettings = json.RawMessage(fmt.Sprintf(`{"network":"%s"}`, tt.transport))
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

// TestAddClient_AutoGeneratesIdentity: обёртка AddClient сама генерирует
// clientID и subID (client.go AddClient) — панель обязана получить непустые
// uuid и subId. Поведение панели (add/update payload, ошибки) покрыто
// тестами TestAddClientWithID* и TestPanelContract_*.
func TestAddClient_AutoGeneratesIdentity(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "get/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"obj":{"id":1,"streamSettings":"{\"network\":\"tcp\"}"}}`)

			return
		}

		body, _ := io.ReadAll(r.Body)

		var payload struct {
			Client ClientConfig `json:"client"`
		}
		require.NoError(t, json.Unmarshal(body, &payload))

		// Панельный add-payload несёт сгенерированный uuid в поле id
		// (см. doAddClientWithID: clientObj["id"] = req.ClientID).
		assert.NotEmpty(t, payload.Client.ID, "AddClient must auto-generate client uuid")
		assert.NotEmpty(t, payload.Client.SubID, "AddClient must auto-generate subId")
		assert.Equal(t, "test@example.com", payload.Client.Email)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)

	defer client.Close()

	result, err := client.AddClient(context.Background(), []int{1}, "test@example.com", 1<<30, time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.ID, "auto-generated client id must be echoed back")
	assert.Equal(t, "test@example.com", result.Email)
	assert.True(t, result.Enable)
}

func TestAddClientWithID_SurfacesPanelMsg(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "get/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"obj":{"id":1}}`)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"success":false,"msg":"A client with this email already exists"}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)

	defer client.Close()

	_, err = client.AddClientWithID(context.Background(), ClientRequest{
		InboundIDs: []int{1}, Email: "dup@example.com", ClientID: "uuid", SubID: "sub", TrafficBytes: 100, ExpiryTime: time.Now(), ResetDays: 0,
	})
	require.Error(t, err)
	// Core fix: the panel message must be surfaced so the sync classifier
	// can detect "already exists" and resolve the duplicate via the idempotent path.
	assert.ErrorContains(t, err, "A client with this email already exists")
}

func TestAddClientWithID_Validation(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://localhost:2053", testAPIToken)
	require.NoError(t, err)

	defer client.Close()

	ctx := context.Background()

	t.Run("empty inbound IDs", func(t *testing.T) {
		_, err := client.AddClientWithID(ctx, ClientRequest{InboundIDs: []int{}, Email: "test@example.com", ClientID: "uuid", SubID: "subid", TrafficBytes: 100, ExpiryTime: time.Now(), ResetDays: -1})
		assert.Error(t, err)
	})

	t.Run("empty client ID", func(t *testing.T) {
		_, err := client.AddClientWithID(ctx, ClientRequest{InboundIDs: []int{1}, Email: "test@example.com", ClientID: "", SubID: "subid", TrafficBytes: 100, ExpiryTime: time.Now(), ResetDays: -1})
		assert.Error(t, err)
	})

	t.Run("empty sub ID", func(t *testing.T) {
		_, err := client.AddClientWithID(ctx, ClientRequest{InboundIDs: []int{1}, Email: "test@example.com", ClientID: "uuid", SubID: "", TrafficBytes: 100, ExpiryTime: time.Now(), ResetDays: -1})
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

			assert.Equal(t, "/panel/api/clients/add", r.URL.Path)
			body, _ := io.ReadAll(r.Body)
			assert.Contains(t, string(body), `"client"`)
			assert.Contains(t, string(body), `"inboundIds":[1]`)
			assert.Contains(t, string(body), `"id":"some-uuid"`)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)

		defer client.Close()

		result, err := client.AddClientWithID(context.Background(), ClientRequest{InboundIDs: []int{1}, Email: "test@example.com", ClientID: "some-uuid", SubID: "sub-123", TrafficBytes: 1 << 30, ExpiryTime: time.Now().Add(24 * time.Hour), ResetDays: 0})
		require.NoError(t, err)
		assert.Equal(t, "test@example.com", result.Email)
		assert.Equal(t, "some-uuid", result.ID)
		assert.Equal(t, "sub-123", result.SubID)
		assert.Equal(t, 0, result.Reset, "ResetDays=0 must stay 0")
	})

	t.Run("expired_update_moves_expiry_to_next_reset_cycle", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/inbounds/get/") {
				fmt.Fprint(w, `{"success":true,"obj":{"id":1}}`)
				return
			}
			if r.Method == http.MethodGet || !strings.Contains(r.URL.Path, "/update/") {
				fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
				return
			}
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(body, &payload))
			assert.Equal(t, float64(config.SubscriptionResetDay), payload["reset"])
			assert.Equal(t, float64(0), payload["resetDay"])
			assert.Equal(t, float64(0), payload["resetMax"])
			assert.Equal(t, "monthly", payload["trafficReset"])
			assert.Equal(t, float64(1), payload["trafficResetDay"])
			expiry, ok := payload["expiryTime"].(float64)
			require.True(t, ok)
			assert.Greater(t, expiry, float64(time.Now().UnixMilli()))
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		err = client.UpdateClient(context.Background(), ClientRequest{
			InboundIDs: []int{1}, CurrentEmail: "expired@example.com", Email: "expired@example.com",
			ClientID: "expired-uuid", SubID: "expired-sub", TrafficBytes: 1 << 30,
			ExpiryTime: time.Now().Add(-time.Hour), ResetDays: -1, TrafficReset: "monthly", TrafficResetDay: 1,
		})
		require.NoError(t, err)
	})

	t.Run("add_free_profile_sends_auto_renew_fields", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "get/") {
				fmt.Fprint(w, `{"success":true,"obj":{"id":1}}`)
				return
			}
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var wrapper struct {
				Client map[string]any `json:"client"`
			}
			require.NoError(t, json.Unmarshal(body, &wrapper))
			assert.Equal(t, float64(config.SubscriptionResetDay), wrapper.Client["reset"])
			assert.Equal(t, float64(0), wrapper.Client["resetDay"])
			assert.Equal(t, float64(0), wrapper.Client["resetMax"])
			assert.Equal(t, "monthly", wrapper.Client["trafficReset"])
			assert.Equal(t, float64(1), wrapper.Client["trafficResetDay"])
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		_, err = client.AddClientWithID(context.Background(), ClientRequest{
			InboundIDs: []int{1}, Email: "free@example.com", ClientID: "free-uuid", SubID: "free-sub",
			TrafficBytes: 1 << 30, ExpiryTime: time.Now().Add(24 * time.Hour),
			ResetDays: -1, ResetDay: 0, ResetMax: 0, TrafficReset: "monthly", TrafficResetDay: 1,
		})
		require.NoError(t, err)
	})

	t.Run("add_without_profile_omits_auto_renew_fields", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "get/") {
				fmt.Fprint(w, `{"success":true,"obj":{"id":1}}`)
				return
			}
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var wrapper struct {
				Client map[string]any `json:"client"`
			}
			require.NoError(t, json.Unmarshal(body, &wrapper))
			_, hasResetDay := wrapper.Client["resetDay"]
			_, hasResetMax := wrapper.Client["resetMax"]
			_, hasTrafficReset := wrapper.Client["trafficReset"]
			_, hasTrafficResetDay := wrapper.Client["trafficResetDay"]
			assert.False(t, hasResetDay, "resetDay must be omitted without an auto-renew profile")
			assert.False(t, hasResetMax, "resetMax must be omitted without an auto-renew profile")
			assert.False(t, hasTrafficReset, "trafficReset must be omitted without an auto-renew profile")
			assert.False(t, hasTrafficResetDay, "trafficResetDay must be omitted without an auto-renew profile")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		_, err = client.AddClientWithID(context.Background(), ClientRequest{InboundIDs: []int{1}, Email: "plain@example.com", ClientID: "plain-uuid", SubID: "plain-sub", TrafficBytes: 1 << 30, ExpiryTime: time.Now().Add(24 * time.Hour), ResetDays: -1})
		require.NoError(t, err)
	})
}

func TestDeleteClient(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/panel/api/clients/del/test-email", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)

		defer client.Close()

		err = client.DeleteClient(context.Background(), "test-email")
		assert.NoError(t, err)
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

			assert.Contains(t, r.URL.Path, "/panel/api/clients/update/old-email")
			body, _ := io.ReadAll(r.Body)
			assert.Contains(t, string(body), `"email":"new@email.com"`)
			assert.Contains(t, string(body), `"totalGB"`)
			assert.Contains(t, string(body), `"tgId":12345`)
			assert.NotContains(t, string(body), `"clients"`) // no old nested wrapper
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)

		defer client.Close()

		err = client.UpdateClient(context.Background(), ClientRequest{InboundIDs: []int{1}, CurrentEmail: "old-email", ClientID: "test-uuid", Email: "new@email.com", SubID: "sub-456", TrafficBytes: 1 << 30, ExpiryTime: time.Now().Add(48 * time.Hour), ResetDays: -1, TgID: 12345, Comment: "test comment"})
		assert.NoError(t, err)
	})

	t.Run("empty client ID", func(t *testing.T) {
		client, err := NewClient("http://localhost:2053", testAPIToken)
		require.NoError(t, err)

		defer client.Close()

		err = client.UpdateClient(context.Background(), ClientRequest{InboundIDs: []int{1}, CurrentEmail: "current-email", ClientID: "", Email: "email", SubID: "sub", TrafficBytes: 0, ExpiryTime: time.Time{}, ResetDays: -1, TgID: 0, Comment: ""})
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

		err = client.UpdateClient(context.Background(), ClientRequest{InboundIDs: []int{1}, CurrentEmail: "current-email", ClientID: "test-uuid", Email: "email", SubID: "sub", TrafficBytes: 0, ExpiryTime: time.Time{}, ResetDays: -1, TgID: 0, Comment: ""})
		assert.Error(t, err)
	})

	t.Run("non_expired_update_keeps_expiry", func(t *testing.T) {
		// A client whose expiry is still in the future must keep it: the
		// recovery fallback only moves already-expired free clients.
		future := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Millisecond)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "get/") {
				fmt.Fprint(w, `{"success":true,"obj":{"id":1}}`)
				return
			}
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(body, &payload))
			expiry, ok := payload["expiryTime"].(float64)
			require.True(t, ok)
			assert.Equal(t, float64(future.UnixMilli()), expiry, "non-expired expiry must not be moved")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		err = client.UpdateClient(context.Background(), ClientRequest{
			InboundIDs: []int{1}, CurrentEmail: "future@example.com", Email: "future@example.com",
			ClientID: "future-uuid", SubID: "future-sub", TrafficBytes: 1 << 30,
			ExpiryTime: future, ResetDays: -1, TrafficReset: "monthly", TrafficResetDay: 1,
		})
		require.NoError(t, err)
	})

	t.Run("update_without_profile_omits_auto_renew_fields", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "get/") {
				fmt.Fprint(w, `{"success":true,"obj":{"id":1}}`)
				return
			}
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(body, &payload))
			_, hasResetDay := payload["resetDay"]
			_, hasResetMax := payload["resetMax"]
			_, hasTrafficReset := payload["trafficReset"]
			_, hasTrafficResetDay := payload["trafficResetDay"]
			assert.False(t, hasResetDay, "resetDay must be omitted without an auto-renew profile")
			assert.False(t, hasResetMax, "resetMax must be omitted without an auto-renew profile")
			assert.False(t, hasTrafficReset, "trafficReset must be omitted without an auto-renew profile")
			assert.False(t, hasTrafficResetDay, "trafficResetDay must be omitted without an auto-renew profile")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)
		defer client.Close()

		err = client.UpdateClient(context.Background(), ClientRequest{
			InboundIDs: []int{1}, CurrentEmail: "plain@example.com", Email: "plain@example.com",
			ClientID: "plain-uuid", SubID: "plain-sub", TrafficBytes: 1 << 30,
			ExpiryTime: time.Now().Add(24 * time.Hour), ResetDays: -1,
		})
		require.NoError(t, err)
	})
}

func TestGetClientTraffic(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Note: r.URL.Path is decoded by Go's http server (test@example.com, not %40)
			assert.Equal(t, "/panel/api/clients/traffic/test@example.com", r.URL.Path)
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

// GetClientByEmail теперь читает выделенный эндпоинт GET /clients/get/{email};
// контрактные тесты — в этом же файле, группа TestPanelContract_*.

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
}

func TestGetRequiredFlow_Fallback(t *testing.T) {
	t.Parallel()

	// When getInbound fails, surface the wrapped error instead of guessing a flow.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream error"))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)

	defer client.Close()

	flow, err := client.getRequiredFlow(context.Background(), 1)
	require.Error(t, err)
	assert.Empty(t, flow)
	assert.Contains(t, err.Error(), "get inbound for flow")
}

func TestExtractOperation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"add client", "http://host/panel/api/clients/add", "clients.add"},
		{"delete client", "http://host/panel/api/clients/del/email", "clients.del"},
		{"update client", "http://host/panel/api/clients/update/email", "clients.update"},
		{"traffic", "http://host/panel/api/clients/traffic/email", "clients.traffic"},
		{"get client", "http://host/panel/api/clients/get/email", "clients.get"},
		{"inbound", "http://host/panel/api/inbounds/get/1", "inbounds.get"},
		{"server status", "http://host/panel/api/server/status", "server.status"},
		{"unknown path", "http://host/other/path", "unknown"},
		{"empty", "", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, extractOperation(tt.url))
		})
	}
}

// Контрактные тесты панели 3x-ui: форма конверта {success, msg, obj},
// вложенность карточки клиента и точные тексты ошибок из
// doc/architecture.md, раздел «Контракт API панели 3x-ui»
// (раздел 3, проверено на живой панели 2026-10-31).
// Тексты классифицируются в classify.go.

// newPanelFixture запускает панель-заглушку: GET /panel/api/inbounds/get/{id}
// отвечает минимальным inbound (нужен для группировки flow в update/add),
// остальные запросы — в handler тестируемого сценария.
func newPanelFixture(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/panel/api/inbounds/get/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"success":true,"obj":{"id":1,"streamSettings":"{\"network\":\"tcp\"}"}}`)
	})
	mux.HandleFunc("/", handler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server
}

func TestPanelContract_EnvelopeForm(t *testing.T) {
	t.Parallel()

	// Конверт: все клиентские эндпоинты отвечают HTTP 200 с {success,msg,obj};
	// распознаётся и success=false, и success=true с пустым obj.
	t.Run("success=false with msg", func(t *testing.T) {
		obj, err := parseEnvelope(opUpdate, []byte(`{"success":false,"msg":"Something went wrong (record not found)","obj":null}`))
		require.Nil(t, obj)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrClientNotFound)
	})

	t.Run("success=true with obj", func(t *testing.T) {
		obj, err := parseEnvelope(opUpdate, []byte(`{"success":true,"msg":"ok","obj":{"id":1}}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"id":1}`, string(obj))
	})

	t.Run("malformed json", func(t *testing.T) {
		_, err := parseEnvelope(opDel, []byte(`not json`))
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to parse del response")
	})
}

func TestPanelContract_Traffic_NullObjIsClientNotFound(t *testing.T) {
	t.Parallel()

	// Регрессия: панель отвечает success=true и obj=null на отсутствующий
	// email — это НЕ nil-ответ и НЕ нули трафика, а ErrClientNotFound.
	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/panel/api/clients/traffic/ghost@example.com", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"msg":"","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	traffic, trafficErr := client.GetClientTraffic(context.Background(), "ghost@example.com")
	require.Error(t, trafficErr)
	assert.Nil(t, traffic, "obj=null must not be parsed into a zero traffic value")
	assert.ErrorIs(t, trafficErr, ErrClientNotFound)
}

func TestPanelContract_Traffic_ClientNotFoundMsg(t *testing.T) {
	t.Parallel()

	// Совместимость со старым поведением панели: success=false и
	// msg содержит "client not found".
	server := newPanelFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"msg":"client not found","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	_, trafficErr := client.GetClientTraffic(context.Background(), "ghost@example.com")
	assert.ErrorIs(t, trafficErr, ErrClientNotFound)
}

func TestPanelContract_Update_RecordNotFound(t *testing.T) {
	t.Parallel()

	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/panel/api/clients/update/ghost@example.com", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"msg":"Something went wrong (record not found)","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.UpdateClient(context.Background(), ClientRequest{
		InboundIDs: []int{1}, CurrentEmail: "ghost@example.com", Email: "ghost@example.com",
		ClientID: "uuid", SubID: "sub", TrafficBytes: 1 << 30,
		ExpiryTime: time.Now().Add(24 * time.Hour), ResetDays: 30,
	})
	assert.ErrorIs(t, err, ErrClientNotFound)
}

func TestPanelContract_Del_NotFoundInAnyInbound(t *testing.T) {
	t.Parallel()

	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/panel/api/clients/del/ghost@example.com", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"msg":"Something went wrong (client \"ghost@example.com\" not found in any inbound or client record\n)","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.DeleteClient(context.Background(), "ghost@example.com")
	assert.ErrorIs(t, err, ErrClientNotFound)
}

func TestPanelContract_ResetTraffic_RecordNotFound(t *testing.T) {
	t.Parallel()

	server := newPanelFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"msg":"Something went wrong (record not found)","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.ResetTraffic(context.Background(), "ghost@example.com")
	assert.ErrorIs(t, err, ErrClientNotFound)
}

func TestPanelContract_Get_ObtainRecordNotFound(t *testing.T) {
	t.Parallel()

	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/panel/api/clients/get/ghost@example.com", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"msg":"Obtain (record not found)","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	record, getErr := client.GetClientByEmail(context.Background(), "ghost@example.com")
	require.Error(t, getErr)
	assert.Nil(t, record)
	assert.ErrorIs(t, getErr, ErrClientNotFound)
}

func TestPanelContract_Get_NullObjIsClientNotFound(t *testing.T) {
	t.Parallel()

	// Панель отдаёт success=true с obj=null, когда карточки нет: это
	// ErrClientNotFound (как и у traffic/{email}), иначе sync считает пропажу
	// клиента инфраструктурным сбоем и не уходит в pending_add.
	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/panel/api/clients/get/ghost@example.com", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"msg":"","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	record, getErr := client.GetClientByEmail(context.Background(), "ghost@example.com")
	require.Error(t, getErr)
	assert.Nil(t, record)
	assert.ErrorIs(t, getErr, ErrClientNotFound)
}

func TestPanelContract_Update_ScopedPerFlowGroup(t *testing.T) {
	t.Parallel()

	// Панель v3.11 применяет update ко всем привязанным инбаундам, если не
	// передан ?inboundIds=. Инбаунд 1 (tcp → XTLS Vision) и инбаунд 2 (ws → без
	// flow) попадают в разные flow-группы; каждый вызов обязан нести свой
	// фильтр, иначе вызов группы 2 (flow="") стирает Vision на инбаунде 1.
	type panelCall struct {
		inboundIDs string
		flow       string
	}

	calls := make(chan panelCall, 4)

	mux := http.NewServeMux()
	mux.HandleFunc("/panel/api/inbounds/get/", func(w http.ResponseWriter, r *http.Request) {
		network := "tcp"
		if strings.HasSuffix(r.URL.Path, "/2") {
			network = "ws"
		}

		stream, marshalErr := json.Marshal(map[string]string{"network": network})
		require.NoError(t, marshalErr)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"success":true,"obj":{"id":1,"streamSettings":%q}}`, string(stream))
	})
	mux.HandleFunc("/panel/api/clients/update/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))

		flow, _ := payload["flow"].(string)
		calls <- panelCall{inboundIDs: r.URL.Query().Get("inboundIds"), flow: flow}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
	})
	// Многогрупповой update сверяет фактическое членство клиента: панель
	// должна вернуть оба инбаунда, иначе операция считается неполной.
	mux.HandleFunc("/panel/api/clients/get/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"obj":{"client":{"id":"uuid","email":"user@example.com"},"inboundIds":[1,2]}}`)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.UpdateClient(context.Background(), ClientRequest{
		InboundIDs:   []int{1, 2},
		CurrentEmail: "user@example.com",
		Email:        "user@example.com",
		ClientID:     "uuid",
		SubID:        "sub",
		TrafficBytes: 1 << 30,
		ExpiryTime:   time.Now().Add(24 * time.Hour),
		ResetDays:    30,
	})
	require.NoError(t, err)

	byInbound := map[string]string{}

	for range 2 {
		call := <-calls
		byInbound[call.inboundIDs] = call.flow
	}

	assert.Equal(t, map[string]string{
		"1": "xtls-rprx-vision",
		"2": "",
	}, byInbound, "each update call must be scoped to its own flow group")
}

func TestPanelContract_Add_AlreadyInUse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  string
	}{
		{"email in use", `{"success":false,"msg":"email already in use: dup@example.com","obj":null}`},
		{"subId in use", `{"success":false,"msg":"subId already in use: fb9de073cb\n","obj":null}`},
		{"duplicate", `{"success":false,"msg":"duplicate entry","obj":null}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := newPanelFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.msg)
			})

			client, err := NewClient(server.URL, testAPIToken)
			require.NoError(t, err)
			defer client.Close()

			_, addErr := client.AddClientWithID(context.Background(), ClientRequest{
				InboundIDs: []int{1}, Email: "dup@example.com", ClientID: "uuid", SubID: "sub",
				TrafficBytes: 1 << 30, ExpiryTime: time.Now().Add(24 * time.Hour), ResetDays: 30,
			})
			assert.ErrorIs(t, addErr, ErrClientAlreadyExists)
			assert.NotErrorIs(t, addErr, ErrClientNotFound)
		})
	}
}

func TestPanelContract_GetClientByEmail_Card(t *testing.T) {
	t.Parallel()

	// Форма ответа GET /panel/api/clients/get/{email} (контракт, раздел 2.1):
	// клиент лежит ВНУТРИ obj.client, inboundIds — рядом в obj.inboundIds.
	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/panel/api/clients/get/alpha@example.com", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"msg":"","obj":{`+
			`"client":{"id":7,"uuid":"uuid-1","email":"alpha@example.com","enable":true,`+
			`"totalGB":128849018880,"expiryTime":1795899600000,"subId":"sub-1",`+
			`"reset":30,"resetDay":5,"resetMax":3,"resetWeekday":0,`+
			`"trafficReset":"monthly","trafficResetDay":1},`+
			`"externalLinks":[],"inboundIds":[2,4,5],`+
			`"tunnelAllowedIPs":null,"usedTraffic":null}}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	record, getErr := client.GetClientByEmail(context.Background(), "alpha@example.com")
	require.NoError(t, getErr)
	require.NotNil(t, record)

	// Панель отдаёт id числом (внутренний идентификатор записи), идентичность
	// клиента — uuid. Проверяем обе ветки разбора: число в id, строка в uuid.
	assert.Equal(t, "7", record.Client.ID)
	assert.Equal(t, "uuid-1", record.Client.UUID)
	assert.Equal(t, "alpha@example.com", record.Client.Email)
	assert.True(t, record.Client.Enable)

	// Полный профиль авто-renew, который /clients/traffic не отдаёт.
	assert.Equal(t, 30, record.Client.Reset)
	assert.Equal(t, 5, record.Client.ResetDay)
	assert.Equal(t, 3, record.Client.ResetMax)
	assert.Equal(t, "monthly", record.Client.TrafficReset)
	assert.Equal(t, 1, record.Client.TrafficResetDay)

	assert.Equal(t, []int{2, 4, 5}, record.InboundIDs)
}

// Регрессия B7: add обязан применять ту же нормализацию ResetDays, что и
// update (отрицательное значение → config.SubscriptionResetDay), чтобы add и
// update давали идентичный профиль. Дефект в проде не проявлялся — панель
// нормализует значение при записи; правка нужна для единообразия.
func TestPanelContract_Add_NormalizesNegativeResetDays(t *testing.T) {
	t.Parallel()

	var gotReset float64

	server := newPanelFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, rErr := io.ReadAll(r.Body)
		require.NoError(t, rErr)

		var payload struct {
			Client map[string]any `json:"client"`
		}
		require.NoError(t, json.Unmarshal(body, &payload))

		reset, ok := payload.Client["reset"].(float64)
		require.True(t, ok, "reset must be a number in the add payload")
		gotReset = reset

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"msg":"ok","obj":null}`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	_, addErr := client.AddClientWithID(context.Background(), ClientRequest{
		InboundIDs: []int{1}, Email: "norm@example.com", ClientID: "uuid", SubID: "sub",
		TrafficBytes: 1 << 30, ExpiryTime: time.Now().Add(24 * time.Hour), ResetDays: -1,
	})
	require.NoError(t, addErr)
	assert.Equal(t, float64(config.SubscriptionResetDay), gotReset,
		"add with ResetDays=-1 must send reset=config.SubscriptionResetDay, not -1")
}

// Распознавание текстов регистронезависимо; не распознанные контрактом
// тексты не превращаются в sentinel'ы.
func TestPanelContract_ClassifyFailure_CaseInsensitiveAndStrict(t *testing.T) {
	t.Parallel()

	assert.ErrorIs(t, classifyFailure(opAdd, "EMAIL ALREADY IN USE: x@y.z"), ErrClientAlreadyExists)
	assert.ErrorIs(t, classifyFailure(opDel, `Client "x@y.z" NOT FOUND in any inbound or client record`), ErrClientNotFound)
	assert.ErrorIs(t, classifyFailure(opUpdate, "RECORD NOT FOUND"), ErrClientNotFound)

	// Чужие тексты — не sentinel'ы.
	assert.Nil(t, classifyFailure(opAdd, "inbound not found"))
	assert.Nil(t, classifyFailure(opDel, "something else"))
	// "not found" в другом контексте (traffic-ветка ищет только
	// "client not found") — не sentinel.
	assert.Nil(t, classifyFailure(opTraffic, "record not found"))
	assert.Nil(t, classifyFailure(opGet, "boom"))
}

// Панель отвечает не-200 только при инфраструктурных проблемах; по решению
// о ретраях это не ретраится (см. utils.IsRetryable) — фикстура-тест ниже
// закрепляет: не-200 не считается успехом и не классифицируется в sentinel.
func TestPanelContract_Non200IsNotRetriedAndNotClassified(t *testing.T) {
	t.Parallel()

	var calls int

	server := newPanelFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `gateway error`)
	})

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)
	defer client.Close()

	err = client.DeleteClient(context.Background(), "ghost@example.com")
	require.Error(t, err)
	assert.ErrorIs(t, err, utils.ErrNon200Response)
	assert.NotErrorIs(t, err, ErrClientNotFound)
	assert.Equal(t, 1, calls, "non-200 panel response must not be retried — current retry decision")
}

// doGetInbound обязан разбирать конверт так же, как остальные эндпоинты:
// success=true с obj=null не должен превращаться в ошибку json.Unmarshal,
// а панельный "record not found" — в sentinel (classify.go).
func TestGetInbound_EmptyObjAndClassification(t *testing.T) {
	t.Parallel()

	t.Run("success with null obj", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":true,"msg":"","obj":null}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)

		defer client.Close()

		_, err = client.GetInbound(context.Background(), 1)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "unexpected end of JSON input")
		assert.Contains(t, err.Error(), "empty obj")
	})

	t.Run("panel record not found is an inbound error, not a client one", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"success":false,"msg":"Something went wrong (record not found)"}`)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, testAPIToken)
		require.NoError(t, err)

		defer client.Close()

		_, err = client.GetInbound(context.Background(), 1)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInboundNotFound,
			"missing inbound must not be reported as a missing client: that path falls back to create")
		assert.NotErrorIs(t, err, ErrClientNotFound)
	})
}

// Тест поведения: если панель применила только часть flow-групп, клиент живёт
// на части инбаундов — операция должна вернуть ретраебельный
// ErrIncompleteFanout, а не отказ панели (который паркует узел).
func TestAddClientWithID_PartialFlowGroupFailureIsIncompleteFanout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/inbounds/get/") {
			// inbound 1 — ws (flow ""), inbound 2 — tcp (vision). Группы
			// применяются в сортированном порядке: "" раньше vision.
			network := "ws"
			if strings.HasSuffix(r.URL.Path, "/2") {
				network = "tcp"
			}

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"success":true,"obj":{"id":1,"streamSettings":"{\"network\":\"%s\"}"}}`, network)

			return
		}

		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"xtls-rprx-vision"`) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"success":false,"msg":"inbound not found"}`)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)

	defer client.Close()

	_, err = client.AddClientWithID(context.Background(), ClientRequest{
		InboundIDs: []int{1, 2}, Email: "partial@example.com", ClientID: "uuid", SubID: "sub",
		TrafficBytes: 100, ExpiryTime: time.Now(), ResetDays: 0,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIncompleteFanout)
}

// Тест поведения: панель отвечает success по всем группам, но клиент привязан
// не ко всем инбаундам — это тоже неполный fan-out, а не успех.
func TestAddClientWithID_MissingInboundMembershipIsIncompleteFanout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/inbounds/get/") {
			network := "ws"
			if strings.HasSuffix(r.URL.Path, "/2") {
				network = "tcp"
			}

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"success":true,"obj":{"id":1,"streamSettings":"{\"network\":\"%s\"}"}}`, network)

			return
		}

		if strings.Contains(r.URL.Path, "/clients/get/") {
			w.Header().Set("Content-Type", "application/json")
			// Панель отчиталась об успехе, но клиент есть только на inbound 1.
			fmt.Fprint(w, `{"success":true,"obj":{"client":{"id":"uuid","email":"party@example.com"},"inboundIds":[1]}}`)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)

	defer client.Close()

	_, err = client.AddClientWithID(context.Background(), ClientRequest{
		InboundIDs: []int{1, 2}, Email: "party@example.com", ClientID: "uuid", SubID: "sub",
		TrafficBytes: 100, ExpiryTime: time.Now(), ResetDays: 0,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIncompleteFanout)
}

// Тест поведения: полное членство в инбаундах — успех без ложных ошибок.
func TestAddClientWithID_MultiInboundSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/inbounds/get/") {
			network := "ws"
			if strings.HasSuffix(r.URL.Path, "/2") {
				network = "tcp"
			}

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"success":true,"obj":{"id":1,"streamSettings":"{\"network\":\"%s\"}"}}`, network)

			return
		}

		if strings.Contains(r.URL.Path, "/clients/get/") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"success":true,"obj":{"client":{"id":"uuid","email":"full@example.com"},"inboundIds":[1,2]}}`)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"msg":"ok"}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)

	defer client.Close()

	result, err := client.AddClientWithID(context.Background(), ClientRequest{
		InboundIDs: []int{1, 2}, Email: "full@example.com", ClientID: "uuid", SubID: "sub",
		TrafficBytes: 100, ExpiryTime: time.Now(), ResetDays: 0,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
}

// Тест поведения: неразбираемый streamSettings не превращается в vision —
// иначе клиент уезжает на панель с flow, которого inbound не поддерживает.
func TestGetRequiredFlow_UnparsableStreamSettingsIsError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"obj":{"id":1,"streamSettings":"{not-json"}}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testAPIToken)
	require.NoError(t, err)

	defer client.Close()

	flow, err := client.getRequiredFlow(context.Background(), 1)
	require.Error(t, err)
	assert.Empty(t, flow)
}
