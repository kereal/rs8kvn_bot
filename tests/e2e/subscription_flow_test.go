package e2e

import (
	"context"
	"fmt"
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
	"rs8kvn_bot/internal/web"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_, _ = logger.Init("", "error")
}

func setupTestDB(t *testing.T) *database.Service {
	t.Helper()

	// Save and restore working directory
	origWd, _ := os.Getwd()
	defer os.Chdir(origWd)

	// Chdir to project root so findMigrationsDir() can find migrations
	projectRoot := findProjectRoot()
	os.Chdir(projectRoot)

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
	dbPath     string
	xui        *testutil.MockXUIClient
	botAPI     *testutil.MockBotAPI
	handler    *bot.Handler
	cfg        *config.Config
	botConfig  *bot.BotConfig
	chatID     int64
	username   string
	subService *service.SubscriptionService
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

	handler := bot.NewHandler(mockBotAPI, cfg, db, mockXUI, botCfg)
	subService := service.NewSubscriptionService(db, mockXUI, cfg)

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
	m.SendCalled = false
	m.RequestCalled = false
	m.LastSentText = ""
	m.LastChatID = 0
	m.SendCount = 0
	m.SendError = nil
	m.RequestError = nil
}

// newCommandMessage creates a Message with proper bot_command entity so CommandArguments() works.
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

func TestE2E_CreateSubscription_Success(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "Should not have subscription initially")

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.xui.AddClientWithIDCalled, "XUI AddClientWithID should be called")

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err, "Subscription should exist in DB")
	assert.Equal(t, env.chatID, sub.TelegramID)
	assert.Equal(t, env.username, sub.Username)
	assert.Equal(t, "active", sub.Status)
	assert.NotEmpty(t, sub.ClientID, "ClientID should be set")
	assert.NotEmpty(t, sub.SubscriptionID, "SubscriptionID should be set")
	assert.NotEmpty(t, sub.SubscriptionURL, "SubscriptionURL should be set")

	assert.True(t, env.botAPI.SendCalled, "Confirmation message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")
}

func TestE2E_CreateSubscription_NoDuplicate(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	resetMockBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 200,
			},
		},
	})

	assert.False(t, env.xui.AddClientWithIDCalled, "XUI should not be called for existing subscription")

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, len(allSubs), "Should have exactly one subscription")
}

func TestE2E_CreateSubscription_ConcurrentProtection(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)

	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			env.handler.HandleCallback(ctx, tgbotapi.Update{
				CallbackQuery: &tgbotapi.CallbackQuery{
					From: &tgbotapi.User{
						ID:       env.chatID,
						UserName: env.username,
					},
					Data: "create_subscription",
					Message: &tgbotapi.Message{
						Chat:      &tgbotapi.Chat{ID: env.chatID},
						MessageID: 100,
					},
				},
			})
		}()
	}

	wg.Wait()

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, len(allSubs), "Should have exactly one subscription despite concurrent calls")
}

func TestE2E_CreateSubscription_XUIFailure(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return nil, fmt.Errorf("connection refused")
	}

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Error message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подключиться к серверу", "Should show connection error message")

	_, err := env.db.GetByTelegramID(ctx, env.chatID)
	assert.Error(t, err, "No subscription should exist after XUI failure")
}

func TestE2E_StartCommand_NoSubscription(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: env.chatID},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Text: "/start",
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Main menu should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Привет", "Should greet user")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")
}

func TestE2E_StartCommand_WithSubscription(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/test-sub-id",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: env.chatID},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Text: "/start",
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Subscription menu should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "кнопки ниже", "Should show menu with buttons")
}

func TestE2E_MySubscription(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/test-sub-id",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	resetMockBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "menu_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Subscription info should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")
	assert.Contains(t, env.botAPI.LastSentText, "https://example.com/sub/test-sub-id", "Should contain subscription URL")
}

