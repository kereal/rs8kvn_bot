package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

// HasPaymentConfirmedByProviderPaymentID сообщает, подтверждался ли ранее платёж
// с указанным provider_payment_id (событие payment_confirmed). Нужен авто-возврату
// Stars: charge уже исполненного заказа возвращать нельзя.
//
// provider ограничивает поиск одним провайдером: одинаковый provider_payment_id
// у другого провайдера — другой платёж, совпадение было бы ложным и авто-возврат
// Stars-платежа был бы пропущен.
//
// JSON1 в используемом SQLite (mattn/go-sqlite3) не собран, поэтому details
// ищется литеральным вхождением через instr (core-функция SQLite, без JSON1):
// фрагменты `"provider_payment_id":"..."` и `"provider":"..."` строятся через
// json.Marshal, как и marshalAuditDetails, — кавычки и экранирование совпадают
// с тем, как details реально записаны. Поиск регистрозависим (в отличие от
// LIKE): charge-иды, различающиеся только регистром, — разные платежи, и
// ложное совпадение означало бы пропущенный авто-возврат дубля оплаты.
func (s *Service) HasPaymentConfirmedByProviderPaymentID(ctx context.Context, provider string, providerPaymentID string) (bool, error) {
	chargeID := strings.TrimSpace(providerPaymentID)
	providerName := strings.TrimSpace(provider)
	if chargeID == "" || providerName == "" {
		return false, nil
	}

	chargeEncoded, err := json.Marshal(chargeID)
	if err != nil {
		return false, fmt.Errorf("has payment confirmed by provider payment id: %w", err)
	}

	providerEncoded, err := json.Marshal(providerName)
	if err != nil {
		return false, fmt.Errorf("has payment confirmed by provider payment id: %w", err)
	}

	chargeFragment := `"provider_payment_id":` + string(chargeEncoded)
	providerFragment := `"provider":` + string(providerEncoded)

	var count int64

	err = s.db.WithContext(ctx).
		Model(&AuditLog{}).
		Where("event = ?", AuditEventPaymentConfirmed).
		Where("instr(details, ?) > 0", chargeFragment).
		Where("instr(details, ?) > 0", providerFragment).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("has payment confirmed by provider payment id: %w", err)
	}

	return count > 0, nil
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

	// Delegated so the package keeps a single audit query builder.
	events, _, err := s.ListAuditLogPage(ctx, AuditLogFilter{Limit: limit, Desc: true})
	if err != nil {
		return nil, fmt.Errorf("get recent audit log: %w", err)
	}

	return events, nil
}
