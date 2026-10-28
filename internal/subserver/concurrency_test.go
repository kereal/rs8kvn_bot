package subserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/require"
)

// TestFetchAndAggregateSources_Parallel verifies that source fetches are
// performed concurrently (bounded by maxSourceConcurrency) without depending on
// wall-clock timing. Each test server tracks the number of in-flight requests
// and holds its handler open until a second handler has entered: the observed
// peak concurrency must exceed one (sequential would never exceed one) and stay
// within the semaphore bound, and every source must be aggregated.
func TestFetchAndAggregateSources_Parallel(t *testing.T) {
	t.Parallel()

	numNodes := 10

	// overlapTimeout bounds the wait for a second concurrent handler. When the
	// fetches run sequentially no second handler can ever enter, so the driver
	// stops waiting, releases the blocked handler, and the peak assertion below
	// reports the failure instead of hanging the suite.
	const overlapTimeout = 2 * time.Second

	var inFlight atomic.Int32
	var peak atomic.Int32

	// entered receives one token per handler that is inside the server; release
	// is closed once two handlers overlap, unblocking all of them.
	entered := make(chan struct{}, numNodes)
	release := make(chan struct{})

	go func() {
		timer := time.NewTimer(overlapTimeout)
		defer timer.Stop()

		for seen := 0; seen < 2; {
			select {
			case <-entered:
				seen++
			case <-timer.C:
				close(release)

				return
			}
		}

		close(release)
	}()

	nodes := make([]database.Node, numNodes)
	for i := range numNodes {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cur := inFlight.Add(1)
			for {
				prev := peak.Load()
				if cur <= prev || peak.CompareAndSwap(prev, cur) {
					break
				}
			}
			// Signal that this handler is in flight, then block until a sibling
			// has also entered (or the overlap deadline fires). No wall-clock
			// sleep: the peak above 1 is produced structurally.
			entered <- struct{}{}
			<-release
			inFlight.Add(-1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
		}))
		defer ts.Close()

		nodes[i] = database.Node{Name: "node", SubscriptionURL: ts.URL}
	}

	ctx := context.Background()
	_, success, total := fetchAndAggregateSources(ctx, "test", nodes)

	// All nodes must be fetched and counted, regardless of parallelism.
	require.Equal(t, numNodes, total, "every node must be counted")
	require.Equal(t, numNodes, success, "every fetch must succeed")

	// Structural proof of concurrency: more than one source was in flight at
	// the same time (sequential execution would never observe a peak above 1).
	require.GreaterOrEqual(t, int(peak.Load()), 2,
		"sources must be fetched concurrently, not sequentially")

	// And the bounded concurrency must be respected.
	require.LessOrEqual(t, int(peak.Load()), maxSourceConcurrency,
		"parallelism must not exceed the maxSourceConcurrency semaphore")
}