func TestE2E_HelpCommand(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleHelp(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: env.chatID},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Text: "/help",
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Help text should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Справка", "Should contain help text")
}

func TestE2E_InviteCommand(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleInvite(ctx, tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: env.chatID},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Text: "/invite",
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Invite link should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "пригласительная ссылка", "Should mention invite link")
	assert.Contains(t, env.botAPI.LastSentText, "t.me/testbot?start=share_", "Should contain telegram invite URL")
}

func TestE2E_CreateSubscription_TrafficLimitCorrect(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	var capturedTraffic int64
	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		capturedTraffic = trafficBytes
		return &xui.ClientConfig{
			ID:    "client-uuid-123",
			SubID: "sub-id-456",
		}, nil
	}

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	expectedTraffic := int64(env.cfg.TrafficLimitGB) * 1024 * 1024 * 1024
	assert.Equal(t, expectedTraffic, capturedTraffic, "Traffic limit should match config")
}

func TestE2E_CreateSubscription_SubscriptionURLFormat(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)

	assert.Contains(t, sub.SubscriptionURL, env.cfg.XUIHost, "URL should contain XUI host")
	assert.Contains(t, sub.SubscriptionURL, env.cfg.XUISubPath, "URL should contain sub path")
	assert.Contains(t, sub.SubscriptionURL, sub.SubscriptionID, "URL should contain subscription ID")
}

func TestE2E_CreateSubscription_UsernameStored(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, env.username, sub.Username, "Username should be stored correctly")
}

func TestE2E_MultipleUsers_Isolation(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	users := []struct {
		chatID   int64
		username string
	}{
		{111, "user1"},
		{222, "user2"},
		{333, "user3"},
	}

	for _, u := range users {
		env.handler.HandleCallback(ctx, tgbotapi.Update{
			CallbackQuery: &tgbotapi.CallbackQuery{
				From: &tgbotapi.User{
					ID:       u.chatID,
					UserName: u.username,
				},
				Data: "create_subscription",
				Message: &tgbotapi.Message{
					Chat:      &tgbotapi.Chat{ID: u.chatID},
					MessageID: 100,
				},
			},
		})
	}

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, len(allSubs), "Should have 3 subscriptions")

	for _, u := range users {
		sub, err := env.db.GetByTelegramID(ctx, u.chatID)
		require.NoError(t, err)
		assert.Equal(t, u.username, sub.Username, "Username should match for user %d", u.chatID)
	}
}

func TestE2E_Subscription_ReplacesOldActive(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	oldSub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "old-client-id",
		SubscriptionID:  "old-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/old",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, oldSub))

	// Create a second subscription directly via the service to trigger the revoke logic
	result, err := env.subService.Create(ctx, env.chatID, env.username)
	require.NoError(t, err)
	require.NotNil(t, result)

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)

	activeCount := 0
	revokedCount := 0
	for _, s := range allSubs {
		switch s.Status {
		case "active":
			activeCount++
		case "revoked":
			revokedCount++
		}
	}
	assert.Equal(t, 1, activeCount, "Should have exactly one active subscription")
	assert.Equal(t, 1, revokedCount, "Old subscription should be revoked")
}

func TestE2E_QRCodeGeneration(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/test-sub-id",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	resetMockBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "qr_code",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalled, "QR code should be sent")
}

func TestE2E_BackToSubscription(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "back_to_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.RequestCalled, "Should attempt to delete QR message")
}

func TestE2E_MenuHelp(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/test-sub-id",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	resetMockBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "menu_help",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Help should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Ваша подписка готова", "Should contain subscription help text")
}

func TestE2E_MenuDonate(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "menu_donate",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Donate info should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Поддержка", "Should contain donate info")
}

func TestE2E_BackToStart(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/test-sub-id",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	resetMockBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "back_to_start",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Main menu should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "кнопки ниже", "Should show subscription menu")
}

