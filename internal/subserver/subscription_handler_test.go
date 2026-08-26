package subserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSubSvc creates a subserver.Service with a short TTL for tests.
func newTestSubSvc(t *testing.T) *Service {
	t.Helper()

	svc := NewService(5 * time.Minute)

	t.Cleanup(func() { svc.Stop() })

	return svc
}

// ==================== HandleSubscription Tests ====================

func TestHandleSubscription_CacheHit_Active(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	// Pre-populate cache
	cachedBody := []byte("cached-content")
	cachedHeaders := map[string]string{"content-type": "text/plain"}
	svc.SetCache("sub123", cachedBody, cachedHeaders)

	// Mock: subscription is active
	mockDB.GetSubscriptionStatusFunc = func(ctx context.Context, subID string) (string, time.Time, error) {
		return "active", time.Now().Add(24 * time.Hour), nil
	}

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub123", "1.2.3.4", nil)
	require.NoError(t, err)
	assert.Equal(t, cachedBody, result.Body)
	assert.Equal(t, cachedHeaders, result.Headers)
}

func TestHandleSubscription_CacheHit_Revoked_InvalidatesCache(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	// Pre-populate cache
	svc.SetCache("sub-revoked", []byte("old-data"), map[string]string{"content-type": "text/plain"})

	// Mock: subscription is revoked
	mockDB.GetSubscriptionStatusFunc = func(ctx context.Context, subID string) (string, time.Time, error) {
		return "revoked", time.Time{}, nil
	}

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-revoked", "1.2.3.4", nil)
	_ = result
	// A revoked subscription must read as not found (404), not as a server error.
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)

	// Cache should be invalidated
	_, _, ok := svc.GetCache("sub-revoked")
	assert.False(t, ok, "cache should be invalidated for revoked subscription")
}

func TestHandleSubscription_CacheHit_Expired_InvalidatesCache(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	svc.SetCache("sub-expired", []byte("old-data"), map[string]string{"content-type": "text/plain"})

	// Subscription is "active" but expiry time is in the past
	pastTime := time.Now().Add(-1 * time.Hour)
	mockDB.GetSubscriptionStatusFunc = func(ctx context.Context, subID string) (string, time.Time, error) {
		return "active", pastTime, nil
	}

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-expired", "1.2.3.4", nil)
	_ = result
	// An expired subscription must read as not found (404), not as a server error.
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)

	_, _, ok := svc.GetCache("sub-expired")
	assert.False(t, ok, "cache should be invalidated for expired subscription")
}

func TestHandleSubscription_CacheHit_StatusCheckError_ReturnsError(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	cachedBody := []byte("stale-content")
	svc.SetCache("sub-err", cachedBody, map[string]string{"content-type": "text/plain"})

	// A DB error during revalidation must fail closed instead of serving stale
	// access that may already have been revoked.
	mockDB.GetSubscriptionStatusFunc = func(ctx context.Context, subID string) (string, time.Time, error) {
		return "", time.Time{}, fmt.Errorf("db error")
	}

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-err", "1.2.3.4", nil)
	require.Error(t, err)
	assert.Nil(t, result)

	// Keep the entry so a later successful revalidation can still use it.
	_, _, ok := svc.GetCache("sub-err")
	assert.True(t, ok, "cache entry should remain available after a transient DB error")
}

func TestHandleSubscription_CacheMiss_SubscriptionNotFound(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return nil, fmt.Errorf("not found: %w", database.ErrSubscriptionNotFound)
	}

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "nonexistent", "1.2.3.4", nil)
	_ = result

	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestHandleSubscription_Base64Response(t *testing.T) {
	t.Parallel()

	// Set up a fake 3x-ui server that returns base64-encoded share links
	vlessLink := "vless://uuid@server:443?encryption=none&security=tls&type=tcp&sni=example.com#Test"
	encodedBody := base64.StdEncoding.EncodeToString([]byte(vlessLink))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824; expire=1735689600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(encodedBody))
	}))
	defer ts.Close()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	subURL := ts.URL + "/"
	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, SubscriptionID: "sub-b64", Status: "active"},
			Plan:         database.Plan{ID: 1, Name: "test", TrafficLimit: 1073741824},
			Nodes: []database.Node{
				{ID: 1, Name: "test-node", IsActive: true, SubscriptionURL: subURL},
			},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-b64", "1.2.3.4", nil)
	require.NoError(t, err)
	assert.NotNil(t, result.Body)
	assert.Contains(t, result.Headers["content-type"], "base64")
	assert.Contains(t, result.Headers["subscription-userinfo"], "upload=0")
}

