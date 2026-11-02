package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// shortenSupervisorBackoff ускоряет надзор на время теста и восстанавливает
// значения после него.
func shortenSupervisorBackoff(t *testing.T) {
	t.Helper()

	prevInitial, prevMax, prevStable := supervisorInitialBackoff, supervisorMaxBackoff, supervisorStableRun
	supervisorInitialBackoff = time.Millisecond
	supervisorMaxBackoff = 2 * time.Millisecond
	supervisorStableRun = time.Hour

	t.Cleanup(func() {
		supervisorInitialBackoff, supervisorMaxBackoff, supervisorStableRun = prevInitial, prevMax, prevStable
	})
}

// Тест поведения: упавший воркер поднимается заново, а не остаётся мёртвым до
// рестарта процесса.
func TestRunSupervised_RestartsAfterPanic(t *testing.T) {
	shortenSupervisorBackoff(t)

	before := testutil.ToFloat64(metrics.WorkerRestartsTotal.WithLabelValues("test-restart"))

	var runs atomic.Int32

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)

		runSupervised(ctx, "test-restart", func(context.Context) {
			if runs.Add(1) <= 2 {
				panic("boom")
			}
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not finish")
	}

	assert.Equal(t, int32(3), runs.Load(), "воркер должен быть выполнен 3 раза: 2 паники + успешный прогон")

	after := testutil.ToFloat64(metrics.WorkerRestartsTotal.WithLabelValues("test-restart"))
	assert.Equal(t, float64(2), after-before, "каждая паника должна увеличивать счётчик рестартов")
}

// Тест поведения: штатный возврат воркера — это «работа закончена», а не падение.
func TestRunSupervised_DoesNotRestartAfterNormalReturn(t *testing.T) {
	shortenSupervisorBackoff(t)

	var runs atomic.Int32

	runSupervised(context.Background(), "test-clean-exit", func(context.Context) {
		runs.Add(1)
	})

	assert.Equal(t, int32(1), runs.Load(), "воркер, вернувшийся без паники, не перезапускается")
}

// Тест поведения: отменённый контекст останавливает надзор — паника во время
// остановки не должна порождать новый запуск.
func TestRunSupervised_StopsOnContextCancel(t *testing.T) {
	shortenSupervisorBackoff(t)

	var runs atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())

	runSupervised(ctx, "test-cancel", func(context.Context) {
		runs.Add(1)
		cancel()
		panic("boom during shutdown")
	})

	assert.Equal(t, int32(1), runs.Load(), "после отмены контекста перезапуска быть не должно")
}