func TestE2E_AdminStats(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.cfg.TelegramAdminID,
				UserName: "admin",
			},
			Data: "admin_stats",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.cfg.TelegramAdminID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Admin stats should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Статистика", "Should contain stats")
}

func TestE2E_AdminLastReg(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	sub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "test-client-id",
		SubscriptionID:  "test-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "active",
		SubscriptionURL: "https://example.com/sub/test-sub-id",
		CreatedAt:       time.Now(),
	}
	require.NoError(t, env.db.CreateSubscription(ctx, sub))

	resetMockBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.cfg.TelegramAdminID,
				UserName: "admin",
			},
			Data: "admin_lastreg",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.cfg.TelegramAdminID},
				MessageID: 100,
			},
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Last registrations should be sent")
	assert.Contains(t, env.botAPI.LastSentText, env.username, "Should show registered user")
}

func TestE2E_CreateSubscription_RevokesOnlyActive(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	oldSub := &database.Subscription{
		TelegramID:      env.chatID,
		Username:        env.username,
		ClientID:        "old-client-id",
		SubscriptionID:  "old-sub-id",
		InboundID:       1,
		TrafficLimit:    107374182400,
		Status:          "expired",
		SubscriptionURL: "https://example.com/sub/old",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, oldSub))

	resetMockBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: env.chatID},
				MessageID: 100,
			},
		},
	})

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)

	expiredCount := 0
	for _, s := range allSubs {
		if s.Status == "expired" {
			expiredCount++
		}
	}
	assert.Equal(t, 1, expiredCount, "Expired subscription should not be revoked")
}

// === TRIAL FLOW ===

func TestE2E_TrialBind_Success(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	trialSubID := "trial-abc-123"
	_, err := env.db.CreateTrialSubscription(ctx, "test_invite_code", trialSubID, "trial-client-id", 1, 1073741824, time.Now().Add(24*time.Hour), "https://example.com/sub/trial-abc-123")
	require.NoError(t, err)

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalled, "Activation message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Подписка активирована", "Should confirm activation")

	bound, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, env.chatID, bound.TelegramID, "TelegramID should be set")
	assert.Equal(t, env.username, bound.Username, "Username should be set")
	assert.False(t, bound.IsTrial, "IsTrial should be false after bind")
}

func TestE2E_TrialBind_AlreadyHasSubscription(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	existingSub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "existing-client",
		SubscriptionID: "existing-sub",
		InboundID:      1,
		TrafficLimit:   107374182400,
		Status:         "active",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, existingSub))

	trialSubID := "trial-xyz-789"
	_, err := env.db.CreateTrialSubscription(ctx, "test_invite_code", trialSubID, "trial-client-id", 1, 1073741824, time.Now().Add(24*time.Hour), "https://example.com/sub/trial-xyz-789")
	require.NoError(t, err)

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalled, "Error message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "уже есть активная подписка", "Should reject with existing subscription message")
}

func TestE2E_TrialBind_NotFound(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_nonexistent", 6),
	})

	assert.True(t, env.botAPI.SendCalled, "Error message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Не удалось активировать", "Should show activation error message")
}

func TestE2E_TrialBind_AlreadyActivated(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	trialSubID := "trial-double-123"
	_, err := env.db.CreateTrialSubscription(ctx, "test_invite_code", trialSubID, "trial-client-id", 1, 1073741824, time.Now().Add(24*time.Hour), "https://example.com/sub/trial-double-123")
	require.NoError(t, err)

	// First bind
	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	resetMockBotAPI(env.botAPI)

	// Second bind attempt
	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalled, "Error message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "уже есть активная подписка", "Should reject already-bound trial")
}

// === REFERRAL FLOW ===

func TestE2E_ShareLink_CachesPendingInvite(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.db.GetOrCreateInvite(ctx, 111222, "sharecode123")
	require.NoError(t, err)

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_sharecode123", 6),
	})

	assert.True(t, env.botAPI.SendCalled, "Invite message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "пригласили", "Should show invited message")
	assert.Contains(t, env.botAPI.LastSentText, "реферальное", "Should mention referral")
}

