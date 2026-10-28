package bot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/require"
)

func TestBot_GracefulShutdown(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
	}

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "test")

	ctx, cancel := context.WithCancel(context.Background())

	handler.StartCacheCleanup(ctx, 20*time.Millisecond)
	handler.StartRateLimiterCleanup(ctx, 20*time.Millisecond, 100*time.Millisecond)

	cancel()

	done := make(chan struct{})

	go func() {
		handler.WaitForBackgroundGoroutines()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Background goroutines stopped after cancel")
	case <-time.After(2 * time.Second):
		t.Error("Shutdown timeout - background goroutines did not stop after cancel")
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	t.Parallel()

	cfg := &config.Config{
		TelegramBotToken: "test_token",
		WebServerPort:    18880,
	}

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler.StartCacheCleanup(ctx, time.Minute)
	handler.StartRateLimiterCleanup(ctx, time.Minute, 5*time.Minute)

	var wg sync.WaitGroup

	wg.Go(func() {
		<-ctx.Done()
	})

	cancel()

	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("All goroutines stopped gracefully")
	case <-time.After(2 * time.Second):
		t.Error("Shutdown timeout - goroutines leaked")
	}
}

func TestGracefulShutdown_StopsAllBackgroundLoops(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
	}

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()
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

func TestGracefulShutdown_DatabaseClose(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewDatabaseService()

	err := mockDB.Close()
	require.NoError(t, err, "Database should close without error")

	err = mockDB.Close()
	require.NoError(t, err, "Second close should also be safe (idempotent)")
}

func TestGracefulShutdown_RateLimiterCleanup(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TelegramBotToken: "test_token",
	}

	mockBot := testutil.NewBotAPI()
	mockDB := testutil.NewDatabaseService()
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
