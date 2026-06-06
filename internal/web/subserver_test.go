package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/subserver"
	"rs8kvn_bot/internal/testutil"

	"gorm.io/gorm"
)

func testServer(t *testing.T, db interfaces.DatabaseService, cfg *config.Config) *Server {
	botCfg := &bot.BotConfig{Username: "testbot"}
	subSvc := service.NewSubscriptionService(db, nil, nil, cfg, "", nil)
		srv := NewServer(":0", db, cfg, botCfg, subSvc, subserver.NewService(config.SubServerCacheTTL))
	return srv
}

func TestHandleSubscription_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	db := testutil.NewMockDatabaseService()
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, "GET", w.Header().Get("Allow"))
}

func TestHandleSubscription_SubServerNil(t *testing.T) {
	t.Parallel()

	srv := &Server{}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "Subscription server is not available", w.Body.String())
}

func TestHandleSubscription_InvalidCode(t *testing.T) {
	t.Parallel()

	db := testutil.NewMockDatabaseService()
	srv := testServer(t, db, &config.Config{})

	tests := []string{
		"/sub/",
		"/sub/with/path",
		"/sub/special@char",
	}

	for _, path := range tests {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		srv.handleSubscription(w, r)
		assert.Equal(t, http.StatusNotFound, w.Code, "path: %s", path)
		assert.Equal(t, "Subscription not found", w.Body.String(), "path: %s", path)
	}
}

func TestHandleSubscription_SubscriptionNotFound(t *testing.T) {
	t.Parallel()

	db := testutil.NewMockDatabaseService()
	db.GetSubscriptionWithPlanAndSourcesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return nil, gorm.ErrRecordNotFound
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/unknown", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "Subscription not found", w.Body.String())
}

func TestHandleSubscription_NoServersAvailable(t *testing.T) {
	t.Parallel()

	db := testutil.NewMockDatabaseService()
	db.GetSubscriptionWithPlanAndSourcesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Sources:      []database.Source{},
		}, nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "Subscription not found", w.Body.String())
}

func TestHandleSubscription_PlainSource(t *testing.T) {
	t.Parallel()

	plainContent := "vless://abc@x.com:443\nvmess://def@y.com:8443"

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-UserInfo", "upload=100; download=200; total=1000; expire=1234567890")
		w.Write([]byte(plainContent))
	}))
	defer backend.Close()

	db := testutil.NewMockDatabaseService()
	db.GetSubscriptionWithPlanAndSourcesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Sources: []database.Source{
				{ID: 1, Name: "src1", Active: true, SubURL: backend.URL + "/sub/"},
			},
		}, nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(w.Body.String()))
	require.NoError(t, err)
	body := string(decoded)

	assert.Contains(t, body, "vless://abc@x.com:443")
	assert.Contains(t, body, "vmess://def@y.com:8443")

	userInfo := w.Header().Get("Subscription-UserInfo")
	assert.Contains(t, userInfo, "upload=100")
	assert.Contains(t, userInfo, "download=200")
	assert.Contains(t, userInfo, "total="+fmt.Sprintf("%d", 1<<30))
	assert.Contains(t, userInfo, "expire=1234567890")
}

func TestHandleSubscription_JSONSource(t *testing.T) {
	t.Parallel()

	jsonContent := `[{"type":"vless","address":"x.com","port":443,"uuid":"abc123","encryption":"none","remark":"S1"}]`

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-UserInfo", "upload=50; download=75; total=500; expire=9999")
		w.Write([]byte(jsonContent))
	}))
	defer backend.Close()

	db := testutil.NewMockDatabaseService()
	db.GetSubscriptionWithPlanAndSourcesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 2 << 30},
			Sources: []database.Source{
				{ID: 1, Name: "src1", Active: true, SubURL: backend.URL + "/sub/"},
			},
		}, nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))

	var items []json.RawMessage
	err := json.Unmarshal(w.Body.Bytes(), &items)
	require.NoError(t, err)
	require.Len(t, items, 1)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(items[0], &parsed))
	assert.Equal(t, "vless", parsed["type"])
	assert.Equal(t, "x.com", parsed["address"])

	userInfo := w.Header().Get("Subscription-UserInfo")
	assert.Contains(t, userInfo, "upload=50")
	assert.Contains(t, userInfo, "download=75")
	assert.Contains(t, userInfo, "total="+fmt.Sprintf("%d", 2<<30))
}

func TestHandleSubscription_MultipleSources(t *testing.T) {
	t.Parallel()

	s1Content := "vless://a@x.com:443"
	s2Content := "trojan://b@y.com:8443"

	b1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-UserInfo", "upload=100; download=200; total=500; expire=111")
		w.Write([]byte(s1Content))
	}))
	defer b1.Close()

	b2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-UserInfo", "upload=300; download=400; total=500; expire=222")
		w.Write([]byte(s2Content))
	}))
	defer b2.Close()

	db := testutil.NewMockDatabaseService()
	db.GetSubscriptionWithPlanAndSourcesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 10 << 30},
			Sources: []database.Source{
				{ID: 1, Name: "s1", Active: true, SubURL: b1.URL + "/sub/"},
				{ID: 2, Name: "s2", Active: true, SubURL: b2.URL + "/sub/"},
			},
		}, nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(w.Body.String()))
	require.NoError(t, err)
	body := string(decoded)

	assert.Contains(t, body, "vless://a@x.com:443")
	assert.Contains(t, body, "trojan://b@y.com:8443")

	userInfo := w.Header().Get("Subscription-UserInfo")
	assert.Contains(t, userInfo, "upload=400")
	assert.Contains(t, userInfo, "download=600")
	assert.Contains(t, userInfo, "total="+fmt.Sprintf("%d", 10<<30))
	assert.Contains(t, userInfo, "expire=111")
}

