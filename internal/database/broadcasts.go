package database

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// CreateBroadcast создаёт новую рассылку.
func (s *Service) CreateBroadcast(ctx context.Context, b *Broadcast) error {
	result := s.db.WithContext(ctx).Create(b)
	if result.Error != nil {
		return fmt.Errorf("failed to create broadcast: %w", result.Error)
	}

	return nil
}

// GetBroadcast возвращает рассылку по ID.
func (s *Service) GetBroadcast(ctx context.Context, id uint) (*Broadcast, error) {
	var b Broadcast

	result := s.db.WithContext(ctx).First(&b, id)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrBroadcastNotFound
		}

		return nil, fmt.Errorf("failed to get broadcast: %w", result.Error)
	}

	return &b, nil
}

// ListBroadcasts возвращает последние limit рассылок, начиная с самых
// свежих (created_at DESC, id DESC для стабильного порядка при равных датах).
func (s *Service) ListBroadcasts(ctx context.Context, limit int) ([]Broadcast, error) {
	var broadcasts []Broadcast

	result := s.db.WithContext(ctx).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&broadcasts)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list broadcasts: %w", result.Error)
	}

	return broadcasts, nil
}

// UpdateBroadcast обновляет только изменяемые поля карточки рассылки:
// статус, время завершения, счётчики и JSON-отчёт. name/filters/message_text/
// planned_at неизменяемы после создания — обновление их не трогает, чтобы
// финализация рассылки не затирала карточку. RowsAffected == 0 возвращает
// ErrBroadcastNotFound.
func (s *Service) UpdateBroadcast(ctx context.Context, b *Broadcast) error {
	result := s.db.WithContext(ctx).
		Model(&Broadcast{}).
		Where("id = ?", b.ID).
		Select("status", "started_at", "finished_at",
			"recipients_total", "sent_count", "blocked_count", "failed_count", "delivery_report").
		Updates(b)
	if result.Error != nil {
		return fmt.Errorf("failed to update broadcast: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return ErrBroadcastNotFound
	}

	return nil
}
