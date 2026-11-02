package subserver

import (
	"context"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// LastRequestWriteInterval — минимальный интервал между записями last_request
// одной подписки. Клиенты перезапрашивают конфиг синхронно (несколько устройств
// после истечения кэша), и каждая такая запись сериализуется на единственном
// писателе SQLite. Пять минут сохраняют смысл поля («когда клиент последний раз
// обращался») и снимают запись с горячего пути чтения кэша.
const LastRequestWriteInterval = 5 * time.Minute

// touchPruneThreshold — при каком размере карты троттлинга чистим устаревшие
// записи, чтобы она не росла вместе с числом подписчиков за всё время жизни
// процесса.
const touchPruneThreshold = 4096

// lastRequestThrottle remembers the last write time per subscription.
type lastRequestThrottle struct {
	mu      sync.Mutex
	seen    map[string]time.Time
	every   time.Duration
	pruneAt int
}

// Allow reports whether a write for key is allowed now and records it.
func (t *lastRequestThrottle) Allow(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.seen == nil {
		t.seen = make(map[string]time.Time)
	}

	if last, ok := t.seen[key]; ok && now.Sub(last) < t.every {
		return false
	}

	if t.pruneAt > 0 && len(t.seen) >= t.pruneAt {
		for k, seenAt := range t.seen {
			if now.Sub(seenAt) >= t.every {
				delete(t.seen, k)
			}
		}
	}

	t.seen[key] = now

	return true
}

// touchLastRequest обновляет last_request с троттлингом. Ошибка записи не
// блокирует выдачу подписки: поле аналитическое.
func (s *Service) touchLastRequest(ctx context.Context, db interfaces.SubscriptionRepository, subID string) {
	if !s.lastRequest.Allow(subID, time.Now()) {
		return
	}

	err := db.UpdateLastRequest(ctx, subID)
	if err != nil {
		logger.Warn("Failed to update last_request",
			zap.String("sub_id", subID),
			zap.Error(err))
	}
}
