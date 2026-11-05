package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-0002 P3: ten failed authorizations per minute per IP, then 429.
func TestWithAuth_LimitsFailedLogins(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	for attempt := range failedAuthBurst {
		rec := do(t, srv, http.MethodGet, "/admin/api/plans", "wrong", "")
		require.Equal(t, http.StatusUnauthorized, rec.Code, "attempt %d", attempt+1)
	}

	rec := do(t, srv, http.MethodGet, "/admin/api/plans", "wrong", "")

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "60", rec.Header().Get("Retry-After"))
	assert.Contains(t, rec.Body.String(), `"rate_limited"`)
}

// The limiter must never lock the operator out: a correct token is served even
// after the failure budget is spent.
func TestWithAuth_ValidTokenSurvivesSpentBudget(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	for range failedAuthBurst {
		do(t, srv, http.MethodGet, "/admin/api/session", "wrong", "")
	}

	require.Equal(t, http.StatusTooManyRequests, do(t, srv, http.MethodGet, "/admin/api/session", "wrong", "").Code)

	rec := do(t, srv, http.MethodGet, "/admin/api/session", testToken, "")

	require.Equal(t, http.StatusOK, rec.Code)
	// The successful call refilled the bucket, so guessing starts from a full
	// budget again.
	assert.Equal(t, http.StatusUnauthorized, do(t, srv, http.MethodGet, "/admin/api/session", "wrong", "").Code)
}

func TestWithAuth_SuccessResetsFailureBudget(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	for range failedAuthBurst - 1 {
		require.Equal(t, http.StatusUnauthorized, do(t, srv, http.MethodGet, "/admin/api/session", "wrong", "").Code)
	}

	require.Equal(t, http.StatusOK, do(t, srv, http.MethodGet, "/admin/api/session", testToken, "").Code)

	// Without the reset these would be 429: the budget was one failure away
	// from empty.
	for attempt := range failedAuthBurst - 1 {
		rec := do(t, srv, http.MethodGet, "/admin/api/session", "wrong", "")
		require.Equal(t, http.StatusUnauthorized, rec.Code, "attempt after success %d", attempt+1)
	}
}

func TestFailedAuthLimiter_IsolatesKeyAndRefills(t *testing.T) {
	t.Parallel()

	// Fast refill keeps the test independent of the production minute window.
	limiter := newFailedAuthLimiter(2, 1000, 2)

	limiter.failure("192.0.2.1")
	limiter.failure("192.0.2.1")

	assert.True(t, limiter.exceeded("192.0.2.1"))
	assert.False(t, limiter.exceeded("192.0.2.2"), "another IP keeps its own budget")

	time.Sleep(5 * time.Millisecond)
	assert.False(t, limiter.exceeded("192.0.2.1"), "tokens refill over time")

	// Keys are bounded like utils.IntervalGate keeps its map bounded, so a
	// scanner cannot grow it without limit.
	limiter.failure("a")
	limiter.failure("b")
	limiter.failure("c")

	assert.LessOrEqual(t, len(limiter.buckets), 2)
}

func TestClientIP_IgnoresPortAndForwardedHeader(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/session", nil)
	req.RemoteAddr = "192.0.2.7:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")

	assert.Equal(t, "192.0.2.7", clientIP(req))

	req.RemoteAddr = "[2001:db8::1]:443"
	assert.Equal(t, "2001:db8::1", clientIP(req))

	req.RemoteAddr = "192.0.2.7"
	assert.Equal(t, "192.0.2.7", clientIP(req), "address without a port is kept as is")
}
