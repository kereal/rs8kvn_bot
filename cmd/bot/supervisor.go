package main

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"go.uber.org/zap"
)

// Паузы надзора за фоновыми воркерами. Переменные (а не константы), чтобы
// тесты могли уменьшить backoff.
var (
	// supervisorInitialBackoff — пауза перед первым перезапуском упавшего воркера.
	supervisorInitialBackoff = 5 * time.Second
	// supervisorMaxBackoff — верхняя граница экспоненциальной паузы.
	supervisorMaxBackoff = 2 * time.Minute
	// supervisorStableRun — прогон длиннее этой границы считается стабильным
	// и сбрасывает backoff к начальному.
	supervisorStableRun = time.Minute
)

// runSupervised запускает фоновый воркер и перезапускает его после паники.
//
// Контракт: возврат fn означает «воркер закончил работу» (контекст отменён или
// одноразовая задача выполнена) и перезапуска не влечёт. Паника означает «воркер
// упал» и влечёт перезапуск с экспоненциальным backoff, пока жив ctx. Без этого
// восстановление после паники оставляло горутину мёртвой до рестарта процесса:
// recoverAndReport глотал панику, процесс продолжал работать, /readyz оставался
// зелёным, и воркер молча не работал (см. инцидент с nil-deref в синке).
func runSupervised(ctx context.Context, name string, fn func(context.Context)) {
	runSupervisedWithWait(ctx, name, fn, waitSupervisorBackoff)
}

// waitSupervisorBackoff waits for the restart delay or cancellation.
func waitSupervisorBackoff(ctx context.Context, delay time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}

func runSupervisedWithWait(ctx context.Context, name string, fn func(context.Context), wait func(context.Context, time.Duration) bool) {
	backoff := supervisorInitialBackoff

	for {
		started := time.Now()
		crashed := runWorkerOnce(ctx, name, fn)

		if !crashed || ctx.Err() != nil {
			return
		}

		if time.Since(started) >= supervisorStableRun {
			backoff = supervisorInitialBackoff
		}

		metrics.WorkerRestartsTotal.WithLabelValues(name).Inc()
		logger.Error("worker panicked; restarting after backoff",
			zap.String("worker", name),
			zap.Duration("backoff", backoff),
		)

		if !wait(ctx, backoff) {
			return
		}

		backoff *= 2
		if backoff > supervisorMaxBackoff {
			backoff = supervisorMaxBackoff
		}
	}
}

// runWorkerOnce выполняет один прогон воркера и сообщает, упал ли он.
func runWorkerOnce(ctx context.Context, name string, fn func(context.Context)) (crashed bool) {
	defer func() {
		if r := recover(); r != nil {
			reportPanic(name, r)
			crashed = true
		}
	}()

	fn(ctx)

	return false
}

// reportPanic отправляет панику в Sentry и пишет стек в лог.
func reportPanic(component string, recovered any) {
	stack := debug.Stack()

	sentry.CurrentHub().Recover(recovered)
	sentry.Flush(logger.SentryPanicFlushTimeout)
	logger.Error(component+" panicked",
		zap.Any("panic", recovered),
		zap.String("stack", string(stack)),
	)
}

// recoverAndReport recovers from panics, reports to Sentry, and logs the error.
// Для долгоживущих воркеров используйте runSupervised: он перезапускает
// упавший воркер, а не просто гасит панику.
// Usage: defer recoverAndReport("Component name")
func recoverAndReport(component string) {
	if r := recover(); r != nil {
		reportPanic(component, r)
	}
}
