package bot

import (
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func NewTestBotConfig() *BotConfig {
	return &BotConfig{
		Username:                "testbot",
		ID:                      123456789,
		FirstName:               "TestBot",
		IsBot:                   true,
		CanJoinGroups:           false,
		CanReadAllGroupMessages: false,
		SupportsInlineQueries:   false,
		loadedAt:                time.Now(),
	}
}

// newTestAdminHandler creates a Handler with admin config and a stub SubscriptionService
// wired to the provided mock objects. Eliminates repeated NewHandler + subscriptionService
// assignment across admin tests.
func newTestAdminHandler(cfg *config.Config, mockDB *testutil.DatabaseService, mockXUI *testutil.XUIClient, mockBot *testutil.BotAPI) *Handler {
	h := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
	xuiClients := map[uint]interfaces.XUIClient{1: mockXUI}
	nodes := []database.Node{{ID: 1, IsActive: true, Host: "http://localhost:2053", APIToken: "test-token", InboundIDs: "[1]", SubscriptionURL: "http://example.com/sub/"}}
	h.subscriptionService = service.NewSubscriptionService(mockDB, xuiClients, nil, nodes, cfg)
	h.subscriptionService.SetInvalidateFunc(h.cache.Invalidate)
	return h
}

// createTextUpdate creates a tgbotapi.Update with a plain (non-command) text message.
func createTextUpdate(fromUser *tgbotapi.User, text string) tgbotapi.Update {
	return tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: fromUser.ID},
			From: fromUser,
			Text: text,
		},
	}
}
// text string, fromUser *tgbotapi.User and return tgbotapi.Update.
func createCommandUpdate(messageID int64, fromUser *tgbotapi.User, text string) tgbotapi.Update {
	cmdLen := 0
	for _, ch := range text {
		if ch == ' ' {
			break
		}
		if ch == '/' {
			cmdLen = 0
		}
		cmdLen++
	}
	if cmdLen == 0 {
		cmdLen = len(text)
	}

	entities := []tgbotapi.MessageEntity{}
	if cmdLen > 0 && len(text) > 0 && text[0] == '/' {
		entities = append(entities, tgbotapi.MessageEntity{
			Type:   "bot_command",
			Offset: 0,
			Length: cmdLen,
		})
	}

	return tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat:     &tgbotapi.Chat{ID: fromUser.ID},
			From:     fromUser,
			Text:     text,
			Entities: entities,
		},
	}
}

// newTestHandlerWithSubService creates a Handler with a stub SubscriptionService
// wired from the provided mock DB and XUI. Falls back to fresh mocks when nil.
func newTestHandlerWithSubService(t *testing.T, cfg *config.Config, mockDB *testutil.DatabaseService, mockXUI *testutil.XUIClient, mockBot *testutil.BotAPI) *Handler {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{TelegramAdminID: 123456}
	}
	if mockDB == nil {
		mockDB = testutil.NewDatabaseService()
	}
	if mockXUI == nil {
		mockXUI = testutil.NewXUIClient()
	}
	if mockBot == nil {
		mockBot = testutil.NewBotAPI()
	}
	dbSources := []database.Node{{
		ID: 1, Name: "main", Host: "http://example.com",
		APIToken: "token", InboundIDs: "[1]", IsActive: true}}
	subService := service.NewSubscriptionService(
		mockDB,
		map[uint]interfaces.XUIClient{1: mockXUI},
		nil,
		dbSources,
		cfg)
	return NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), subService, "")
}
