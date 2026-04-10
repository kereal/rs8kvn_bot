package bot

import (
	"context"
	"sync"
	"testing"
	"time"

	"rs8kvn_bot/internal/testutil"
)

func TestReferralCache_NewReferralCache(t *testing.T) {
	rc := NewReferralCache(nil)
	if rc == nil {
		t.Fatal("expected non-nil ReferralCache")
	}
	if got := rc.Get(123); got != 0 {
		t.Errorf("expected 0 for empty cache, got %d", got)
	}
	if got := rc.GetAll(); len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestReferralCache_Get(t *testing.T) {
	rc := NewReferralCache(nil)

	// Unknown chatID returns 0
	if got := rc.Get(999); got != 0 {
		t.Errorf("expected 0 for unknown chatID, got %d", got)
	}

	// Known chatID returns correct count
	rc.SetForTest(42, 5)
	if got := rc.Get(42); got != 5 {
		t.Errorf("expected 5 for known chatID, got %d", got)
	}
}

func TestReferralCache_Get_All(t *testing.T) {
	rc := NewReferralCache(nil)
	rc.SetForTest(1, 10)
	rc.SetForTest(2, 20)

	result := rc.GetAll()

	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result[1] != 10 {
		t.Errorf("expected result[1]=10, got %d", result[1])
	}
	if result[2] != 20 {
		t.Errorf("expected result[2]=20, got %d", result[2])
	}

	// Mutation-safe: modifying the returned map should not affect the cache
	result[1] = 999
	if got := rc.Get(1); got != 10 {
		t.Errorf("expected cache to still have 10 after mutating returned map, got %d", got)
	}
}

func TestReferralCache_SetForTest(t *testing.T) {
	rc := NewReferralCache(nil)

	rc.SetForTest(100, 7)
	if got := rc.Get(100); got != 7 {
		t.Errorf("expected 7 after SetForTest, got %d", got)
	}

	// Overwrite existing
	rc.SetForTest(100, 42)
	if got := rc.Get(100); got != 42 {
		t.Errorf("expected 42 after overwrite, got %d", got)
	}
}

func TestReferralCache_Increment(t *testing.T) {
	rc := NewReferralCache(nil)

	// New entry creates count=1
	rc.Increment(10)
	if got := rc.Get(10); got != 1 {
		t.Errorf("expected 1 after first increment, got %d", got)
	}

	// Existing increments
	rc.Increment(10)
	if got := rc.Get(10); got != 2 {
		t.Errorf("expected 2 after second increment, got %d", got)
	}
}

func TestReferralCache_Increment_Multiple(t *testing.T) {
	rc := NewReferralCache(nil)

	for i := 0; i < 5; i++ {
		rc.Increment(55)
	}
	if got := rc.Get(55); got != 5 {
		t.Errorf("expected 5 after 5 increments, got %d", got)
	}

	// Different chatID is independent
	rc.Increment(66)
	rc.Increment(66)
	if got := rc.Get(66); got != 2 {
		t.Errorf("expected 2 for chatID 66, got %d", got)
	}
	if got := rc.Get(55); got != 5 {
		t.Errorf("expected 5 for chatID 55 still, got %d", got)
	}
}

func TestReferralCache_Decrement(t *testing.T) {
	rc := NewReferralCache(nil)

	rc.SetForTest(10, 3)
	rc.Decrement(10)
	if got := rc.Get(10); got != 2 {
		t.Errorf("expected 2 after decrement from 3, got %d", got)
	}
}

func TestReferralCache_Decrement_Unknown(t *testing.T) {
	rc := NewReferralCache(nil)

	// Decrement on unknown chatID creates entry with count=0
	rc.Decrement(999)
	if got := rc.Get(999); got != 0 {
		t.Errorf("expected 0 for decremented unknown chatID, got %d", got)
	}
}

func TestReferralCache_Decrement_DoesNotGoNegative(t *testing.T) {
	rc := NewReferralCache(nil)

	rc.SetForTest(10, 1)
	rc.Decrement(10) // 1 -> 0
	if got := rc.Get(10); got != 0 {
		t.Errorf("expected 0 after decrement from 1, got %d", got)
	}

	rc.Decrement(10) // 0 -> 0, should not go negative
	if got := rc.Get(10); got != 0 {
		t.Errorf("expected 0, count should not go negative, got %d", got)
	}
}

func TestReferralCache_Save_IsNoOp(t *testing.T) {
	rc := NewReferralCache(nil)
	rc.SetForTest(1, 5)

	err := rc.Save(context.Background())
	if err != nil {
		t.Errorf("expected no error from Save, got %v", err)
	}

	// Cache state unchanged after Save
	if got := rc.Get(1); got != 5 {
		t.Errorf("expected cache state unchanged after Save, got %d", got)
	}
}

func TestReferralCache_Sync_RefreshesFromDB(t *testing.T) {
	mockDB := testutil.NewMockDatabaseService()
	mockDB.GetAllReferralCountsFunc = func(ctx context.Context) (map[int64]int64, error) {
		return map[int64]int64{100: 10, 200: 20}, nil
	}

	rc := NewReferralCache(mockDB)

	// Increment in cache (simulating a stale value)
	rc.Increment(100) // count=1 in cache
	rc.Increment(200) // count=1 in cache

	// Sync reloads from DB, replacing in-memory values
	err := rc.Sync(context.Background())
	if err != nil {
		t.Fatalf("unexpected error from Sync: %v", err)
	}

	if got := rc.Get(100); got != 10 {
		t.Errorf("expected 10 after sync from DB for chatID 100, got %d", got)
	}
	if got := rc.Get(200); got != 20 {
		t.Errorf("expected 20 after sync from DB for chatID 200, got %d", got)
	}
}

func TestReferralCache_GetAll_ConcurrentSafe(t *testing.T) {
	rc := NewReferralCache(nil)
	rc.SetForTest(1, 10)
	rc.SetForTest(2, 20)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := rc.GetAll()
			if len(m) != 2 {
				t.Errorf("expected 2 entries, got %d", len(m))
			}
		}()
	}
	wg.Wait()
}

