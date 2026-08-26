package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// BroadcastFilter описывает фильтр получателей рассылки.
// Все поля необязательны: пустой фильтр = все активные пользователи.
type BroadcastFilter struct {
	// PlanType фильтрует по типу тарифа: "paid" (все платные) или "free" (бесплатный).
	// Пустая строка = все тарифы.
	PlanType string `json:"plan_type,omitempty"`

	// SubscriptionStatus фильтрует по статусу подписки: "active", "revoked" или
	// "all". Пустая строка = "active" (по умолчанию).
	SubscriptionStatus string `json:"subscription_status,omitempty"`

	// RegisteredAfter — пользователи, зарегистрированные после этой даты (включительно).
	RegisteredAfter *time.Time `json:"registered_after,omitempty"`

	// RegisteredBefore — пользователи, зарегистрированные до этой даты (включительно).
	RegisteredBefore *time.Time `json:"registered_before,omitempty"`

	// InactiveDays — пользователи, которые НЕ обращались к боту последние N дней.
	// 0 = никогда не обращались (last_request IS NULL).
	// > 0 = last_request < NOW() - INTERVAL 'N days'.
	// nil = без фильтра по активности.
	InactiveDays *int `json:"inactive_days,omitempty"`

	// EverPaid — фильтр по истории платежей.
	// true = только те, у кого есть хотя бы один оплаченный заказ (orders.status = 'paid').
	// nil = без фильтра по платежам.
	EverPaid *bool `json:"ever_paid,omitempty"`
}

// ParseBroadcastFilter парсит JSON-строку фильтра из broadcasts.filters.
// Пустая строка или "{}" возвращает пустой фильтр (без ограничений).
func ParseBroadcastFilter(raw string) (BroadcastFilter, error) {
	if raw == "" || raw == "{}" {
		return BroadcastFilter{}, nil
	}

	var f BroadcastFilter
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return BroadcastFilter{}, fmt.Errorf("parse broadcast filter: %w", err)
	}
	if f.SubscriptionStatus != "" && f.SubscriptionStatus != "active" && f.SubscriptionStatus != "revoked" && f.SubscriptionStatus != "all" {
		return BroadcastFilter{}, fmt.Errorf("parse broadcast filter: unsupported subscription status %q", f.SubscriptionStatus)
	}

	return f, nil
}

// String возвращает человекочитаемое описание фильтра для превью.
func (f BroadcastFilter) String() string {
	if f.IsEmpty() {
		return "Все активные пользователи"
	}

	var parts []string

	switch f.PlanType {
	case "paid":
		parts = append(parts, "Платные")
	case "free":
		parts = append(parts, "Бесплатные")
	}

	if f.SubscriptionStatus != "" && f.SubscriptionStatus != "active" {
		if f.SubscriptionStatus == "all" {
			parts = append(parts, "Все статусы")
		} else {
			parts = append(parts, "Статус: "+f.SubscriptionStatus)
		}
	}

	if f.RegisteredAfter != nil {
		parts = append(parts, "После "+f.RegisteredAfter.Format("02.01.2006"))
	}
	if f.RegisteredBefore != nil {
		parts = append(parts, "До "+f.RegisteredBefore.Format("02.01.2006"))
	}

	if f.InactiveDays != nil {
		switch {
		case *f.InactiveDays == 0:
			parts = append(parts, "Никогда не обращались")
		case *f.InactiveDays <= 30:
			parts = append(parts, fmt.Sprintf("Не активны > %d дн.", *f.InactiveDays))
		default:
			months := *f.InactiveDays / 30
			parts = append(parts, fmt.Sprintf("Не активны > %d мес.", months))
		}
	}

	if f.EverPaid != nil {
		if *f.EverPaid {
			parts = append(parts, "Когда-либо платили")
		} else {
			parts = append(parts, "Никогда не платили")
		}
	}

	return strings.Join(parts, " · ")
}

// IsEmpty возвращает true, если фильтр не задан (все пользователи).
func (f BroadcastFilter) IsEmpty() bool {
	return f.PlanType == "" && f.SubscriptionStatus == "" &&
		f.RegisteredAfter == nil && f.RegisteredBefore == nil &&
		f.InactiveDays == nil && f.EverPaid == nil
}

// MarshalJSON реализует json.Marshaler — пустой фильтр сериализуется как "{}".
func (f BroadcastFilter) MarshalJSON() ([]byte, error) {
	type Alias BroadcastFilter
	return json.Marshal((Alias)(f))
}

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
		Select("status", "finished_at", "recipients_total", "sent_count", "blocked_count", "unreachable_count", "failed_count", "last_error", "retry_at", "retry_count", "delivery_report").
		Updates(b)
	if result.Error != nil {
		return fmt.Errorf("failed to update broadcast: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return ErrBroadcastNotFound
	}

	return nil
}