func TestE2E_ShareLink_ExistingSubscription_Ignored(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	existingSub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "existing-client",
		SubscriptionID: "existing-sub",
		InboundID:      1,
		TrafficLimit:   107374182400,
		Status:         "active",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, existingSub))

	_, err := env.db.GetOrCreateInvite(ctx, 111222, "sharecode456")
	require.NoError(t, err)

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_sharecode456", 6),
	})

	assert.True(t, env.botAPI.SendCalled, "Menu should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "кнопки ниже", "Should show normal menu, not invite message")
}

func TestE2E_ShareLink_InvalidCode(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_invalidcode", 6),
	})

	assert.True(t, env.botAPI.SendCalled, "Menu should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Привет", "Should show normal menu for invalid code")
}

// === INVITE WEB FLOW ===

func TestE2E_InviteLink_CreatesTrial(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "invite_test_abc"
	_, err := env.db.GetOrCreateInvite(ctx, 200001, inviteCode)
	require.NoError(t, err)

	// Set high rate limit to avoid cross-test pollution
	env.cfg.TrialRateLimit = 100

	loginCalled := false
	env.xui.LoginFunc = func(ctx context.Context) error {
		loginCalled = true
		return nil
	}

	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig)

	req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req.Header.Set("X-Forwarded-For", "10.1.1.1")
	rec := httptest.NewRecorder()

	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	html := rec.Body.String()
	assert.Contains(t, html, "trial_", "Should contain trial activation link")
	assert.Contains(t, html, "https://t.me/", "Should contain Telegram link")

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	trialCount := 0
	for _, sub := range allSubs {
		if sub.IsTrial {
			trialCount++
		}
	}
	assert.Equal(t, 1, trialCount, "Trial subscription should be created in DB")
	assert.True(t, env.xui.AddClientWithIDCalled, "XUI AddClientWithID should be called")
	assert.True(t, loginCalled, "XUI Login should be called")
}

func TestE2E_InviteLink_InvalidCode(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	env.cfg.TrialRateLimit = 100

	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig)

	req := httptest.NewRequest("GET", "/i/nonexistent_code", nil)
	req.Header.Set("X-Forwarded-For", "10.1.2.1")
	rec := httptest.NewRecorder()

	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Приглашение не найдено", "Should show invite not found error")
}

func TestE2E_InviteLink_XuiLoginFails(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "invite_xui_fail"
	_, err := env.db.GetOrCreateInvite(ctx, 200003, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	env.xui.LoginFunc = func(ctx context.Context) error {
		return fmt.Errorf("login failed")
	}

	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig)

	req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req.Header.Set("X-Forwarded-For", "10.1.3.1")
	rec := httptest.NewRecorder()

	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Ошибка сервера", "Should show server error")
}

func TestE2E_InviteLink_RateLimitExceeded(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "invite_ratelimit"
	_, err := env.db.GetOrCreateInvite(ctx, 200004, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 1

	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig)

	req1 := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req1.Header.Set("X-Forwarded-For", "10.1.4.1")
	rec1 := httptest.NewRecorder()
	srv.HandleInvite(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code)

	req2 := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req2.Header.Set("X-Forwarded-For", "10.1.4.1")
	rec2 := httptest.NewRecorder()
	srv.HandleInvite(rec2, req2)

	assert.Equal(t, http.StatusTooManyRequests, rec2.Code)
	assert.Contains(t, rec2.Body.String(), "Слишком много запросов", "Should show rate limit error")
}

