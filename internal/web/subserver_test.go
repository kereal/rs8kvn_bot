package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"gorm.io/gorm"
)

func testServer(t *testing.T, db interfaces.DatabaseService, cfg *config.Config) *Server {
	subSvc := service.NewSubscriptionService(db, nil, nil, nil, cfg)
	srv := NewServer(":0", db, cfg, "testbot", subSvc, subserver.NewService(config.SubServerCacheTTL))
	return srv
}

func TestHandleSubscription_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	db := testutil.NewDatabaseService()
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

	db := testutil.NewDatabaseService()
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

func TestHandleSubscription_AccessLog(t *testing.T) {
	t.Parallel()

	db := testutil.NewDatabaseService()
	db.GetWithPlanAndNodesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
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

	db := testutil.NewDatabaseService()
	db.GetWithPlanAndNodesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Nodes:        []database.Node{},
		}, nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "Subscription not found", w.Body.String())
}

func TestHandleSubscription_SourceVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		userInfo    string
		mockDBExtra func(db *testutil.DatabaseService)
		check       func(t *testing.T, w *httptest.ResponseRecorder, body string)
	}{
		{
			name:     "plain source",
			body:     "vless://abc@x.com:443\nvmess://def@y.com:8443",
			userInfo: "upload=100; download=200; total=1000; expire=1234567890",
			check: func(t *testing.T, w *httptest.ResponseRecorder, body string) {
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
				require.NoError(t, err)
				assert.Contains(t, string(decoded), "vless://abc@x.com:443")
				assert.Contains(t, string(decoded), "vmess://def@y.com:8443")
				userInfo := w.Header().Get("Subscription-UserInfo")
				assert.Contains(t, userInfo, "upload=100")
				assert.Contains(t, userInfo, "download=200")
			},
		},
		{
			name:     "JSON source",
			body:     `[{"type":"vless","address":"x.com","port":443,"uuid":"abc123","encryption":"none","remark":"S1"}]`,
			userInfo: "upload=50; download=75; total=500; expire=9999",
			check: func(t *testing.T, w *httptest.ResponseRecorder, body string) {
				assert.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))
				var items []json.RawMessage
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &items))
				require.Len(t, items, 1)
				var parsed map[string]any
				require.NoError(t, json.Unmarshal(items[0], &parsed))
				assert.Equal(t, "vless", parsed["type"])
				assert.Equal(t, "x.com", parsed["address"])
				userInfo := w.Header().Get("Subscription-UserInfo")
				assert.Contains(t, userInfo, "upload=50")
			},
		},
		{
			name:     "multiple sources",
			body:     "vless://a@x.com:443",
			userInfo: "upload=100; download=200; total=500; expire=111",
			mockDBExtra: func(db *testutil.DatabaseService) {
				db.GetWithPlanAndNodesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
					return &database.SubscriptionFull{
						Subscription: database.Subscription{ID: 1, Status: "active"},
						Plan:         database.Plan{TrafficLimit: 10 << 30},
						Nodes: []database.Node{
							{ID: 1, Name: "s1", IsActive: true, SubscriptionURL: httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								w.Header().Set("Subscription-UserInfo", "upload=100; download=200; total=500; expire=111")
								w.Write([]byte("vless://a@x.com:443"))
							})).URL + "/sub/"},
							{ID: 2, Name: "s2", IsActive: true, SubscriptionURL: httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								w.Header().Set("Subscription-UserInfo", "upload=300; download=400; total=500; expire=222")
								w.Write([]byte("trojan://b@y.com:8443"))
							})).URL + "/sub/"},
						},
					}, nil
				}
			},
			check: func(t *testing.T, w *httptest.ResponseRecorder, body string) {
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
				require.NoError(t, err)
				assert.Contains(t, string(decoded), "vless://a@x.com:443")
				assert.Contains(t, string(decoded), "trojan://b@y.com:8443")
				userInfo := w.Header().Get("Subscription-UserInfo")
				assert.Contains(t, userInfo, "upload=400")
				assert.Contains(t, userInfo, "download=600")
			},
		},
		{
			name: "mixed JSON and plain",
			body: `{"type":"vless","address":"j.com","port":443,"uuid":"abc","encryption":"none","remark":"J"}`,
			check: func(t *testing.T, w *httptest.ResponseRecorder, body string) {
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
				require.NoError(t, err)
				assert.Contains(t, string(decoded), "vless://abc@j.com:443")
			},
		},
		{
			name:     "base64 encoded source",
			body:     base64.StdEncoding.EncodeToString([]byte("vless://b64@x.com:443\nvmess://b64@y.com:443")),
			userInfo: "upload=10; download=20; total=100; expire=555",
			check: func(t *testing.T, w *httptest.ResponseRecorder, body string) {
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
				require.NoError(t, err)
				assert.Contains(t, string(decoded), "vless://b64@x.com:443")
				assert.Contains(t, string(decoded), "vmess://b64@y.com:443")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b1, b2 *httptest.Server
			if tt.name == "mixed JSON and plain" {
				b1 = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte(`{"type":"vless","address":"j.com","port":443,"uuid":"abc","encryption":"none","remark":"J"}`))
				}))
				b2 = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte("vless://p@plain.com:443"))
				}))
				defer b1.Close()
				defer b2.Close()
			}
			if tt.name == "multiple sources" {
				defer func() {
					if b1 != nil {
						b1.Close()
					}
				}()
				defer func() {
					if b2 != nil {
						b2.Close()
					}
				}()
			}

			var sourceURL string
			if tt.name == "mixed JSON and plain" {
				sourceURL = b1.URL + "/sub/"
			} else if tt.name == "multiple sources" {
				// already set in mockDBExtra
			} else {
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Subscription-UserInfo", tt.userInfo)
					w.Write([]byte(tt.body))
				}))
				defer backend.Close()
				sourceURL = backend.URL + "/sub/"
			}

			db := testutil.NewDatabaseService()
			if tt.mockDBExtra != nil {
				tt.mockDBExtra(db)
			} else {
				db.GetWithPlanAndNodesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
					nodes := []database.Node{{ID: 1, Name: tt.name, IsActive: true, SubscriptionURL: sourceURL}}
					if tt.name == "mixed JSON and plain" {
						nodes = []database.Node{
							{ID: 1, Name: "json", IsActive: true, SubscriptionURL: b1.URL + "/sub/"},
							{ID: 2, Name: "plain", IsActive: true, SubscriptionURL: b2.URL + "/sub/"},
						}
					}
					return &database.SubscriptionFull{
						Subscription: database.Subscription{ID: 1, Status: "active"},
						Plan:         database.Plan{TrafficLimit: 1 << 30},
						Nodes:        nodes,
					}, nil
				}
			}
			srv := testServer(t, db, &config.Config{})

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
			srv.handleSubscription(w, r)
			assert.Equal(t, http.StatusOK, w.Code)
			tt.check(t, w, w.Body.String())
		})
	}
}

