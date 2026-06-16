package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/webhook"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/require"
)

func init() {
	_, _ = logger.Init("", "error")
}

func setupTestDB(t *testing.T) *database.Service {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.NewService(dbPath)
	require.NoError(t, err, "Failed to create database service")

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Logf("Warning: failed to close database: %v", err)
		}
	})

	return db
}

func e2eNodes(host string) []database.Node {
	return []database.Node{{ID: 1, Name: "main", IsActive: true, Host: host, APIToken: "test-api-token", InboundIDs: "[1]"}}
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
		SiteURL:          "https://example.com",
		TelegramBotToken: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
		GlobalSubURL:     "https://example.com/sub/",
	}

	mockXUI := testutil.NewMockXUIClient()
	mockBotAPI := testutil.NewMockBotAPI()

	botCfg := &bot.BotConfig{
		Username:  "testbot",
		ID:        123456789,
		FirstName: "TestBot",
		IsBot:     true,
	}

	ctx := context.Background()
	require.NoError(t, db.SeedDefaultNode(ctx, "main", "https://panel.example.com", "test-api-token", []int{1}, ""), "seed test node")
	xuiClients := map[uint]interfaces.XUIClient{1: mockXUI}
	nodes := e2eNodes("https://panel.example.com")
	subService := service.NewSubscriptionService(db, xuiClients, nodes, cfg, cfg.GlobalSubURL, &webhook.NoopSender{})
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
		"/panel/api/clients/add": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(xui.APIResponse{Success: true, Msg: "Client added"}); err != nil {
				t.Fatalf("encode %s response: %v", "/panel/api/clients/add", err)
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

		if strings.HasPrefix(path, "/panel/api/clients/del/") {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(xui.APIResponse{Success: true, Msg: "Client deleted"})
			return
		}
		if strings.HasPrefix(path, "/panel/api/clients/traffic/") {
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
		TelegramAdminID:  123456,
		SiteURL:          "https://example.com",
		TelegramBotToken: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
		GlobalSubURL:     "",
		Sources: []config.Source{
			{Name: "main", XUIHost: server.URL, XUIAPIToken: "test-api-token", XUIInboundIDs: "[1]", Active: true},
		},
	}

	xuiClient, err := xui.NewClient(server.URL, "test-api-token")
	require.NoError(t, err)

	xuiClients := map[uint]interfaces.XUIClient{1: xuiClient}
	nodes := e2eNodes(server.URL)
	subService := service.NewSubscriptionService(db, xuiClients, nodes, cfg, cfg.GlobalSubURL, &webhook.NoopSender{})

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
