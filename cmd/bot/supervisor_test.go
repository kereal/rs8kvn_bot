package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/web"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// runStamp фиксирует границы одного прогона воркера; пауза надзора — это
// интервал «конец прогона → начало следующего».
type runStamp struct {
	start, end time.Time
}

// collectStamps собирает все runStamp из канала (закрыв его).
func collectStamps(ch chan runStamp) []runStamp {
	close(ch)
	var stamps []runStamp
	for s := range ch {
		stamps = append(stamps, s)
	}

	return stamps
}

// Тест поведения: паузы между перезапусками растут экспоненциально (до
// supervisorMaxBackoff), а стабильный прогон длиннее supervisorStableRun
// сбрасывает backoff к начальному.
func TestRunSupervised_BackoffEscalation(t *testing.T) {
	setBackoff := func(t *testing.T, initial, max, stable time.Duration) {
		t.Helper()

		prevInitial, prevMax, prevStable := supervisorInitialBackoff, supervisorMaxBackoff, supervisorStableRun
		supervisorInitialBackoff, supervisorMaxBackoff, supervisorStableRun = initial, max, stable
		t.Cleanup(func() {
			supervisorInitialBackoff, supervisorMaxBackoff, supervisorStableRun = prevInitial, prevMax, prevStable
		})
	}

	// runStamp фиксирует границы одного прогона воркера; пауза надзора — это
	// интервал «конец прогона → начало следующего».
	type runStamp struct {
		start, end time.Time
	}

	// collectStamps собирает все runStamp из канала (закрыв его).
	collectStamps := func(ch chan runStamp) []runStamp {
		close(ch)
		var stamps []runStamp
		for s := range ch {
			stamps = append(stamps, s)
		}

		return stamps
	}

	t.Run("backoff grows between restarts", func(t *testing.T) {
		setBackoff(t, 10*time.Millisecond, 40*time.Millisecond, time.Hour) // reset недостижим

		stampsCh := make(chan runStamp, 16)
		var runs atomic.Int32

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)

			runSupervised(ctx, "test-backoff-grow", func(context.Context) {
				start := time.Now()
				defer func() { stampsCh <- runStamp{start: start, end: time.Now()} }()

				// 5 прогонов: 4 рестарта с паузами 10→20→40→40 мс; после
				// пятого отменяем контекст, чтобы надзор остановился.
				if runs.Add(1) >= 5 {
					cancel()
				}
				panic("boom")
			})
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("supervisor did not finish")
		}

		stamps := collectStamps(stampsCh)
		require.Len(t, stamps, 5, "ожидается 5 прогонов: 4 паники с рестартом + отменяющий")

		delay := func(i int) time.Duration { return stamps[i+1].start.Sub(stamps[i].end) }
		// Ожидаемые паузы: 10мс → 20мс → 40мс → 40мс (потолок).
		assert.Less(t, delay(0), delay(1), "backoff должен расти: 10мс → 20мс")
		assert.Less(t, delay(1), delay(2), "backoff должен расти: 20мс → 40мс")
		assert.GreaterOrEqual(t, delay(2), 40*time.Millisecond, "backoff должен дорасти до максимума")
		assert.Less(t, delay(3), delay(2)+5*time.Millisecond, "после максимума backoff не растёт дальше 40мс")
	})

	t.Run("backoff resets after stable run", func(t *testing.T) {
		setBackoff(t, 10*time.Millisecond, 40*time.Millisecond, 30*time.Millisecond)

		stampsCh := make(chan runStamp, 16)
		var runs atomic.Int32

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)

			runSupervised(ctx, "test-backoff-reset", func(context.Context) {
				start := time.Now()
				defer func() { stampsCh <- runStamp{start: start, end: time.Now()} }()

				n := runs.Add(1)
				// Третий прогон длится дольше supervisorStableRun: backoff
				// перед следующим рестартом сбрасывается к начальному.
				if n == 3 {
					time.Sleep(50 * time.Millisecond)
				}
				if n >= 4 {
					cancel()
				}
				panic("boom")
			})
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("supervisor did not finish")
		}

		stamps := collectStamps(stampsCh)
		require.Len(t, stamps, 4)

		delay := func(i int) time.Duration { return stamps[i+1].start.Sub(stamps[i].end) }
		// Ожидаемые паузы: 10мс, 20мс, затем сброс после стабильного прогона → 10мс.
		assert.Less(t, delay(0), delay(1), "backoff растёт до сброса: 10мс → 20мс")
		assert.Less(t, delay(2), delay(1), "стабильный прогон должен сбросить backoff к начальному")
		assert.LessOrEqual(t, delay(2), 15*time.Millisecond, "после сброса пауза снова около начальных 10мс")
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