func TestHandleSubscription_PlainResponse(t *testing.T) {
	t.Parallel()

	vlessLink := "vless://uuid@server:443?encryption=none&security=tls&type=tcp&sni=example.com#Plain"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(vlessLink))
	}))
	defer ts.Close()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 2, SubscriptionID: "sub-plain", Status: "active"},
			Plan:         database.Plan{ID: 1, Name: "test", TrafficLimit: 0},
			Nodes: []database.Node{
				{ID: 1, Name: "node", IsActive: true, SubscriptionURL: ts.URL + "/"},
			},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-plain", "1.2.3.4", nil)
	require.NoError(t, err)
	assert.NotNil(t, result.Body)
	// Should be base64-encoded
	decoded, decErr := base64.StdEncoding.DecodeString(string(result.Body))
	require.NoError(t, decErr)
	assert.Contains(t, string(decoded), "vless://")
}

func TestHandleSubscription_JSONResponse_PureJSON(t *testing.T) {
	t.Parallel()

	jsonConfig := map[string]any{
		"type":       "vless",
		"address":    "json-server.example.com",
		"port":       443,
		"uuid":       "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"encryption": "none",
		"security":   "reality",
		"sni":        "reality.example.com",
		"remark":     "JSON-Node",
	}
	jsonBody, err := json.Marshal(jsonConfig)
	require.NoError(t, err)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(jsonBody)
	}))
	defer ts.Close()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 3, SubscriptionID: "sub-json", Status: "active"},
			Plan:         database.Plan{ID: 1, Name: "test", TrafficLimit: 0},
			Nodes: []database.Node{
				{ID: 1, Name: "json-node", IsActive: true, SubscriptionURL: ts.URL + "/"},
			},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-json", "1.2.3.4", nil)
	require.NoError(t, err)
	assert.NotNil(t, result.Body)
	assert.Contains(t, result.Headers["content-type"], "application/json")
}

func TestHandleSubscription_NoNodesWithSubscriptionURL(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	// Node has empty SubscriptionURL — should be skipped
	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 4, SubscriptionID: "sub-no-url", Status: "active"},
			Plan:         database.Plan{ID: 1, Name: "test", TrafficLimit: 0},
			Nodes: []database.Node{
				{ID: 1, Name: "no-url-node", IsActive: true, SubscriptionURL: ""},
			},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-no-url", "1.2.3.4", nil)
	_ = result

	assert.ErrorIs(t, err, ErrNoSubscriptionItems)
}

func TestHandleSubscription_FetchError_SkipsNode(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	// Node points to an invalid URL — FetchFromNode will fail
	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 5, SubscriptionID: "sub-fetch-err", Status: "active"},
			Plan:         database.Plan{ID: 1, Name: "test", TrafficLimit: 0},
			Nodes: []database.Node{
				{ID: 1, Name: "bad-node", IsActive: true, SubscriptionURL: "http://127.0.0.1:1/"},
			},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-fetch-err", "1.2.3.4", nil)
	_ = result

	assert.ErrorIs(t, err, ErrNoSubscriptionItems)
}

func TestHandleSubscription_MultipleNodes_AggregatesResponses(t *testing.T) {
	t.Parallel()

	// Two upstream servers returning different share links
	link1 := "vless://uuid1@server1:443?encryption=none#Node1"
	link2 := "vless://uuid2@server2:443?encryption=none#Node2"

	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(link1))
	}))
	defer ts1.Close()

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(link2))
	}))
	defer ts2.Close()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 6, SubscriptionID: "sub-multi", Status: "active"},
			Plan:         database.Plan{ID: 1, Name: "test", TrafficLimit: 0},
			Nodes: []database.Node{
				{ID: 1, Name: "node1", IsActive: true, SubscriptionURL: ts1.URL + "/"},
				{ID: 2, Name: "node2", IsActive: true, SubscriptionURL: ts2.URL + "/"},
			},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-multi", "1.2.3.4", nil)
	require.NoError(t, err)
	assert.NotNil(t, result.Body)

	// Decode base64 body and check both links are present
	decoded, decErr := base64.StdEncoding.DecodeString(string(result.Body))
	require.NoError(t, decErr)

	bodyStr := string(decoded)
	assert.Contains(t, bodyStr, "vless://uuid1@")
	assert.Contains(t, bodyStr, "vless://uuid2@")
}

// clashBody is a minimal Clash/Mihomo config used by the priority tests.
const clashBody = `proxies:
  - name: "DE_3"
    type: vless
    server: 46.101.238.160
    port: 443
    uuid: 0970324b-8c61-4ae7-8c3f-385a6f1e17e4
    network: ws
    ws-opts:
      path: "/"
      headers:
        Host: vpn47.cc.cd
    tls: true
    servername: vpn47.cc.cd
    client-fingerprint: chrome
`

func newClashServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte(clashBody))
	}))
}

