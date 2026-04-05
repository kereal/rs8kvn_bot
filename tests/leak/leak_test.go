package leak

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/logger"

	"github.com/stretchr/testify/require"
)

func init() {
	_, _ = logger.Init("", "error")
}

func TestMemoryLeak_CreateDeleteCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	var initialMemStats runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&initialMemStats)

	initialGoroutines := runtime.NumGoroutine()

	for i := 0; i < 20; i++ {
		db, err := database.NewService(t.TempDir() + "/test_leak_" + string(rune(i+'a')) + ".db")
		require.NoError(t, err)

		ctx := context.Background()
		sub := &database.Subscription{
			TelegramID:     int64(1000000 + i),
			Username:       "leaktest",
			ClientID:       "client-leak",
			SubscriptionID: "sub-leak",
			InboundID:      1,
			TrafficLimit:   10737418240,
			Status:         "active",
		}
		err = db.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		err = db.Close()
		require.NoError(t, err)
	}

	runtime.GC()

	var finalMemStats runtime.MemStats
	runtime.ReadMemStats(&finalMemStats)
	finalGoroutines := runtime.NumGoroutine()

	t.Logf("Memory: before=%dKB, after=%dKB, delta=%dKB",
		initialMemStats.Alloc/1024, finalMemStats.Alloc/1024, (finalMemStats.Alloc-initialMemStats.Alloc)/1024)
	t.Logf("Goroutines: before=%d, after=%d, delta=%d", initialGoroutines, finalGoroutines, finalGoroutines-initialGoroutines)

	memGrowth := int64(finalMemStats.Alloc - initialMemStats.Alloc)
	require.Less(t, memGrowth, int64(50*1024*1024), "Memory should not grow excessively")
}

func TestGoroutineLeak_SubscriptionService(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	initialGoroutines := runtime.NumGoroutine()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, _ := database.NewService(t.TempDir() + "/test_goroutine_leak.db")
			if db != nil {
				db.Close()
			}
		}()
	}

	wg.Wait()

	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Goroutine leak test: initial=%d, final=%d, delta=%d", initialGoroutines, finalGoroutines, finalGoroutines-initialGoroutines)

	require.LessOrEqual(t, finalGoroutines, initialGoroutines+5, "Should not leak goroutines")
}
