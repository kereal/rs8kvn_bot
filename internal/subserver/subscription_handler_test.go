package subserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

	result, err := HandleSubscription(ctx, mockDB, svc, "sub123", "1.2.3.4", nil)
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

	_, err := HandleSubscription(ctx, mockDB, svc, "sub-revoked", "1.2.3.4", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not active")

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

	_, err := HandleSubscription(ctx, mockDB, svc, "sub-expired", "1.2.3.4", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not active")

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

	mockDB.GetSubscriptionStatusFunc = func(ctx context.Context, subID string) (string, time.Time, error) {
		return "", time.Time{}, fmt.Errorf("db error")
	}

	_, err := HandleSubscription(ctx, mockDB, svc, "sub-err", "1.2.3.4", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache status check failed")
}

func TestHandleSubscription_CacheMiss_SubscriptionNotFound(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return nil, fmt.Errorf("not found: %w", database.ErrSubscriptionNotFound)
	}

	_, err := HandleSubscription(ctx, mockDB, svc, "nonexistent", "1.2.3.4", nil)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestHandleSubscription_Base64Response(t *testing.T) {
	t.Parallel()

	// Set up a fake 3x-ui server that returns base64-encoded share links
	vlessLink := "vless://uuid@server:443?encryption=none&security=tls&type=tcp&sni=example.com#Test"
	encodedBody := base64.StdEncoding.EncodeToString([]byte(vlessLink))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("subscription-userinfo", "upload=0; download=0; total=1073741824; expire=1735689600")
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

	result, err := HandleSubscription(ctx, mockDB, svc, "sub-b64", "1.2.3.4", nil)
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

	result, err := HandleSubscription(ctx, mockDB, svc, "sub-plain", "1.2.3.4", nil)
	require.NoError(t, err)
	assert.NotNil(t, result.Body)
	// Should be base64-encoded
	decoded, decErr := base64.StdEncoding.DecodeString(string(result.Body))
	require.NoError(t, decErr)
	assert.Contains(t, string(decoded), "vless://")
}

func TestHandleSubscription_JSONResponse_PureJSON(t *testing.T) {
	t.Parallel()

	jsonConfig := map[string]interface{}{
		"type":       "vless",
		"address":    "json-server.example.com",
		"port":       443,
		"uuid":       "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"encryption": "none",
		"security":   "reality",
		"sni":        "reality.example.com",
		"remark":     "JSON-Node",
	}
	jsonBody, _ := json.Marshal(jsonConfig)

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

	result, err := HandleSubscription(ctx, mockDB, svc, "sub-json", "1.2.3.4", nil)
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

	_, err := HandleSubscription(ctx, mockDB, svc, "sub-no-url", "1.2.3.4", nil)
	assert.ErrorIs(t, err, ErrNoSubscriptionItems)
}

func TestHandleSubscription_FetchError_SkipsNode(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()
	svc := newTestSubSvc(t)
	ctx := context.Background()

	// Node points to an invalid URL — FetchFromXUI will fail
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

	_, err := HandleSubscription(ctx, mockDB, svc, "sub-fetch-err", "1.2.3.4", nil)
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

	result, err := HandleSubscription(ctx, mockDB, svc, "sub-multi", "1.2.3.4", nil)
	require.NoError(t, err)
	assert.NotNil(t, result.Body)

	// Decode base64 body and check both links are present
	decoded, decErr := base64.StdEncoding.DecodeString(string(result.Body))
	require.NoError(t, decErr)
	bodyStr := string(decoded)
	assert.Contains(t, bodyStr, "vless://uuid1@")
	assert.Contains(t, bodyStr, "vless://uuid2@")
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

	filtered := FilterHeaders(h)
	assert.Equal(t, "test-device", filtered["x-hwid"])
	assert.Equal(t, "v2rayng", filtered["user-agent"])
	assert.NotContains(t, filtered, "x-forwarded-for")
	assert.NotContains(t, filtered, "x-real-ip")
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
		{"ssr://something", true},
		{"hysteria://pass@host:443", true},
		{"hysteria2://pass@host:443", true},
		{"hy2://pass@host:443", true},
		{"tuic://host:443", true},
		{"wg://host:443", true},
		{"wireguard://host:443", true},
		{"http://example.com", false},
		{"random text", false},
	}

	for _, tt := range tests {
		t.Run(tt.line[:min(20, len(tt.line))], func(t *testing.T) {
			assert.Equal(t, tt.want, isValidServer(tt.line))
		})
	}
}