// Clash-only must be returned as base64-encoded share links, not a JSON array.
func TestHandleSubscription_ClashOnly_ReturnsBase64Links(t *testing.T) {
	t.Parallel()

	ts := newClashServer()
	defer ts.Close()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 7, SubscriptionID: "sub-clash", Status: "active"},
			Plan:         database.Plan{ID: 1, Name: "test", TrafficLimit: 0},
			Nodes: []database.Node{
				{ID: 1, Name: "node1", IsActive: true, SubscriptionURL: ts.URL + "/"},
			},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-clash", "1.2.3.4", nil)
	require.NoError(t, err)
	require.NotNil(t, result.Body)

	decoded, decErr := base64.StdEncoding.DecodeString(string(result.Body))
	require.NoError(t, decErr)
	assert.NotEmpty(t, decoded)
	// Must NOT be a JSON array of serverConfig objects.
	assert.False(t, strings.HasPrefix(strings.TrimSpace(string(decoded)), "["),
		"clash-only must not return a raw serverConfig array")
	assert.Contains(t, string(decoded), "vless://0970324b-8c61-4ae7-8c3f-385a6f1e17e4@46.101.238.160:443")
}

// Any Clash node mixed with another format forces the base64/link output.
func TestHandleSubscription_JSONAndClash_ReturnsBase64(t *testing.T) {
	t.Parallel()

	tsClash := newClashServer()
	defer tsClash.Close()

	jsonBody := `[{"type":"vless","address":"9.9.9.9","port":443,"uuid":"aaaaaaaa-8c61-4ae7-8c3f-385a6f1e17e4","remark":"J"}]`

	tsJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(jsonBody))
	}))
	defer tsJSON.Close()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 8, SubscriptionID: "sub-mix", Status: "active"},
			Plan:         database.Plan{ID: 1, Name: "test", TrafficLimit: 0},
			Nodes: []database.Node{
				{ID: 1, Name: "json", IsActive: true, SubscriptionURL: tsJSON.URL + "/"},
				{ID: 2, Name: "clash", IsActive: true, SubscriptionURL: tsClash.URL + "/"},
			},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-mix", "1.2.3.4", nil)
	require.NoError(t, err)
	require.NotNil(t, result.Body)

	decoded, decErr := base64.StdEncoding.DecodeString(string(result.Body))
	require.NoError(t, decErr)

	bodyStr := string(decoded)
	assert.Contains(t, bodyStr, "vless://0970324b-8c61-4ae7-8c3f-385a6f1e17e4@46.101.238.160:443")
	assert.Contains(t, bodyStr, "vless://aaaaaaaa-8c61-4ae7-8c3f-385a6f1e17e4@9.9.9.9:443")
}

// Base64 + Clash also forces the base64/link output.
func TestHandleSubscription_Base64AndClash_ReturnsBase64(t *testing.T) {
	t.Parallel()

	tsClash := newClashServer()
	defer tsClash.Close()

	link := "trojan://pass@8.8.8.8:443?security=tls#T"

	tsB64 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(link))))
	}))
	defer tsB64.Close()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 9, SubscriptionID: "sub-b64c", Status: "active"},
			Plan:         database.Plan{ID: 1, Name: "test", TrafficLimit: 0},
			Nodes: []database.Node{
				{ID: 1, Name: "b64", IsActive: true, SubscriptionURL: tsB64.URL + "/"},
				{ID: 2, Name: "clash", IsActive: true, SubscriptionURL: tsClash.URL + "/"},
			},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-b64c", "1.2.3.4", nil)
	require.NoError(t, err)
	require.NotNil(t, result.Body)

	decoded, decErr := base64.StdEncoding.DecodeString(string(result.Body))
	require.NoError(t, decErr)

	bodyStr := string(decoded)
	assert.Contains(t, bodyStr, "trojan://pass@8.8.8.8:443")
	assert.Contains(t, bodyStr, "vless://0970324b-8c61-4ae7-8c3f-385a6f1e17e4@46.101.238.160:443")
}

func TestHandleSubscription_FetchNode_UsesURLDirectly(t *testing.T) {
	t.Parallel()

	// Fetch node: the upstream URL is used as-is, without appending subID.
	// The server verifies it receives the exact URL path (no /sub-xxx suffix).
	vlessLink := "vless://uuid@fetch-server:443?encryption=none#FetchNode"

	var requestedPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(vlessLink))
	}))
	defer ts.Close()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 7, SubscriptionID: "sub-fetch", Status: "active"},
			Plan:         database.Plan{ID: 1, Name: "test", TrafficLimit: 0},
			Nodes: []database.Node{
				{ID: 1, Name: "fetch-node", IsActive: true, Type: database.NodeTypeFetch, SubscriptionURL: ts.URL + "/raw-proxy"},
			},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-fetch", "1.2.3.4", nil)
	require.NoError(t, err)
	assert.NotNil(t, result.Body)

	// Verify the server received the exact URL path, not path + subID
	assert.Equal(t, "/raw-proxy", requestedPath)

	// Verify the proxy data was returned
	decoded, decErr := base64.StdEncoding.DecodeString(string(result.Body))
	require.NoError(t, decErr)
	assert.Contains(t, string(decoded), "vless://uuid@fetch-server")
}

