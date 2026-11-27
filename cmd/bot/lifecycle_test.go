package main

import (
	"context"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/bot"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type financialCtxKey struct{}

// Финансовый апдейт уже принят из Telegram, поэтому отмена shutdown-контекста
// не должна прерывать его обработку. Значения родительского контекста при этом
// сохраняются, а HandlerTimeout накладывается внутри финансового обработчика.
func TestFinancialUpdateContext_DetachedFromShutdown(t *testing.T) {
	parent, cancel := context.WithCancel(
		context.WithValue(context.Background(), financialCtxKey{}, "kept"),
	)
	t.Cleanup(cancel)

	ctx := financialUpdateContext(parent)
	require.NotNil(t, ctx)

	cancel()

	assert.NoError(t, ctx.Err(), "shutdown не должен отменять принятый финансовый апдейт")
	assert.Equal(t, "kept", ctx.Value(financialCtxKey{}))
}

// drainBufferedUpdates не выбрасывает буферизованный successful_payment на
// остановке: financial-апдейт из хвоста канала проходит в финансовый маршрут.
func TestDrainBufferedUpdates_KeepsBufferedSuccessfulPayment(t *testing.T) {
	handler := &bot.Handler{}
	updates := make(chan tgbotapi.Update, 3)
	updates <- tgbotapi.Update{UpdateID: 1}
	updates <- tgbotapi.Update{UpdateID: 2, Message: &tgbotapi.Message{SuccessfulPayment: &tgbotapi.SuccessfulPayment{}}}
	updates <- tgbotapi.Update{UpdateID: 3, Message: &tgbotapi.Message{
		Text:     "/terms",
		Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len("/terms")}},
	}}
	close(updates)

	var routed []int
	drainBufferedUpdates(updates, handler.IsFinancialUpdate, func(u tgbotapi.Update) {
		routed = append(routed, u.UpdateID)
	})

	assert.Equal(t, []int{2, 3}, routed, "финансовые апдейты буфера не должны теряться")
}
