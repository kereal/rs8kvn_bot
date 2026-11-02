package subserver

import (
	"context"
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