// TestHandleSubscription_ParallelCacheMiss_PreservesDevicesAndIPs locks the
// analytics read-modify-write contract: concurrent cache-miss requests must not
// overwrite each other's device/IP entries. Each request re-reads the freshest
// subscription row inside the analytics critical section before appending its
// own entry, so every device and every IP must survive.
func TestHandleSubscription_ParallelCacheMiss_PreservesDevicesAndIPs(t *testing.T) {
	t.Parallel()

	const requests = 8

	var (
		stateMu sync.Mutex
		devices = "[]"
		ips     = "[]"
	)

	mockDB := testutil.NewDatabaseService()
	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		stateMu.Lock()
		curDevices, curIPs := devices, ips
		stateMu.Unlock()

		// Widen the read-modify-write window so the buggy version (load outside
		// the lock) reliably reads the same stale snapshot for all requests and
		// loses concurrent updates.
		time.Sleep(10 * time.Millisecond)

		return &database.SubscriptionFull{
			Subscription: database.Subscription{
				ID:             1,
				SubscriptionID: subID,
				Status:         "active",
				Devices:        curDevices,
				Ips:            curIPs,
			},
			Plan: database.Plan{ID: 1, Name: "test", TrafficLimit: 0},
			// No node with a subscription_url: fetching yields no items, the
			// cache is never populated, so every request stays on the
			// cache-miss path and runs the analytics update.
			Nodes: []database.Node{{ID: 1, Name: "no-url", IsActive: true, SubscriptionURL: ""}},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error {
		stateMu.Lock()
		defer stateMu.Unlock()

		devices = devicesJSON

		return nil
	}
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error {
		stateMu.Lock()
		defer stateMu.Unlock()

		ips = ipsJSON

		return nil
	}

	svc := newTestSubSvc(t)
	ctx := context.Background()

	start := make(chan struct{})
	errs := make([]error, requests)

	var wg sync.WaitGroup

	for i := range requests {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			<-start

			headers := map[string]string{"x-hwid": fmt.Sprintf("device-%d", i)}
			_, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-parallel", fmt.Sprintf("10.0.0.%d", i), headers)
			errs[i] = err
		}(i)
	}

	close(start)
	wg.Wait()

	// Every request runs the analytics path; the fetch yields no items, which
	// is expected and irrelevant for this test.
	for i, err := range errs {
		require.ErrorIs(t, err, ErrNoSubscriptionItems, "request %d", i)
	}

	stateMu.Lock()
	finalDevices, finalIPs := devices, ips
	stateMu.Unlock()

	// All concurrent requests' devices must be preserved.
	var parsedDevices []map[string]string
	require.NoError(t, json.Unmarshal([]byte(finalDevices), &parsedDevices))
	require.Len(t, parsedDevices, requests)

	for i := range requests {
		assert.Contains(t, finalDevices, fmt.Sprintf("device-%d", i))
	}

	// All concurrent requests' IPs must be preserved.
	var parsedIPs []map[string]string
	require.NoError(t, json.Unmarshal([]byte(finalIPs), &parsedIPs))
	require.Len(t, parsedIPs, requests)

	for i := range requests {
		assert.Contains(t, finalIPs, fmt.Sprintf("10.0.0.%d", i))
	}
}

// ==================== UpdateDevices Tests ====================

func TestUpdateDevices_NewDevice(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()

	var capturedDevices string

	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error {
		capturedDevices = devicesJSON
		return nil
	}

	subFull := &database.SubscriptionFull{
		Subscription: database.Subscription{
			ID:             1,
			SubscriptionID: "sub-dev-1",
			Devices:        "[]",
		},
	}

	headers := map[string]string{"x-hwid": "device-abc"}
	UpdateDevices(context.Background(), mockDB, subFull, headers)

	assert.NotEmpty(t, capturedDevices)
	assert.Contains(t, capturedDevices, "device-abc")
	assert.Contains(t, capturedDevices, "timestamp")
}

func TestUpdateDevices_NilHeaders_SkipsDevice(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()

	var capturedDevices string

	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error {
		capturedDevices = devicesJSON
		return nil
	}

	subFull := &database.SubscriptionFull{
		Subscription: database.Subscription{
			ID:             2,
			SubscriptionID: "sub-dev-nil",
			Devices:        "[]",
		},
	}

	UpdateDevices(context.Background(), mockDB, subFull, nil)
	// Devices should still be saved (empty array), since SetDevices is called
	assert.Equal(t, "[]", capturedDevices)
}

