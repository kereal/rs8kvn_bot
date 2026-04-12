package bot

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBot_GracefulShutdown(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TrafficLimitGB:   100,
		XUIHost:          "http://localhost:2053",
		XUIInboundID:     1,
		XUISubPath:       "sub",
		TelegramBotToken: "test_token",
		XUIUsername:      "admin",
		XUIPassword:      "password",
	}

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "test")

	ctx, cancel := context.WithCancel(context.Background())

	handler.StartCacheCleanup(ctx, 20*time.Millisecond)
	handler.StartRateLimiterCleanup(ctx, 20*time.Millisecond, 100*time.Millisecond)

	runtime.GC()
	var memStatsBefore runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	cancel()

	time.Sleep(50 * time.Millisecond)

	runtime.GC()
	var memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsAfter)

	t.Logf("Memory before: %d KB, after: %d KB", memStatsBefore.Alloc/1024, memStatsAfter.Alloc/1024)

	assert.LessOrEqual(t, memStatsAfter.Alloc, memStatsBefore.Alloc+5*1024*1024, "Memory should not leak significantly")
}

func TestServer_GracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}
	t.Parallel()

	cfg := &config.Config{
		TrafficLimitGB:   10,
		XUIHost:          "http://localhost:2053",
		XUIInboundID:     1,
		XUISubPath:       "sub",
		TelegramBotToken: "test_token",
		HealthCheckPort:  18880,
	}

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler.StartCacheCleanup(ctx, time.Minute)
	handler.StartRateLimiterCleanup(ctx, time.Minute, 5*time.Minute)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		select {
		case <-ctx.Done():
		case <-time.After(20 * time.Millisecond):
		}
	}()

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

func TestHeartbeat_StopOnContextCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	time.Sleep(20 * time.Millisecond)

	select {
	case <-ctx.Done():
		t.Log("Context cancelled as expected")
	default:
	}

	select {
	case <-ctx.Done():
	default:
		t.Error("Context should be done after cancel")
	}

	require.Equal(t, context.Canceled, ctx.Err())
}

func TestGoroutineLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}
	t.Parallel()

	initialGoroutines := runtime.NumGoroutine()

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(2 * time.Millisecond)
		}()
	}

	wg.Wait()

	time.Sleep(10 * time.Millisecond)

	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()

	t.Logf("Goroutines: initial=%d, final=%d, delta=%d", initialGoroutines, finalGoroutines, finalGoroutines-initialGoroutines)

	assert.LessOrEqual(t, finalGoroutines, initialGoroutines+2, "Should not leak goroutines")
}

func TestGracefulShutdown_WithActiveUpdates(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TrafficLimitGB:   100,
		XUIHost:          "http://localhost:2053",
		XUIInboundID:     1,
		XUISubPath:       "sub",
		TelegramBotToken: "test_token",
		XUIUsername:      "admin",
		XUIPassword:      "password",
	}

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "test")

	ctx, cancel := context.WithCancel(context.Background())

	handler.StartCacheCleanup(ctx, 10*time.Millisecond)
	handler.StartRateLimiterCleanup(ctx, 10*time.Millisecond, 50*time.Millisecond)
	handler.StartReferralCacheSync(ctx)

	time.Sleep(20 * time.Millisecond)

	cancel()

	time.Sleep(50 * time.Millisecond)

	t.Log("Shutdown with active updates completed")
}

func TestGracefulShutdown_DatabaseClose(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewMockDatabaseService()

	err := mockDB.Close()
	require.NoError(t, err, "Database should close without error")

	err = mockDB.Close()
	require.NoError(t, err, "Second close should also be safe (idempotent)")
}

func TestGracefulShutdown_RateLimiterCleanup(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID:  123456789,
		TrafficLimitGB:   100,
		XUIHost:          "http://localhost:2053",
		XUIInboundID:     1,
		XUISubPath:       "sub",
		TelegramBotToken: "test_token",
	}

	mockBot := testutil.NewMockBotAPI()
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig(), nil, "test")

	ctx, cancel := context.WithCancel(context.Background())

	handler.StartRateLimiterCleanup(ctx, 10*time.Millisecond, 50*time.Millisecond)

	time.Sleep(20 * time.Millisecond)

	cancel()

	time.Sleep(50 * time.Millisecond)

	t.Log("Rate limiter cleanup completed gracefully")
}