func TestHandleSubscription_AccessLogVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupReq   func() *http.Request
		wantFields []string
	}{
		{
			name: "with headers",
			setupReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/sub/unknown?debug=1", nil)
				r.RemoteAddr = "203.0.113.10:1234"
				r.Header.Set("X-Hwid", "hw-1")
				r.Header.Set("X-Device-Os", "iOS")
				r.Header.Set("X-Ver-Os", "17.0")
				r.Header.Set("X-Device-Model", "iPhone 15")
				r.Header.Set("User-Agent", "V2Ray/1.0")
				return r
			},
			wantFields: []string{"GET", "/sub/unknown?debug=1", "404", "-", "203.0.113.10", "hw-1", "iOS", "17.0", "iPhone 15", "V2Ray/1.0"},
		},
		{
			name: "missing optional headers",
			setupReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/sub/unknown", nil)
				r.RemoteAddr = "203.0.113.10:1234"
				return r
			},
			wantFields: []string{"GET", "/sub/unknown", "404", "-", "203.0.113.10", "", "", "", "", ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testutil.NewDatabaseService()
			srv := testServer(t, db, &config.Config{})
			logPath := filepath.Join(t.TempDir(), "subserver.log")
			accessLogger, err := subserver.NewAccessLogger(logPath)
			require.NoError(t, err)
			srv.subserverLogger = accessLogger

			r := tt.setupReq()
			w := httptest.NewRecorder()
			srv.handleSubscription(w, r)

			require.NoError(t, accessLogger.Close())
			content, err := os.ReadFile(logPath)
			require.NoError(t, err)

			line := strings.TrimRight(string(content), "\n")
			parts := splitAccessLogLine(line)
			for len(parts) < 11 {
				parts = append(parts, "")
			}
			require.Len(t, parts, 11)
			assert.Regexp(t, regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`), parts[0])
			assert.NotContains(t, line, "\nGET")
			assert.NotContains(t, line, "SUBSERVER_ACCESS")
			assert.NotContains(t, line, `"method"`)
			assert.Equal(t, tt.wantFields, parts[1:])
		})
	}
}

func TestHandleSubscription_AccessLogSuccessTotalOrder(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "subserver.log")
	accessLogger, err := subserver.NewAccessLogger(logPath)
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodGet, "/sub/test", nil)
	r.RemoteAddr = "203.0.113.10:1234"
	// success=3, total=5 must be written as "3/5", not "5/3".
	accessLogger.Log(r, 200, "203.0.113.10", 3, 5)

	require.NoError(t, accessLogger.Close())
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)

	line := strings.TrimRight(string(content), "\n")
	parts := splitAccessLogLine(line)
	require.GreaterOrEqual(t, len(parts), 5)
	// Field layout: timestamp method uri status success/total ...
	assert.Equal(t, "3/5", parts[4])
}

func splitAccessLogLine(line string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false
	for _, r := range line {
		if r == '"' {
			inQuotes = !inQuotes
		} else if r == ' ' && !inQuotes {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteRune(r)
	}
	parts = append(parts, current.String())
	for i, p := range parts {
		if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
			parts[i] = p[1 : len(p)-1]
		}
	}
	return parts
}
func TestHandleSubscription_SourceFetchError(t *testing.T) {
	t.Parallel()

	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer b.Close()

	db := testutil.NewDatabaseService()
	db.GetWithPlanAndNodesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Nodes: []database.Node{
				{ID: 1, Name: "bad", IsActive: true, SubscriptionURL: b.URL + "/sub/"},
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

// TestHandleSubscription_DatabaseError_Returns500GenericBody verifies the audit #5
// fix: a real infrastructure (DB) error must return 500 with a generic body, NOT
// "Subscription not found" (which would conflate 404 and 500 semantics).
func TestHandleSubscription_DatabaseError_Returns500GenericBody(t *testing.T) {
	t.Parallel()

	db := testutil.NewDatabaseService()
	db.GetWithPlanAndNodesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return nil, gorm.ErrInvalidDB // arbitrary non-not-found infra error
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	srv.handleSubscription(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "infra error must be 500, not 404")
	assert.Equal(t, "Internal Server Error", w.Body.String(), "500 body must be generic, not 'Subscription not found'")
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

	db := testutil.NewDatabaseService()
	db.GetWithPlanAndNodesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Nodes: []database.Node{
				{ID: 1, Name: "src", IsActive: true, SubscriptionURL: backend.URL + "/sub/"},
			},
		}, nil
	}
	db.GetSubscriptionStatusFunc = func(ctx context.Context, _ string) (string, time.Time, error) {
		return "active", time.Time{}, nil
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

	assert.Equal(t, 1, callCount, "second request served from per-subID cache")
}

func TestHandleSubscription_DevicesTracking(t *testing.T) {
	t.Parallel()

	plainContent := "vless://dev@x.com:443"

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(plainContent))
	}))
	defer backend.Close()

	var savedDevices string
	db := testutil.NewDatabaseService()
	db.GetWithPlanAndNodesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Nodes: []database.Node{
				{ID: 1, Name: "src", IsActive: true, SubscriptionURL: backend.URL + "/sub/"},
			},
		}, nil
	}
	db.UpdateDevicesFunc = func(ctx context.Context, id uint, devicesJSON string) error {
		savedDevices = devicesJSON
		return nil
	}
	srv := testServer(t, db, &config.Config{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sub/test123", nil)
	r.Header.Set("X-Hwid", "device1")
	r.Header.Set("User-Agent", "v2rayN/1.0")
	srv.handleSubscription(w, r)

	require.NotEmpty(t, savedDevices, "devices should have been saved")
	var devices []map[string]string
	err := json.Unmarshal([]byte(savedDevices), &devices)
	require.NoError(t, err)
	require.Len(t, devices, 1)
	assert.Equal(t, "device1", devices[0]["x-hwid"])
	assert.Equal(t, "v2rayn/1.0", devices[0]["user-agent"])
	_, err = time.Parse(time.RFC3339, devices[0]["timestamp"])
	assert.NoError(t, err, "timestamp should be RFC3339")
}

func TestHandleSubscription_SourceWithoutSubURL(t *testing.T) {
	t.Parallel()

	db := testutil.NewDatabaseService()
	db.GetWithPlanAndNodesFunc = func(ctx context.Context, _ string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, Status: "active"},
			Plan:         database.Plan{TrafficLimit: 1 << 30},
			Nodes: []database.Node{
				{ID: 1, Name: "nosuburl", IsActive: true, SubscriptionURL: ""},
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

func init() {
	if err := testutil.InitLogger(nil); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to initialize logger:", err)
	}
}