func TestUpdateDevices_ReplacesExistingDevice(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()

	var capturedDevices string

	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error {
		capturedDevices = devicesJSON
		return nil
	}

	// Pre-existing device with same hwid
	existingDevices := `[{"x-hwid":"device-xyz","user-agent":"old-agent","timestamp":"2025-01-01T00:00:00Z"}]`
	subFull := &database.SubscriptionFull{
		Subscription: database.Subscription{
			ID:             3,
			SubscriptionID: "sub-dev-replace",
			Devices:        existingDevices,
		},
	}

	headers := map[string]string{"x-hwid": "device-xyz", "user-agent": "new-agent"}
	UpdateDevices(context.Background(), mockDB, subFull, headers)

	assert.Contains(t, capturedDevices, "device-xyz")
	assert.Contains(t, capturedDevices, "new-agent")
	assert.NotContains(t, capturedDevices, "old-agent")
}

// ==================== UpdateIPs Tests ====================

func TestUpdateIPs_NewIP(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	subFull := &database.SubscriptionFull{
		Subscription: database.Subscription{
			ID:             10,
			SubscriptionID: "sub-ip-1",
			Ips:            "[]",
		},
	}

	UpdateIPs(context.Background(), mockDB, subFull, "10.0.0.1")

	// Verify via the side effect on subFull.Subscription.Ips
	assert.Contains(t, subFull.Subscription.Ips, "10.0.0.1")
}

func TestUpdateIPs_EmptyIP_SkipsEntry(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()

	subFull := &database.SubscriptionFull{
		Subscription: database.Subscription{
			ID:             11,
			SubscriptionID: "sub-ip-empty",
			Ips:            "[]",
		},
	}

	UpdateIPs(context.Background(), mockDB, subFull, "")

	// Empty IP should not add any entry; Ips stays as empty array
	assert.Equal(t, "[]", subFull.Subscription.Ips)
}

func TestUpdateIPs_ReplacesExistingIP(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()

	existingIPs := `[{"10.0.0.1":"2025-01-01T00:00:00Z"}]`
	subFull := &database.SubscriptionFull{
		Subscription: database.Subscription{
			ID:             12,
			SubscriptionID: "sub-ip-replace",
			Ips:            existingIPs,
		},
	}

	UpdateIPs(context.Background(), mockDB, subFull, "10.0.0.1")

	// The IP entry should be replaced (rotated to end) with a new timestamp
	assert.Contains(t, subFull.Subscription.Ips, "10.0.0.1")
	// Count occurrences of "10.0.0.1" — should be exactly 1
	assert.Equal(t, 1, strings.Count(subFull.Subscription.Ips, "10.0.0.1"))
}

// ==================== Helper Tests (subscription_helpers.go) ====================

func TestParseUserInfoValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers map[string]string
		key     string
		want    int64
	}{
		{"upload value", map[string]string{"subscription-userinfo": "upload=100; download=200; total=1000"}, "upload", 100},
		{"download value", map[string]string{"subscription-userinfo": "upload=100; download=200; total=1000"}, "download", 200},
		{"total value", map[string]string{"subscription-userinfo": "upload=0; download=0; total=5368709120"}, "total", 5368709120},
		{"missing key", map[string]string{"subscription-userinfo": "upload=100"}, "download", 0},
		{"nil headers", nil, "upload", 0},
		{"missing userinfo header", map[string]string{"content-type": "text/plain"}, "upload", 0},
		{"invalid number", map[string]string{"subscription-userinfo": "upload=abc"}, "upload", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseUserInfoValue(tt.headers, tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseExpireFromUserInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		userInfo string
		want     string
	}{
		{"with expire", "upload=0; download=0; total=0; expire=1735689600", "1735689600"},
		{"no expire", "upload=0; download=0; total=0", ""},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseExpireFromUserInfo(tt.userInfo)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildUserInfoHeader(t *testing.T) {
	t.Parallel()

	// Without expire
	result := BuildUserInfoHeader(100, 200, 1000, "")
	assert.Contains(t, result, "upload=100")
	assert.Contains(t, result, "download=200")
	assert.Contains(t, result, "total=1000")
	assert.NotContains(t, result, "expire=")

	// With expire
	result = BuildUserInfoHeader(0, 0, 1073741824, "1735689600")
	assert.Contains(t, result, "upload=0")
	assert.Contains(t, result, "download=0")
	assert.Contains(t, result, "total=1073741824")
	assert.Contains(t, result, "expire=1735689600")
}

func TestFilterHeaders(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("X-Hwid", "test-device")
	h.Set("X-Forwarded-For", "1.2.3.4")
	h.Set("X-Real-Ip", "5.6.7.8")
	h.Set("User-Agent", "v2rayng")
	h.Set("Accept", "application/json")
	h.Set("Authorization", "Bearer token")
	h.Set("Cookie", "session=abc")

	filtered := FilterHeaders(h)
	assert.Equal(t, "test-device", filtered["x-hwid"])
	assert.Equal(t, "v2rayng", filtered["user-agent"])
	assert.NotContains(t, filtered, "x-forwarded-for")
	assert.NotContains(t, filtered, "x-real-ip")
	assert.NotContains(t, filtered, "accept")
	assert.NotContains(t, filtered, "authorization")
	assert.NotContains(t, filtered, "cookie")
}

func TestSkipTransportHeader(t *testing.T) {
	t.Parallel()

	assert.True(t, SkipTransportHeader("Content-Length"))
	assert.True(t, SkipTransportHeader("Content-Type"))
	assert.True(t, SkipTransportHeader("Transfer-Encoding"))
	assert.True(t, SkipTransportHeader("subscription-userinfo"))
	assert.False(t, SkipTransportHeader("profile-title"))
	assert.False(t, SkipTransportHeader("routing-mark"))
}

func TestResponseHeaders(t *testing.T) {
	t.Parallel()

	source := map[string]string{
		"profile-title":         "My Profile",
		"subscription-userinfo": "upload=0; download=0",
	}
	result := ResponseHeaders(source, "text/plain", "upload=0; download=0; total=1000")

	// http.Header canonicalizes keys to Title-Case
	assert.Equal(t, "My Profile", result["Profile-Title"])
	assert.Equal(t, "text/plain", result["content-type"])
	assert.Equal(t, "upload=0; download=0; total=1000", result["subscription-userinfo"])
}

// TestAppendProfileTitleSuffix covers the base64-aware suffix logic used to add
// " Premium" to the upstream profile-title header for premium subscriptions.
func TestAppendProfileTitleSuffix(t *testing.T) {
	t.Parallel()

	encode := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

	tests := []struct {
		name   string
		value  string
		suffix string
		want   string
	}{
		{
			name:   "base64 prefixed",
			value:  "base64:" + encode("My Profile"),
			suffix: " Premium",
			want:   "base64:" + encode("My Profile Premium"),
		},
		{
			name:   "uppercase base64 prefix",
			value:  "BASE64:" + encode("My Profile"),
			suffix: " Premium",
			want:   "base64:" + encode("My Profile Premium"),
		},
		{
			name:   "raw base64 without prefix",
			value:  encode("My Profile"),
			suffix: " Premium",
			want:   "base64:" + encode("My Profile Premium"),
		},
		{
			name:   "plain text",
			value:  "My Profile",
			suffix: " Premium",
			want:   "base64:" + encode("My Profile Premium"),
		},
		{
			name:   "plain text that looks like base64",
			value:  "Test",
			suffix: " Premium",
			want:   "base64:" + encode("Test Premium"),
		},
		{
			name:   "cyrillic title",
			value:  "base64:" + encode("Мой профиль"),
			suffix: " Premium",
			want:   "base64:" + encode("Мой профиль Premium"),
		},
		{
			name:   "title already contains Premium is still suffixed",
			value:  "base64:" + encode("Premium Profile"),
			suffix: " Premium",
			want:   "base64:" + encode("Premium Profile Premium"),
		},
		{
			name:   "non-decodable payload treated as plain text",
			value:  "base64:not-valid-base64!!!",
			suffix: " Premium",
			want:   "base64:" + encode("not-valid-base64!!! Premium"),
		},
		{
			name:   "empty value",
			value:  "",
			suffix: " Premium",
			want:   "",
		},
		{
			name:   "empty suffix",
			value:  "base64:" + encode("My Profile"),
			suffix: "",
			want:   "base64:" + encode("My Profile"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AppendProfileTitleSuffix(tt.value, tt.suffix)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestApplyProfileTitleSuffix(t *testing.T) {
	t.Parallel()

	encode := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

	t.Run("title-case key", func(t *testing.T) {
		headers := map[string]string{"Profile-Title": "My Profile", "content-type": "text/plain"}
		ApplyProfileTitleSuffix(headers, " Premium")

		assert.Equal(t, "base64:"+encode("My Profile Premium"), headers["Profile-Title"])
		assert.Equal(t, "text/plain", headers["content-type"])
	})

	t.Run("lowercase key", func(t *testing.T) {
		headers := map[string]string{"profile-title": encode("My Profile")}
		ApplyProfileTitleSuffix(headers, " Premium")

		assert.Equal(t, "base64:"+encode("My Profile Premium"), headers["profile-title"])
	})

	t.Run("no profile-title header is a no-op", func(t *testing.T) {
		headers := map[string]string{"content-type": "text/plain"}
		result := ApplyProfileTitleSuffix(headers, " Premium")

		assert.Equal(t, map[string]string{"content-type": "text/plain"}, result)
		assert.Equal(t, map[string]string{"content-type": "text/plain"}, headers)
	})

	t.Run("nil headers is a no-op", func(t *testing.T) {
		assert.Nil(t, ApplyProfileTitleSuffix(nil, " Premium"))
	})

	t.Run("empty suffix is a no-op", func(t *testing.T) {
		headers := map[string]string{"Profile-Title": "My Profile"}
		ApplyProfileTitleSuffix(headers, "")

		assert.Equal(t, "My Profile", headers["Profile-Title"])
	})
}

// TestHandleSubscription_PaidSubscription_ProfileTitleSuffix verifies that a paid
// (premium) subscription gets " Premium" appended (base64-aware) to the upstream
// profile-title header, while the header is passed through untouched otherwise.
// "Paid" means a product was purchased or money was paid — admin plan overrides
// without payment do not count (see Subscription.IsPaid).
func TestHandleSubscription_PaidSubscription_ProfileTitleSuffix(t *testing.T) {
	t.Parallel()

	vlessLink := "vless://uuid@server:443#TitleTest"
	encodedBody := base64.StdEncoding.EncodeToString([]byte(vlessLink))
	title := "My Profile"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(title)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(encodedBody))
	}))
	defer ts.Close()

	productID := uint(7)

	tests := []struct {
		name    string
		product *uint
		price   int64
		want    string
	}{
		{"paid via product", &productID, 2300, title + " Premium"},
		{"paid via price only", nil, 100, title + " Premium"},
		{"free subscription untouched", nil, 0, title},
		{"trial subscription untouched", nil, 0, title},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB := testutil.NewDatabaseService()
			svc := newTestSubSvc(t)

			mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
				return &database.SubscriptionFull{
					Subscription: database.Subscription{
						ID:             1,
						SubscriptionID: subID,
						Status:         "active",
						ProductID:      tt.product,
						PricePaidCents: tt.price,
					},
					Plan: database.Plan{ID: 1, Name: "premium", TrafficLimit: 0},
					Nodes: []database.Node{
						{ID: 1, Name: "node", IsActive: true, SubscriptionURL: ts.URL + "/"},
					},
				}, nil
			}
			mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
			mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

			result, _, _, err := HandleSubscription(context.Background(), mockDB, svc, "sub-title-"+tt.name, "1.2.3.4", nil)
			require.NoError(t, err)
			require.NotNil(t, result)

			got := result.Headers["Profile-Title"]
			require.NotEmpty(t, got)
			require.True(t, strings.HasPrefix(got, "base64:"))

			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, "base64:"))
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(decoded))
		})
	}
}

