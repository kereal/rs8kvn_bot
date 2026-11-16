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
	err := json.Unmarshal([]byte(raw), &f)
	if err != nil {
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

// Sentinel-ошибки идемпотентного создания рассылки (миграция 042).
var (
	// ErrBroadcastIdempotencyConflict — тот же Idempotency-Key уже занят
	// рассылкой с другим payload (name, message_text, filters, planned_at).
	ErrBroadcastIdempotencyConflict = errors.New("idempotency key already used with a different broadcast payload")
	// ErrBroadcastIdempotencyReplay — повтор создания с тем же ключом и тем
	// же payload: существующая рассылка возвращается без новой строки.
	ErrBroadcastIdempotencyReplay = errors.New("broadcast with this idempotency key already exists")
)

// BroadcastIdempotencyConflictError несёт рассылку, занявшую ключ, чтобы
// HTTP-слой отдал 409 с её id (§3).
type BroadcastIdempotencyConflictError struct {
	Existing Broadcast
}

func (e *BroadcastIdempotencyConflictError) Error() string {
	return fmt.Sprintf("%s: existing broadcast id %d", ErrBroadcastIdempotencyConflict, e.Existing.ID)
}

func (e *BroadcastIdempotencyConflictError) Unwrap() error { return ErrBroadcastIdempotencyConflict }

// BroadcastIdempotencyReplayError несёт существующую рассылку: повтор с тем
// же ключом и payload возвращает её как есть, без второй строки и без
// второго audit-события.
type BroadcastIdempotencyReplayError struct {
	Existing Broadcast
}

func (e *BroadcastIdempotencyReplayError) Error() string {
	return fmt.Sprintf("%s: existing broadcast id %d", ErrBroadcastIdempotencyReplay, e.Existing.ID)
}

func (e *BroadcastIdempotencyReplayError) Unwrap() error { return ErrBroadcastIdempotencyReplay }

// CreateBroadcast создаёт новую рассылку.
//
// Если b.IdempotencyKey задан (панель), создание идемпотентно: тот же ключ
// с идентичным payload возвращает существующую строку как
// *BroadcastIdempotencyReplayError, тот же ключ с другим payload —
// *BroadcastIdempotencyConflictError. Гонка между pre-check и insert
// закрывается повторным lookup'ом после ошибки вставки: ключ не найден —
// возвращается исходная ошибка вставки, найден — классификация по payload.
// Без ключа — обычное создание (NULL в БД).
func (s *Service) CreateBroadcast(ctx context.Context, b *Broadcast) error {
	key := ""
	if b.IdempotencyKey != nil {
		key = strings.TrimSpace(*b.IdempotencyKey)
	}
	if key == "" {
		return s.insertBroadcast(ctx, b)
	}

	err := s.resolveIdempotency(ctx, b, key)
	if err != nil {
		return err
	}

	err = s.insertBroadcast(ctx, b)
	if err == nil {
		return nil
	}

	// Вставка провалилась (в том числе гонка на уникальном индексе
	// idx_broadcasts_idempotency_key): перечитываем ключ и классифицируем
	// заново. Строку с ключом уже удалили — resolveIdempotency вернёт nil,
	// тогда отдаём исходную ошибку вставки как есть.
	resolveErr := s.resolveIdempotency(ctx, b, key)
	if resolveErr != nil {
		return resolveErr
	}

	return err
}

// ResolveBroadcastCreate классифицирует ключ идемпотентности до guard'а
// аудитории: replay/conflict не должны зависеть от текущего состава
// аудитории (§4 — аудитория могла опустеть после первого создания).
// nil — ключ свободен, можно создавать.
func (s *Service) ResolveBroadcastCreate(ctx context.Context, b *Broadcast) error {
	key := ""
	if b.IdempotencyKey != nil {
		key = strings.TrimSpace(*b.IdempotencyKey)
	}
	if key == "" {
		return nil
	}

	return s.resolveIdempotency(ctx, b, key)
}

// insertBroadcast выполняет саму вставку строки рассылки.
func (s *Service) insertBroadcast(ctx context.Context, b *Broadcast) error {
	err := s.db.WithContext(ctx).Create(b).Error
	if err != nil {
		return fmt.Errorf("failed to create broadcast: %w", err)
	}

	return nil
}

// resolveIdempotency ищет рассылку с этим ключом и классифицирует исход:
// nil — ключ свободен, replay — payload совпал, conflict — различается.
func (s *Service) resolveIdempotency(ctx context.Context, b *Broadcast, key string) error {
	existing, err := s.findBroadcastByIdempotencyKey(ctx, key)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	if broadcastPayloadEqual(existing, b) {
		return &BroadcastIdempotencyReplayError{Existing: *existing}
	}

	return &BroadcastIdempotencyConflictError{Existing: *existing}
}

// findBroadcastByIdempotencyKey возвращает nil без ошибки, если ключ свободен.
func (s *Service) findBroadcastByIdempotencyKey(ctx context.Context, key string) (*Broadcast, error) {
	var existing Broadcast

	err := s.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup broadcast by idempotency key: %w", err)
	}

	return &existing, nil
}

// broadcastPayloadEqual сравнивает поля, составляющие payload создания.
func broadcastPayloadEqual(a, b *Broadcast) bool {
	if a.Name != b.Name || a.MessageText != b.MessageText || a.Filters != b.Filters {
		return false
	}

	switch {
	case a.PlannedAt == nil && b.PlannedAt == nil:
		return true
	case a.PlannedAt == nil || b.PlannedAt == nil:
		return false
	default:
		return a.PlannedAt.Equal(*b.PlannedAt)
	}
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
