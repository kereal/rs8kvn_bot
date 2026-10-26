package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Audit log repository: append-only INSERT/SELECT access to audit_log.
//
// Контракт best-effort: вызывайте через logAudit-обёртки сервисов, а не
// напрямую, чтобы ошибка журнала не роняла бизнес-операцию. Исключение —
// платёжные транзакции, где событие пишется в той же tx (InsertAuditLogInTx).

// InsertAuditLog записывает событие аудита.
func (s *Service) InsertAuditLog(ctx context.Context, e *AuditLog) error {
	if e == nil {
		return errors.New("audit log entry is nil")
	}

	err := s.db.WithContext(ctx).Create(e).Error
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}

	return nil
}

// InsertAuditLogInTx записывает событие в переданную транзакцию. Используется
// в платежных tx-флоу (ConfirmOrderPaidCAS, chargeback), где аудит обязан быть
// атомарным с изменением заказа/подписки.
func InsertAuditLogInTx(tx *gorm.DB, e *AuditLog) error {
	if e == nil {
		return errors.New("audit log entry is nil")
	}

	err := tx.Create(e).Error
	if err != nil {
		return fmt.Errorf("insert audit log in tx: %w", err)
	}

	return nil
}

// insertAuditLogBestEffort записывает событие аудита, не влияя на бизнес-флоу:
// ошибка журнала логируется как Warn. Для вызовов из пакета database, где
// сервисный logAudit-хелпер недоступен (воркеры очистки trial и т.п.).
func (s *Service) insertAuditLogBestEffort(ctx context.Context, e *AuditLog) error {
	err := s.InsertAuditLog(ctx, e)
	if err != nil {
		logger.Warn("audit log write failed",
			zap.String("event", e.Event),
			zap.Error(err))

		return err
	}

	return nil
}

// GetAuditLogByEntity возвращает историю событий сущности (новые первыми).
func (s *Service) GetAuditLogByEntity(ctx context.Context, entityType EntityType, entityID string, limit int) ([]AuditLog, error) {
	if limit <= 0 {
		limit = 100
	}

	var events []AuditLog

	err := s.db.WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?", entityType, entityID).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&events).Error
	if err != nil {
		return nil, fmt.Errorf("get audit log by entity: %w", err)
	}

	return events, nil
}

// GetAuditLogByEvent возвращает последние события заданного типа.
func (s *Service) GetAuditLogByEvent(ctx context.Context, event string, limit int) ([]AuditLog, error) {
	if limit <= 0 {
		limit = 100
	}

	var events []AuditLog

	err := s.db.WithContext(ctx).
		Where("event = ?", event).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&events).Error
	if err != nil {
		return nil, fmt.Errorf("get audit log by event: %w", err)
	}

	return events, nil
}

// GetRecentAuditLog возвращает последние события всех типов.
func (s *Service) GetRecentAuditLog(ctx context.Context, limit int) ([]AuditLog, error) {
	if limit <= 0 {
		limit = 100
	}

	var events []AuditLog

	err := s.db.WithContext(ctx).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&events).Error
	if err != nil {
		return nil, fmt.Errorf("get recent audit log: %w", err)
	}

	return events, nil
}
