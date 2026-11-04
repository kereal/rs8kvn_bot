package main

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/web"
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

// Тест поведения: backoff растёт до максимума и сбрасывается после стабильного прогона.
func TestRunSupervised_BackoffEscalation(t *testing.T) {
	setBackoff := func(t *testing.T, initial, max, stable time.Duration) {
		t.Helper()
		prevInitial, prevMax, prevStable := supervisorInitialBackoff, supervisorMaxBackoff, supervisorStableRun
		supervisorInitialBackoff, supervisorMaxBackoff, supervisorStableRun = initial, max, stable
		t.Cleanup(func() {
			supervisorInitialBackoff, supervisorMaxBackoff, supervisorStableRun = prevInitial, prevMax, prevStable
		})
	}

	t.Run("backoff grows between restarts", func(t *testing.T) {
		setBackoff(t, 10*time.Millisecond, 40*time.Millisecond, time.Hour)
		var delays []time.Duration
		runs := 0
		runSupervisedWithWait(context.Background(), "test-backoff-grow", func(context.Context) {
			runs++
			if runs == 5 {
				return
			}
			panic("boom")
		}, func(_ context.Context, delay time.Duration) bool {
			delays = append(delays, delay)
			return true
		})
		assert.Equal(t, 5, runs)
		assert.Equal(t, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond}, delays)
	})

	t.Run("backoff resets after stable run", func(t *testing.T) {
		// Виртуальное время исключает задержки планировщика; запас относительно
		// таймаута отправки паники не замедляет тест.
		synctest.Test(t, func(t *testing.T) {
			setBackoff(t, 10*time.Millisecond, 40*time.Millisecond, 30*time.Second)
			var delays []time.Duration
			runs := 0
			runSupervisedWithWait(context.Background(), "test-backoff-reset", func(context.Context) {
				runs++
				if runs == 3 {
					time.Sleep(time.Minute)
				}
				if runs == 4 {
					return
				}
				panic("boom")
			}, func(_ context.Context, delay time.Duration) bool {
				delays = append(delays, delay)
				return true
			})
			assert.Equal(t, 4, runs)
			assert.Equal(t, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 10 * time.Millisecond}, delays)
		})
	})
}

type stubNodeHealth struct {
	total int
	down  int
}

func (s stubNodeHealth) NodeHealthSnapshot() (int, int) { return s.total, s.down }

// Тест поведения: готовность сервиса отражает состояние нод, а не только БД —
// пять дней лежащая нода больше не выглядит зелёной.
func TestNodeHealthComponent(t *testing.T) {
	tests := []struct {
		name   string
		source nodeHealthSource
		status web.Status
	}{
		{name: "no source", source: nil, status: web.StatusOK},
		{name: "no readable nodes", source: stubNodeHealth{}, status: web.StatusOK},
		{name: "all nodes up", source: stubNodeHealth{total: 3}, status: web.StatusOK},
		{name: "one node down", source: stubNodeHealth{total: 3, down: 1}, status: web.StatusDegraded},
		{name: "all nodes down", source: stubNodeHealth{total: 3, down: 3}, status: web.StatusDown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.status, nodeHealthComponent(tt.source).Status)
		})
	}
}
