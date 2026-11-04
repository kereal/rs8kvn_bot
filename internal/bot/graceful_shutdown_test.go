package bot

import (
	"context"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
)

func TestGracefulShutdown_StopsAllBackgroundLoops(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
	}

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewBotStoreFake()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "test")

	ctx, cancel := context.WithCancel(context.Background())

	handler.StartCacheCleanup(ctx, 10*time.Millisecond)
	handler.StartRateLimiterCleanup(ctx, 10*time.Millisecond, 50*time.Millisecond)
	handler.StartReferralCacheSync(ctx)

	cancel()

	done := make(chan struct{})

	go func() {
		handler.WaitForBackgroundGoroutines()
		close(done)
	}()

	select {
	case <-done:
		t.Log("All background loops stopped after cancel")
	case <-time.After(2 * time.Second):
		t.Error("Shutdown timeout - background goroutines leaked after cancel")
	}
}

func TestGracefulShutdown_RateLimiterCleanup(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
	}

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewBotStoreFake()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "test")

	// Exercise the limiter so a bucket exists before cleanup.
	if !handler.checkRateLimit(123456) {
		t.Error("rate limiter should allow first request")
	}

	ctx, cancel := context.WithCancel(context.Background())

	handler.StartRateLimiterCleanup(ctx, 10*time.Millisecond, 50*time.Millisecond)
	cancel()

	done := make(chan struct{})

	go func() {
		handler.WaitForBackgroundGoroutines()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Rate limiter cleanup loop stopped after cancel")
	case <-time.After(2 * time.Second):
		t.Error("Shutdown timeout - rate limiter cleanup goroutine leaked")
	}
}
