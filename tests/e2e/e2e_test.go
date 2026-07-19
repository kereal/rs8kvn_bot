

package e2e

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/bot"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

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
	xui        *testutil.XUIClient
	botAPI     *testutil.BotAPI
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

	mockXUI := testutil.NewXUIClient()
	mockBotAPI := testutil.NewBotAPI()

	botCfg := &bot.BotConfig{
		Username:  "testbot",
		ID:        123456789,
		FirstName: "TestBot",
		IsBot:     true,
	}

	ctx := context.Background()
	node := &database.Node{Name: "main", IsActive: true, Host: "https://panel.example.com", APIToken: "test-api-token", Type: database.NodeType3xUI, InboundIDs: "[1]"}
	require.NoError(t, db.CreateNode(ctx, node), "create test node")
	require.NoError(t, db.LinkNodeToPlan(ctx, "trial", node.ID), "link node to trial plan")
	require.NoError(t, db.LinkNodeToPlan(ctx, "free", node.ID), "link node to free plan")
	xuiClients := map[uint]interfaces.XUIClient{1: mockXUI}
	nodes := e2eNodes("https://panel.example.com")
	subService := service.NewSubscriptionService(db, xuiClients, nil, nodes, cfg)
	handler := bot.NewHandler(mockBotAPI, cfg, db, botCfg, subService, "")

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

func resetBotAPI(m *testutil.BotAPI) {
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
