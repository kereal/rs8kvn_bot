package main

import (
	"context"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type budgetCtxKey struct{}

// Бюджет создаётся лениво (отсчёт идёт от первого финансового апдейта, а не
// от запуска бота) и общий на весь drain. Отмена родительского контекста, уже
// наступившая к моменту остановки, drain не прерывает.
func TestFinancialDrainBudget_LazySharedAndDetachedFromParent(t *testing.T) {
	var budget financialDrainBudget

	defer budget.stop()

	assert.Nil(t, budget.ctx, "контекст не создаётся до первого финансового апдейта")

	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), budgetCtxKey{}, "kept"))
	t.Cleanup(cancelParent)

	first := budget.contextFor(parent)
	require.NotNil(t, first)

	_, hasDeadline := first.Deadline()
	assert.True(t, hasDeadline, "у drain-контекста есть общий бюджет времени")

	// Остановка уже началась: подтверждение оплаты всё равно должно
	// обработаться, поэтому родительская отмена не наследуется.
	cancelParent()
	assert.NoError(t, first.Err())

	assert.Equal(t, first, budget.contextFor(context.Background()), "бюджет общий на весь drain")
	assert.Equal(t, "kept", first.Value(budgetCtxKey{}), "значения родительского контекста сохраняются")
}

// stop отменяет созданный бюджет и безопасен, когда drain так и не начался.
func TestFinancialDrainBudget_Stop(t *testing.T) {
	var neverUsed financialDrainBudget

	neverUsed.stop()
	assert.Nil(t, neverUsed.ctx, "до первого апдейта отменять нечего")

	var budget financialDrainBudget

	ctx := budget.contextFor(context.Background())
	budget.stop()

	assert.ErrorIs(t, ctx.Err(), context.Canceled)

	// stop() раньше первого contextFor (остановка вышла за ShutdownTimeout,
	// а диспетчер ещё дорабатывал очередь): контекст не должен стать nil —
	// вызывающий код проверяет finCtx.Err() и упал бы паникой.
	var stoppedFirst financialDrainBudget

	stoppedFirst.stop()

	afterStop := stoppedFirst.contextFor(context.Background())
	require.NotNil(t, afterStop)
	assert.ErrorIs(t, afterStop.Err(), context.Canceled)
}

// drainBufferedUpdates не выбрасывает хвост канала на остановке: все
// буферизованные апдейты доходят до обработчика (раньше канал просто
// вычитывался в пустоту, а Telegram уже не переотправит их).
func TestDrainBufferedUpdates_ProcessesWholeBacklog(t *testing.T) {
	updates := make(chan tgbotapi.Update, 3)
	updates <- tgbotapi.Update{UpdateID: 1}
	updates <- tgbotapi.Update{UpdateID: 2, Message: &tgbotapi.Message{SuccessfulPayment: &tgbotapi.SuccessfulPayment{}}}
	updates <- tgbotapi.Update{UpdateID: 3}
	close(updates)

	var processed []int
	drainBufferedUpdates(context.Background(), updates, func(_ context.Context, u tgbotapi.Update) {
		processed = append(processed, u.UpdateID)
	})

	assert.Equal(t, []int{1, 2, 3}, processed, "буферизованные апдейты не должны теряться")
}