func TestE2E_InviteLink_FullFlow_BindTrial(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "invite_full_flow"
	_, err := env.db.GetOrCreateInvite(ctx, 200005, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig)

	req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req.Header.Set("X-Forwarded-For", "10.1.5.1")
	rec := httptest.NewRecorder()
	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	html := rec.Body.String()
	assert.Contains(t, html, "trial_", "Should contain trial link")

	subIDStart := strings.Index(html, "trial_")
	require.Greater(t, subIDStart, -1, "Should find trial_ in HTML")
	subIDEnd := strings.Index(html[subIDStart:], "\"")
	require.Greater(t, subIDEnd, -1, "Should find closing quote")
	trialSubID := html[subIDStart+6 : subIDStart+subIDEnd]

	resetMockBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalled, "Activation message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err, "Subscription should be bound to Telegram ID")
	assert.Equal(t, env.chatID, sub.TelegramID)
	assert.Equal(t, env.username, sub.Username)
	assert.False(t, sub.IsTrial, "Should no longer be marked as trial")
}

// === Full Integration Cycle Tests ===

func TestE2E_FullCycle_InviteToQR(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	// Phase 1: Create invite via web
	inviteCode := "full_cycle_qr"
	_, err := env.db.GetOrCreateInvite(ctx, 300001, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig)

	req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req.Header.Set("X-Forwarded-For", "10.2.1.1")
	rec := httptest.NewRecorder()
	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "Invite page should load")

	html := rec.Body.String()
	assert.Contains(t, html, "trial_", "Should contain trial activation link")

	// Extract trial sub ID
	subIDStart := strings.Index(html, "trial_")
	require.Greater(t, subIDStart, -1)
	subIDEnd := strings.Index(html[subIDStart:], "\"")
	require.Greater(t, subIDEnd, -1)
	trialSubID := html[subIDStart+6 : subIDStart+subIDEnd]

	// Phase 2: Bind trial via /start
	resetMockBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false
	env.xui.UpdateClientCalled = false

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalled, "Activation message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")

	// Verify DB state
	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, env.chatID, sub.TelegramID)
	assert.False(t, sub.IsTrial, "Should be converted from trial")
	assert.NotEmpty(t, sub.Username, "Username should be stored")

	// Phase 3: Request QR code
	resetMockBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			Message: &tgbotapi.Message{
				Chat: &tgbotapi.Chat{ID: env.chatID},
				From: &tgbotapi.User{
					ID:       env.chatID,
					UserName: env.username,
				},
			},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "qr_code",
		},
	})

	assert.True(t, env.botAPI.SendCalled, "QR should be sent")
	assert.True(t, env.botAPI.RequestCalled, "QR photo should be uploaded")
}

func TestE2E_FullCycle_ShareToSubscription(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	// Phase 1: Create invite link
	inviteCode := "share_to_sub"
	_, err := env.db.GetOrCreateInvite(ctx, 300002, inviteCode)
	require.NoError(t, err)

	// Phase 2: User clicks share link
	resetMockBotAPI(env.botAPI)
	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_"+inviteCode, 6),
	})

	assert.True(t, env.botAPI.SendCalled, "Should respond to share link")
	assert.Contains(t, env.botAPI.LastSentText, "пригласил", "Should mention invitation")

	// Phase 3: Create subscription via callback
	resetMockBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			Message: &tgbotapi.Message{
				Chat: &tgbotapi.Chat{ID: env.chatID},
				From: &tgbotapi.User{
					ID:       env.chatID,
					UserName: env.username,
				},
			},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
		},
	})

	assert.True(t, env.botAPI.SendCalled, "Subscription confirmation should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")

	// Verify subscription was created
	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, env.chatID, sub.TelegramID)
	// Note: ReferredBy is set in memory after DB save but not persisted back
	// This is a known limitation of the current implementation
}

