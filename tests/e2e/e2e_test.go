package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/webhook"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/require"
)

var chdirMu sync.Mutex

func init() {
	_, _ = logger.Init("", "error")
}

func setupTestDB(t *testing.T) *database.Service {
	t.Helper()

	chdirMu.Lock()
	defer chdirMu.Unlock()

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(origWd); err != nil {
			t.Logf("Warning: failed to chdir back to %s: %v", origWd, err)
		}
	}()

	projectRoot := findProjectRoot()
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("Failed to change to project root %s: %v", projectRoot, err)
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.NewService(dbPath)
	require.NoError(t, err, "Failed to create database service")

	return db
}

func findProjectRoot() string {
	dir, _ := os.Getwd()
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return dir
}

type e2eTestEnv struct {
	t          *testing.T
	db         *database.Service
	xui        *testutil.MockXUIClient
	botAPI     *testutil.MockBotAPI
	handler    *bot.Handler
	cfg        *config.Config
	botConfig  *bot.BotConfig
	chatID     int64
	username   string
	subService *service.SubscriptionService
}

// waitForServerReady polls the server's /healthz endpoint until it responds
// with HTTP 200 or the timeout expires. This is more reliable than a fixed
// time.Sleep because it works correctly even under heavy CI load.
func waitForServerReady(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	url := "http://" + addr + "/healthz"

	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			if err := resp.Body.Close(); err != nil {
				t.Logf("Warning: failed to close health check body: %v", err)
			}
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("server at %s did not become ready within %v", addr, timeout)
}

func setupE2EEnv(t *testing.T) *e2eTestEnv {
	t.Helper()

	db := setupTestDB(t)

	cfg := &config.Config{
		TelegramAdminID:  123456,
		TrafficLimitGB:   100,
		XUIInboundID:     1,
		XUIHost:          "https://panel.example.com",
		XUISubPath:       "/sub",
		SiteURL:          "https://example.com",
		TelegramBotToken: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
	}

	mockXUI := testutil.NewMockXUIClient()
	mockBotAPI := testutil.NewMockBotAPI()

	botCfg := &bot.BotConfig{
		Username:  "testbot",
		ID:        123456789,
		FirstName: "TestBot",
		IsBot:     true,
	}

	subService := service.NewSubscriptionService(db, mockXUI, cfg, &webhook.NoopSender{})
	handler := bot.NewHandler(mockBotAPI, cfg, db, mockXUI, botCfg, subService, "")

	return &e2eTestEnv{
		t:          t,
		db:         db,
		xui:        mockXUI,
		botAPI:     mockBotAPI,
		handler:    handler,
		cfg:        cfg,
		botConfig:  botCfg,
		chatID:     987654321,
		username:   "testuser",
		subService: subService,
	}
}

func resetMockBotAPI(m *testutil.MockBotAPI) {
	m.SetSendCalled(false)
	m.SetRequestCalled(false)
	m.LastSentText = ""
	m.LastChatID = 0
	m.SendCount = 0
	m.SendError = nil
	m.RequestError = nil
}

func newCommandMessage(chatID int64, userID int64, username, text string, cmdLen int) *tgbotapi.Message {
	return &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: chatID},
		From: &tgbotapi.User{
			ID:       userID,
			UserName: username,
		},
		Text: text,
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: cmdLen},
		},
	}
}

type realXUIEnv struct {
	t          *testing.T
	db         *database.Service
	xuiClient  *xui.Client
	server     *httptest.Server
	cfg        *config.Config
	subService *service.SubscriptionService
}

func setupRealXUIEnv(t *testing.T, handlers map[string]http.HandlerFunc) *realXUIEnv {
	t.Helper()
	db := setupTestDB(t)

	defaults := map[string]http.HandlerFunc{
		"/login": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(xui.APIResponse{Success: true, Msg: "Login successful"}); err != nil {
				t.Fatalf("encode %s response: %v", "/login", err)
			}
		},
		"/panel/api/server/status": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(xui.APIResponse{Success: true}); err != nil {
				t.Fatalf("encode %s response: %v", "/panel/api/server/status", err)
			}
		},
		"/panel/api/inbounds/addClient": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(xui.APIResponse{Success: true, Msg: "Client added"}); err != nil {
				t.Fatalf("encode %s response: %v", "/panel/api/inbounds/addClient", err)
			}
		},
	}

	allHandlers := make(map[string]http.HandlerFunc)
	for path, handler := range defaults {
		allHandlers[path] = handler
	}
	for path, handler := range handlers {
		allHandlers[path] = handler
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if h, ok := allHandlers[path]; ok {
			h(w, r)
			return
		}

		if strings.HasPrefix(path, "/panel/api/inbounds/") && strings.Contains(path, "/delClient/") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{Success: true, Msg: "Client deleted"})
			return
		}
		if strings.HasPrefix(path, "/panel/api/inbounds/") && strings.Contains(path, "/updateClient/") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{Success: true, Msg: "Client updated"})
			return
		}
		if strings.HasPrefix(path, "/panel/api/inbounds/getClientTraffics/") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{
				Success: true,
				Obj:     json.RawMessage(`{"id":1,"inboundId":1,"enable":true,"email":"test","up":100,"down":200,"allTime":300,"total":107374182400}`),
			})
			return
		}

		http.NotFound(w, r)
	})

	server := httptest.NewServer(mux)

	cfg := &config.Config{
		TelegramAdminID:         123456,
		TrafficLimitGB:          100,
		XUIInboundID:            1,
		XUIHost:                 server.URL,
		XUISubPath:              "sub",
		SiteURL:                 "https://example.com",
		TelegramBotToken:        "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
		XUISessionMaxAgeMinutes: 15,
	}

	xuiClient, err := xui.NewClient(server.URL, "admin", "password", 15*time.Minute)
	require.NoError(t, err)

	subService := service.NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})

	return &realXUIEnv{
		t:          t,
		db:         db,
		xuiClient:  xuiClient,
		server:     server,
		cfg:        cfg,
		subService: subService,
	}
}

func (e *realXUIEnv) Close() {
	e.server.Close()
	if err := e.db.Close(); err != nil {
		e.t.Logf("Warning: failed to close database: %v", err)
	}
}
