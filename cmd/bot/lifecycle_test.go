package main

import (
	"context"
	"testing"

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
}