func TestE2E_FullCycle_MultipleUsersViaInvite(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "multi_user_invite"
	referrerID := int64(300003)
	_, err := env.db.GetOrCreateInvite(ctx, referrerID, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig)

	// Two different users access the same invite link
	user1ChatID := int64(400001)
	user2ChatID := int64(400002)

	for _, chatID := range []int64{user1ChatID, user2ChatID} {
		req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.3.%d.1", chatID%256))
		rec := httptest.NewRecorder()
		srv.HandleInvite(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, "User %d should get trial page", chatID)

		html := rec.Body.String()
		subIDStart := strings.Index(html, "trial_")
		require.Greater(t, subIDStart, -1)
		subIDEnd := strings.Index(html[subIDStart:], "\"")
		require.Greater(t, subIDEnd, -1)
		trialSubID := html[subIDStart+6 : subIDStart+subIDEnd]

		resetMockBotAPI(env.botAPI)
		env.xui.AddClientWithIDCalled = false
		env.xui.UpdateClientCalled = false

		username := fmt.Sprintf("user_%d", chatID)
		env.handler.HandleStart(ctx, tgbotapi.Update{
			Message: newCommandMessage(chatID, chatID, username, "/start trial_"+trialSubID, 6),
		})

		assert.True(t, env.botAPI.SendCalled, "User %d should get activation message", chatID)

		sub, err := env.db.GetByTelegramID(ctx, chatID)
		require.NoError(t, err, "User %d should have subscription", chatID)
		assert.Equal(t, chatID, sub.TelegramID)
		assert.False(t, sub.IsTrial)
		assert.Equal(t, referrerID, sub.ReferredBy, "User %d should have correct referrer", chatID)
	}
}

func TestE2E_FullCycle_InviteThenShare(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	// Create two invites: one for web, one for share
	webInviteCode := "web_invite_then_share"
	shareInviteCode := "share_invite_then_bind"

	_, err := env.db.GetOrCreateInvite(ctx, 300004, webInviteCode)
	require.NoError(t, err)
	_, err = env.db.GetOrCreateInvite(ctx, 300005, shareInviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig)

	// User accesses web invite
	req := httptest.NewRequest("GET", "/i/"+webInviteCode, nil)
	req.Header.Set("X-Forwarded-For", "10.4.1.1")
	rec := httptest.NewRecorder()
	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	html := rec.Body.String()
	subIDStart := strings.Index(html, "trial_")
	require.Greater(t, subIDStart, -1)
	subIDEnd := strings.Index(html[subIDStart:], "\"")
	require.Greater(t, subIDEnd, -1)
	trialSubID := html[subIDStart+6 : subIDStart+subIDEnd]

	// Bind trial
	resetMockBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false
	env.xui.UpdateClientCalled = false

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalled)

	// Now user clicks a share link (should be ignored since they have a subscription)
	resetMockBotAPI(env.botAPI)
	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_"+shareInviteCode, 6),
	})

	assert.True(t, env.botAPI.SendCalled)
	// Should show regular menu, not invite message
	assert.NotContains(t, env.botAPI.LastSentText, "пригласил", "Should not show invite message when user has subscription")
}

func TestE2E_FullCycle_ConcurrentInviteAccess(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "concurrent_invite"
	_, err := env.db.GetOrCreateInvite(ctx, 300006, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 1000

	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig)

	var wg sync.WaitGroup
	results := make(chan int, 10)

	// 10 concurrent users access the same invite
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.5.%d.1", idx))
			rec := httptest.NewRecorder()
			srv.HandleInvite(rec, req)

			results <- rec.Code
		}(i)
	}

	wg.Wait()
	close(results)

	successCount := 0
	for code := range results {
		if code == http.StatusOK {
			successCount++
		}
	}

	assert.Equal(t, 10, successCount, "All concurrent requests should succeed")

	// Verify all trials were created
	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	trialCount := 0
	for _, sub := range allSubs {
		if sub.IsTrial && sub.TelegramID == 0 {
			trialCount++
		}
	}
	assert.GreaterOrEqual(t, trialCount, 10, "Should have at least 10 trial subscriptions")
}
