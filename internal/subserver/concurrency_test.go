package subserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
)

func TestFetchAndAggregateSources_Parallel(t *testing.T) {
	// 10 nodes, each sleeping for 100ms.
	// Sequential: 1000ms.
	// Parallel (concurrency 8): ~200ms.
	numNodes := 10
	sleepTime := 100 * time.Millisecond

	nodes := make([]database.Node, numNodes)
	for i := 0; i < numNodes; i++ {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Intentional delay: simulates node work so the test can verify
			// that nodes are fetched in parallel (not sequentially).
			time.Sleep(sleepTime)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		}))
		defer ts.Close()
		nodes[i] = database.Node{Name: "node", SubscriptionURL: ts.URL}
	}

	start := time.Now()
	ctx := context.Background()
	_, _, _ = fetchAndAggregateSources(ctx, "test", nodes)
	duration := time.Since(start)

	// Verify parallel execution (significantly less than sequential)
	assert.Less(t, duration, 800*time.Millisecond, "Should be faster than sequential execution")
	// Verify it's not too fast (respects concurrency constraints)
	assert.GreaterOrEqual(t, duration, 150*time.Millisecond, "Should respect concurrency limits")
}