// ==================== Format Detection Tests ====================

func TestDetectFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want Format
	}{
		{"empty body", "", FormatUnknown},
		{"json object", `{"type":"vless"}`, FormatJSON},
		{"json array", `[{"type":"vless"}]`, FormatJSON},
		{"base64 encoded link", base64.StdEncoding.EncodeToString([]byte("vless://uuid@server:443#Test")), FormatBase64},
		{"plain vless link", "vless://uuid@server:443#Test", FormatPlain},
		{"plain trojan link", "trojan://pass@server:443#Test", FormatPlain},
		{"random text", "not-a-valid-protocol://something", FormatUnknown},
		{"clash yaml with proxies", "proxies:\n  - type: vless\n    server: 1.2.3.4\n    port: 443\n    uuid: test-uuid\n", FormatClash},
		{"clash yaml without proxies", "mixed-port: 7890\nmode: rule\n", FormatUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectFormat([]byte(tt.body))
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormat_String(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "json", FormatJSON.String())
	assert.Equal(t, "base64", FormatBase64.String())
	assert.Equal(t, "plain", FormatPlain.String())
	assert.Equal(t, "clash", FormatClash.String())
	assert.Equal(t, "unknown", FormatUnknown.String())
}

// ==================== isValidServer Tests ====================

func TestIsValidServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line string
		want bool
	}{
		{"vless://uuid@host:443", true},
		{"VLESS://uuid@host:443", true},
		{"vmess://encoded", true},
		{"trojan://pass@host:443", true},
		{"ss://method:pass@host:443", true},
		{"hysteria://pass@host:443", true},
		{"hysteria2://pass@host:443", true},
		{"hy2://pass@host:443", true},
		{"tuic://host:443", true},
		{"http://example.com", false},
		{"random text", false},
	}

	for _, tt := range tests {
		t.Run(tt.line[:min(20, len(tt.line))], func(t *testing.T) {
			assert.Equal(t, tt.want, isValidServer(tt.line))
		})
	}
}

