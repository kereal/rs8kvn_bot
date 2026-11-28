package database

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// SyncQueueFilter — фильтры экрана «Очередь синхронизации». Zero value = все
// несинхронизированные привязки. Валидация строковых полей — в хендлере
// (белый список), здесь — только SQL.
type SyncQueueFilter struct {
	Status string // "" | pending_add | pending_update | pending_remove
	Sort   string // updated_at | retry_at | subscription_id
	Desc   bool
	Limit  int
	Offset int
}

// SyncQueueRow — одна строка очереди: конкретная пара подписка↔нода, которую
// ещё не подтвердил фоновый воркер. Читается одним SELECT'ом с JOIN'ами, N+1
// нет; last_error обрезается в хендлере.
type SyncQueueRow struct {
	SubscriptionID       uint       `gorm:"column:subscription_id"`
	TelegramID           int64      `gorm:"column:telegram_id"`
	SubscriptionIDPublic string     `gorm:"column:subscription_id_public"`
	NodeID               uint       `gorm:"column:node_id"`
	NodeName             string     `gorm:"column:node_name"`
	Status               string     `gorm:"column:status"`
	RetryCount           int        `gorm:"column:retry_count"`
	RetryAt              *time.Time `gorm:"column:retry_at"`
	LastError            *string    `gorm:"column:last_error"`
	UpdatedAt            time.Time  `gorm:"column:updated_at"`
}

// syncQueueStatuses — статусы, которые образуют очередь. active в неё не
// входит: это уже применённая привязка.
var syncQueueStatuses = []SyncStatus{
	SyncStatusPendingAdd,
	SyncStatusPendingUpdate,
	SyncStatusPendingRemove,
}

// buildSyncQueueQuery возвращает свежую цепочку с общим WHERE и JOIN'ами.
// Используется и для COUNT, и для SELECT: фильтры total и items не могут
// разъехаться. Сортировка и пагинация применяются вызывающей стороной.
func buildSyncQueueQuery(s *Service, ctx context.Context, f SyncQueueFilter) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&SubscriptionNode{}).
		Joins("JOIN subscriptions ON subscriptions.id = subscription_nodes.subscription_id").
		Joins("LEFT JOIN nodes ON nodes.id = subscription_nodes.node_id")

	if f.Status != "" {
		q = q.Where("subscription_nodes.status = ?", f.Status)
	} else {
		q = q.Where("subscription_nodes.status IN ?", syncQueueStatuses)
	}

	return q
}

// ListSyncQueue возвращает страницу очереди и общее число строк по тем же
// фильтрам. По умолчанию строки идут от самой старой записи: возраст — главный
// сигнал застрявшей синхронизации.
func (s *Service) ListSyncQueue(ctx context.Context, f SyncQueueFilter) (items []SyncQueueRow, total int64, err error) {
	err = buildSyncQueueQuery(s, ctx, f).Count(&total).Error
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count sync queue: %w", err)
	}

	sort := f.Sort
	if sort == "" {
		sort = "updated_at"
	}
	dir := "ASC"
	if f.Desc {
		dir = "DESC"
	}

	err = buildSyncQueueQuery(s, ctx, f).
		Select(`subscription_nodes.subscription_id,
			subscriptions.telegram_id,
			subscriptions.subscription_id AS subscription_id_public,
			subscription_nodes.node_id,
			COALESCE(nodes.name, '') AS node_name,
			subscription_nodes.status,
			subscription_nodes.retry_count,
			subscription_nodes.retry_at,
			subscription_nodes.last_error,
			subscription_nodes.updated_at`).
		Order("subscription_nodes." + sort + " " + dir).
		Order("subscription_nodes.subscription_id ASC").
		Order("subscription_nodes.node_id ASC").
		Limit(f.Limit).
		Offset(f.Offset).
		Scan(&items).Error
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list sync queue: %w", err)
	}

	return items, total, nil
}
