package database

import (
	"context"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// auditRetentionBatch — размер одной пачки удаления: DELETE ограниченного
// объёма не держит write-lock на всю таблицу.
const auditRetentionBatch = 1000

// DailyMaintenance выполняет суточное обслуживание БД: обрезает журнал аудита
// по сроку хранения. Вызывается из суточного планировщика (после бэкапа).
//
// Файл БД после удаления не уменьшается: SQLite переиспользует освобождённые
// страницы. Цель — остановить линейный рост, а не сжать файл; для сжатия нужен
// ручной VACUUM (см. doc/operations.md).
func (s *Service) DailyMaintenance(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-AuditLogRetention)
	total := int64(0)

	for {
		deleted, err := s.DeleteAuditLogBefore(ctx, cutoff, auditRetentionBatch)
		if err != nil {
			return fmt.Errorf("daily maintenance: %w", err)
		}

		total += deleted

		if deleted < auditRetentionBatch {
			break
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	if total > 0 {
		logger.Info("audit log retention applied",
			zap.Int64("deleted", total),
			zap.String("cutoff", cutoff.Format(time.RFC3339)))
	}

	return nil
}