// ==================== UpdateLastRequest Tests ====================

func TestHandleSubscription_CacheHit_UpdatesLastRequest(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	svc.SetCache("sub-lr-hit", []byte("cached"), map[string]string{"content-type": "text/plain"})

	mockDB.GetSubscriptionStatusFunc = func(ctx context.Context, subID string) (string, time.Time, error) {
		return "active", time.Now().Add(24 * time.Hour), nil
	}

	var calledSubID string

	mockDB.UpdateLastRequestFunc = func(ctx context.Context, subscriptionID string) error {
		calledSubID = subscriptionID
		return nil
	}

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-lr-hit", "1.2.3.4", nil)
	_ = result

	require.NoError(t, err)
	assert.Equal(t, "sub-lr-hit", calledSubID, "UpdateLastRequest must be called on cache hit")
}

func TestHandleSubscription_CacheHit_LastRequestError_DoesNotBlockResponse(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	svc.SetCache("sub-lr-err", []byte("cached"), map[string]string{"content-type": "text/plain"})

	mockDB.GetSubscriptionStatusFunc = func(ctx context.Context, subID string) (string, time.Time, error) {
		return "active", time.Now().Add(24 * time.Hour), nil
	}
	mockDB.UpdateLastRequestFunc = func(ctx context.Context, subscriptionID string) error {
		return fmt.Errorf("db error")
	}

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-lr-err", "1.2.3.4", nil)
	require.NoError(t, err, "UpdateLastRequest failure must not block cache hit response")
	assert.NotNil(t, result.Body)
}

func TestHandleSubscription_CacheMiss_UpdatesLastRequest(t *testing.T) {
	t.Parallel()

	vlessLink := "vless://uuid@server:443#Test"
	encodedBody := base64.StdEncoding.EncodeToString([]byte(vlessLink))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=0; download=0; total=1073741824; expire=1735689600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(encodedBody))
	}))
	defer ts.Close()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	subURL := ts.URL + "/"
	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, SubscriptionID: "sub-lr-miss", Status: "active"},
			Plan:         database.Plan{ID: 1, Name: "test", TrafficLimit: 1073741824},
			Nodes: []database.Node{
				{ID: 1, Name: "test-node", IsActive: true, SubscriptionURL: subURL},
			},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error { return nil }
	mockDB.UpdateIPsFunc = func(ctx context.Context, id uint, ipsJSON string) error { return nil }

	var calledSubID string

	mockDB.UpdateLastRequestFunc = func(ctx context.Context, subscriptionID string) error {
		calledSubID = subscriptionID
		return nil
	}

	result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-lr-miss", "1.2.3.4", nil)
	_ = result

	require.NoError(t, err)
	assert.Equal(t, "sub-lr-miss", calledSubID, "UpdateLastRequest must be called on cache miss")
}

// TestUpdateLastRequest_DB проверяет интеграционно, что колонка last_request
// обновляется в реальной SQLite БД.
func TestUpdateLastRequest_DB(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	ctx := context.Background()

	// The test DSN enables SQLite foreign-key enforcement: reference the
	// seeded free plan so the insert is valid.
	plan, planErr := db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, planErr, "Failed to resolve free plan")

	sub := &database.Subscription{
		TelegramID:     999888777,
		Username:       "lr-test",
		ClientID:       "client-lr-1",
		SubscriptionID: "sub-lr-integration",
		Status:         "active",
		PlanID:         plan.ID,
	}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""), "Failed to create subscription")
	require.Nil(t, sub.LastRequest, "LastRequest must be nil before first request")

	before := time.Now().UTC().Add(-1 * time.Second)

	require.NoError(t, db.UpdateLastRequest(ctx, "sub-lr-integration"), "UpdateLastRequest failed")

	loaded, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.LastRequest, "LastRequest must be set after update")
	assert.True(t, loaded.LastRequest.After(before), "LastRequest must be >= invocation time")

	// Повторный вызов обновляет timestamp (не раньше первого).
	first := *loaded.LastRequest

	require.NoError(t, db.UpdateLastRequest(ctx, "sub-lr-integration"))
	loaded2, err := db.GetByID(ctx, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded2.LastRequest)
	assert.False(t, loaded2.LastRequest.Before(first), "LastRequest must not be earlier than the first call")
}

func TestUpdateLastRequest_NotFound(t *testing.T) {
	t.Parallel()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err, "Failed to create test database service")

	err = db.UpdateLastRequest(context.Background(), "nonexistent-sub-id")
	assert.ErrorIs(t, err, database.ErrSubscriptionNotFound)
}
