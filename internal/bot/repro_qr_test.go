package bot

import (
	"context"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tap issues a callback query as if the user pressed an inline button on the
// message with the given id.
func tap(h *Handler, data string, chatID int64, msgID int) {
	h.HandleCallback(context.Background(), tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From:    &tgbotapi.User{ID: chatID, UserName: "u"},
			Data:    data,
			Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: chatID}, MessageID: msgID},
		},
	})
}

func newQRTestHandler(t *testing.T) (*Handler, *testutil.BotAPI) {
	t.Helper()
	ctx := context.Background()
	db := testutil.NewDatabaseService()
	cfg := &config.Config{TelegramAdminID: 123456, SiteURL: "https://x.com", GlobalSubURL: "https://x.com/sub/"}
	mockXUI := testutil.NewXUIClient()
	xuiClients := map[uint]interfaces.XUIClient{1: mockXUI}
	nodes := []database.Node{{ID: 1, IsActive: true, Host: "https://p", APIToken: "t", InboundIDs: "[1]"}}
	sub := &database.Subscription{TelegramID: 42, Username: "u", ClientID: "c", SubscriptionID: "s", Status: "active"}
	require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	svc := service.NewSubscriptionService(db, xuiClients, nil, nodes, cfg)
	bot := testutil.NewBotAPI()
	return NewHandler(bot, cfg, db, NewTestBotConfig(), svc, ""), bot
}

// TestNavigation_OpenAndBack asserts the shared navigation contract used by
// every "show content in its own message + Back button" screen (QR code,
// invite QR, etc.):
//
//   - opening the screen sends a NEW message (e.g. a photo) and does NOT delete
//     the underlying card/menu message;
//   - pressing Back deletes ONLY that screen's message, by the id the callback
//     carries (the button lives on that message), and leaves the rest intact.
//
// This is the behaviour we want for QR and any future screen that follows the
// same pattern, so the test is written against the contract, not the QR alone.
func TestNavigation_OpenAndBack(t *testing.T) {
	const (
		cardMsgID  = 100 // the subscription card / menu already on screen
		screenMsgID = 555 // the separate QR/invite message Telegram assigns
	)

	t.Run("qr_code", func(t *testing.T) {
		handler, bot := newQRTestHandler(t)

		tap(handler, "qr_code", 42, cardMsgID)

		// A separate QR message is sent, the card stays untouched.
		require.True(t, bot.SendCalledSafe(), "opening QR must send a message")
		assert.Empty(t, bot.DeletedMessageIDsSafe(), "opening QR must NOT delete the card (id %d)", cardMsgID)

		tap(handler, "back_to_subscription", 42, screenMsgID)

		// Back deletes exactly the QR screen message.
		assert.Equal(t, []int{screenMsgID}, bot.DeletedMessageIDsSafe(),
			"back must delete only the QR message (id %d)", screenMsgID)
		assert.NotContains(t, bot.DeletedMessageIDsSafe(), cardMsgID,
			"back must NOT delete the card (id %d)", cardMsgID)
	})

	t.Run("invite_qr", func(t *testing.T) {
		handler, bot := newQRTestHandler(t)

		// share_invite opens the invite screen, then qr_telegram shows its QR.
		tap(handler, "share_invite", 42, cardMsgID)
		tap(handler, "qr_telegram", 42, cardMsgID)

		require.True(t, bot.SendCalledSafe(), "opening invite QR must send a message")
		assert.Empty(t, bot.DeletedMessageIDsSafe(), "opening invite QR must NOT delete the card (id %d)", cardMsgID)

		tap(handler, "back_to_invite", 42, screenMsgID)

		assert.Equal(t, []int{screenMsgID}, bot.DeletedMessageIDsSafe(),
			"back must delete only the invite QR message (id %d)", screenMsgID)
		assert.NotContains(t, bot.DeletedMessageIDsSafe(), cardMsgID,
			"back must NOT delete the card (id %d)", cardMsgID)
	})
}
