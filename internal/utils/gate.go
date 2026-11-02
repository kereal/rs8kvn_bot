package utils

import (
	"sync"
	"time"
)

// IntervalGate сообщает, можно ли обработать ключ сейчас: не чаще одного раза
// за интервал. Хранит только время последнего пропуска, поэтому карту надо
// периодически чистить — это делает Allow при достижении pruneAt.
type IntervalGate struct {
	mu      sync.Mutex
	seen    map[string]time.Time
	every   time.Duration
	pruneAt int
}

// NewIntervalGate создаёт гейт с интервалом every и порогом очистки pruneAt
// (0 отключает очистку).
func NewIntervalGate(every time.Duration, pruneAt int) *IntervalGate {
	return &IntervalGate{every: every, pruneAt: pruneAt}
}

// Allow сообщает, разрешён ли ключ в момент now, и запоминает пропуск.
func (g *IntervalGate) Allow(key string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.seen == nil {
		g.seen = make(map[string]time.Time)
	}

	if last, ok := g.seen[key]; ok && now.Sub(last) < g.every {
		return false
	}

	if g.pruneAt > 0 && len(g.seen) >= g.pruneAt {
		for k, seenAt := range g.seen {
			if now.Sub(seenAt) >= g.every {
				delete(g.seen, k)
			}
		}
		if len(g.seen) >= g.pruneAt {
			clear(g.seen)
		}
	}

	g.seen[key] = now

	return true
}
