package admin

import (
	"net"
	"net/http"
	"sync"

	"github.com/kereal/rs8kvn_bot/internal/ratelimiter"
)

// ADR-0002 P3: at most ten failed authorizations per minute per source IP.
// The panel serves a single operator over an SSH tunnel, so the budget is
// deliberately crude: it exists to stop token guessing, not to shape load.
const (
	failedAuthBurst   = 10
	failedAuthRefill  = failedAuthBurst / 60.0 // tokens per second
	failedAuthPruneAt = 4096
)

// failedAuthLimiter throttles token guessing per source IP.
//
// Only failed attempts spend tokens and a successful one refills the bucket:
// otherwise an operator who pasted the token with a stray character would lock
// themselves out — the correct token always gets through.
type failedAuthLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ratelimiter.TokenBucket
	burst   float64
	refill  float64
	pruneAt int
}

func newFailedAuthLimiter(burst, refill float64, pruneAt int) *failedAuthLimiter {
	return &failedAuthLimiter{burst: burst, refill: refill, pruneAt: pruneAt}
}

// exceeded reports whether ip has burned its failure budget. It never spends a
// token, so callers can ask before deciding what to answer.
func (l *failedAuthLimiter) exceeded(ip string) bool {
	l.mu.Lock()
	bucket, ok := l.buckets[ip]
	l.mu.Unlock()

	if !ok {
		return false
	}

	return bucket.AvailableTokens() < 1
}

// failure spends one token for ip.
func (l *failedAuthLimiter) failure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[ip]
	if !ok {
		// The map is bounded the same way as utils.IntervalGate: drop every
		// bucket that has already refilled, and if that was not enough, start
		// over. Keys are source IPs of requests, so the map only grows if
		// something is actively guessing.
		if l.pruneAt > 0 && len(l.buckets) >= l.pruneAt {
			for key, candidate := range l.buckets {
				if candidate.AvailableTokens() >= l.burst {
					delete(l.buckets, key)
				}
			}

			if len(l.buckets) >= l.pruneAt {
				clear(l.buckets)
			}
		}

		if l.buckets == nil {
			l.buckets = make(map[string]*ratelimiter.TokenBucket)
		}

		bucket = ratelimiter.NewTokenBucket(l.burst, l.refill)
		l.buckets[ip] = bucket
	}

	bucket.Allow()
}

// success forgets the failures recorded for ip: a valid token proves the caller
// is the administrator.
func (l *failedAuthLimiter) success(ip string) {
	l.mu.Lock()
	bucket, ok := l.buckets[ip]
	l.mu.Unlock()

	if ok {
		bucket.Reset()
	}
}

// clientIP extracts the source address for rate limiting.
//
// X-Forwarded-For is ignored on purpose: the panel has no trusted-proxy
// configuration, so a spoofed header must not buy a fresh failure budget. If the
// panel is ever fronted by a reverse proxy, every request will look like the
// proxy address and the limiter degrades into a single global bucket — which is
// exactly what ADR-0002 P3 asks for with one administrator.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}