func TestHandleSubscription_MixedJSONAndPlain(t *testing.T) {
	t.Parallel()

	jsonContent := `{"type":"vless","address":"j.com","port":443,"uuid":"abc","encryption":"none","remark":"J"}`
	plainContent := "vless://p@plain.com:443"

	b1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(jsonContent))
	}))
	defer b1.Close()

	b2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(plainContent))
	}))
	defer b2.Close()

	db := testutil.NewMockDatabaseService()
	db.GetSubscriptionWithPlanAndSourcesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Sources: []database.Source{
				{ID: 1, Name: "json", Active: true, SubURL: b1.URL + "/sub/"},
				{ID: 2, Name: "plain", Active: true, SubURL: b2.URL + "/sub/"},
			},
		}, nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(w.Body.String()))
	require.NoError(t, err)
	body := string(decoded)

	assert.Contains(t, body, "vless://abc@j.com:443")
	assert.Contains(t, body, "vless://p@plain.com:443")
}

func TestHandleSubscription_SourceFetchError(t *testing.T) {
	t.Parallel()

	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer b.Close()

	db := testutil.NewMockDatabaseService()
	db.GetSubscriptionWithPlanAndSourcesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Sources: []database.Source{
				{ID: 1, Name: "bad", Active: true, SubURL: b.URL + "/sub/"},
			},
		}, nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "Subscription not found", w.Body.String())
}

func TestHandleSubscription_CacheSubscriptionResult(t *testing.T) {
	t.Parallel()

	plainContent := "vless://cached@x.com:443"

	callCount := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Write([]byte(plainContent))
	}))
	defer backend.Close()

	db := testutil.NewMockDatabaseService()
	db.GetSubscriptionWithPlanAndSourcesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Sources: []database.Source{
				{ID: 1, Name: "src", Active: true, SubURL: backend.URL + "/sub/"},
			},
		}, nil
	}
	srv := testServer(t, db, &config.Config{})

	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w1, r1)
	assert.Equal(t, http.StatusOK, w1.Code)

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w2, r2)
	assert.Equal(t, http.StatusOK, w2.Code)

	assert.Equal(t, 1, callCount, "backend should only be called once (cached per source URL)")
}

func TestHandleSubscription_DevicesTracking(t *testing.T) {
	t.Parallel()

	plainContent := "vless://dev@x.com:443"

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(plainContent))
	}))
	defer backend.Close()

	var savedDevices string
	db := testutil.NewMockDatabaseService()
	db.GetSubscriptionWithPlanAndSourcesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Sources: []database.Source{
				{ID: 1, Name: "src", Active: true, SubURL: backend.URL + "/sub/"},
			},
		}, nil
	}
	db.UpdateSubscriptionDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error {
		savedDevices = devicesJSON
		return nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	r.Header.Set("X-HWID", "device1")
	r.Header.Set("User-Agent", "v2rayN/1.0")
	srv.handleSubscription(w, r)

	require.NotEmpty(t, savedDevices, "devices should have been saved")
	var devices []map[string]string
	err := json.Unmarshal([]byte(savedDevices), &devices)
	require.NoError(t, err)
	require.Len(t, devices, 1)
	assert.Equal(t, "device1", devices[0]["x-hwid"])
	assert.Equal(t, "v2rayn/1.0", devices[0]["user-agent"])
}

func TestHandleSubscription_SourceWithoutSubURL(t *testing.T) {
	t.Parallel()

	db := testutil.NewMockDatabaseService()
	db.GetSubscriptionWithPlanAndSourcesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Sources: []database.Source{
				{ID: 1, Name: "nosuburl", Active: true, SubURL: ""},
			},
		}, nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "Subscription not found", w.Body.String())
}

func TestHandleSubscription_Base64EncodedSource(t *testing.T) {
	t.Parallel()

	encoded := base64.StdEncoding.EncodeToString([]byte("vless://b64@x.com:443\nvmess://b64@y.com:443"))

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-UserInfo", "upload=10; download=20; total=100; expire=555")
		w.Write([]byte(encoded))
	}))
	defer backend.Close()

	db := testutil.NewMockDatabaseService()
	db.GetSubscriptionWithPlanAndSourcesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Sources: []database.Source{
				{ID: 1, Name: "b64src", Active: true, SubURL: backend.URL + "/sub/"},
			},
		}, nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusOK, w.Code)

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(w.Body.String()))
	require.NoError(t, err)
	body := string(decoded)

	assert.Contains(t, body, "vless://b64@x.com:443")
	assert.Contains(t, body, "vmess://b64@y.com:443")
}

func init() {
	if err := testutil.InitLogger(nil); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to initialize logger:", err)
	}
}