func TestReferralCache_CheckAdminSendRateLimit(t *testing.T) {
	rc := NewReferralCache(nil)

	// First call allows
	if allowed := rc.CheckAdminSendRateLimit(42); !allowed {
		t.Error("expected first call to be allowed")
	}

	// Second call within 30s blocks
	if allowed := rc.CheckAdminSendRateLimit(42); allowed {
		t.Error("expected second call within 30s to be blocked")
	}
}

func TestReferralCache_CheckAdminSendRateLimit_AfterWait(t *testing.T) {
	rc := NewReferralCache(nil)

	// First call
	if allowed := rc.CheckAdminSendRateLimit(42); !allowed {
		t.Error("expected first call to be allowed")
	}

	// Manually set the stored time to 31 seconds ago to simulate waiting
	rc.sendMu.Store(int64(42), time.Now().Add(-31*time.Second))

	// Should allow again after 30s
	if allowed := rc.CheckAdminSendRateLimit(42); !allowed {
		t.Error("expected call to be allowed after 30s wait")
	}
}

func TestReferralCache_ClearAdminSendRateLimit(t *testing.T) {
	rc := NewReferralCache(nil)

	// First call
	if allowed := rc.CheckAdminSendRateLimit(42); !allowed {
		t.Error("expected first call to be allowed")
	}

	// Second call blocked
	if allowed := rc.CheckAdminSendRateLimit(42); allowed {
		t.Error("expected second call to be blocked")
	}

	// Clear the rate limit
	rc.ClearAdminSendRateLimit(42)

	// Should allow again after clear
	if allowed := rc.CheckAdminSendRateLimit(42); !allowed {
		t.Error("expected call to be allowed after clearing rate limit")
	}
}
