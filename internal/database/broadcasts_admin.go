package database

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Guard-предметы экрана «Рассылки» (doc/specs/admin/06-broadcasts.md §5):
// правка черновика, удаление и повтор живут только на статусных переходах,
// поэтому каждый отказ выражается sentinel-ошибкой.

var (
	// ErrBroadcastInvalid — черновик рассылки не прошёл валидацию полей
	// (название, текст, фильтры). Детали поля — в *BroadcastFieldError.
	ErrBroadcastInvalid = errors.New("broadcast draft is invalid")
	// ErrBroadcastLocked — операция запрещена текущим статусом рассылки
	// (running нельзя править и удалять, повтор — только для failed).
	ErrBroadcastLocked = errors.New("broadcast status does not allow the operation")
	// ErrBroadcastAudienceEmpty — фильтр не выбрал ни одного получателя.
	ErrBroadcastAudienceEmpty = errors.New("broadcast audience is empty")
)

// BroadcastFieldError указывает поле черновика, не прошедшее валидацию,
// чтобы HTTP-слой отдал details вида {"name": "…"} (ADR-0002 P9).
type BroadcastFieldError struct {
	Field  string
	Reason string
}

func (e *BroadcastFieldError) Error() string {
	return "invalid broadcast draft: " + e.Field + ": " + e.Reason
}

// Unwrap связывает ошибку поля с sentinel'ом ErrBroadcastInvalid.
func (e *BroadcastFieldError) Unwrap() error { return ErrBroadcastInvalid }

func invalidBroadcastField(field, reason string) error {
	return &BroadcastFieldError{Field: field, Reason: reason}
}

// BroadcastOverview — карточка рассылки для списка и предпросмотра:
// модель плюс вычисленные из JSON-полей флаги (снапшот снят, число ошибок).
type BroadcastOverview struct {
	Broadcast
	SnapshotTaken bool
	ErrorCount    int64
}

// ListBroadcastOverviews возвращает страницу рассылок (id DESC — история
// растёт, новее сверху) с общим числом после фильтра по статусу.
// status "" = без фильтра. SnapshotTaken/ErrorCount вычисляются из
// recipients_state/delivery_report каждой строки; повреждённый JSON не
// роняет список — поля просто остаются нулевыми.
func (s *Service) ListBroadcastOverviews(ctx context.Context, status string, limit, offset int) ([]BroadcastOverview, int64, error) {
	q := s.db.WithContext(ctx).Model(&Broadcast{})
	if status != "" {
		q = q.Where("status = ?", status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count broadcasts: %w", err)
	}

	var rows []Broadcast
	err := q.Order("id DESC").Limit(limit).Offset(offset).Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list broadcasts: %w", err)
	}

	overviews := make([]BroadcastOverview, 0, len(rows))
	for i := range rows {
		overview := BroadcastOverview{Broadcast: rows[i]}
		if state, err := parseBroadcastRecipientState(&rows[i]); err == nil {
			overview.SnapshotTaken = state.Snapshot
		}
		if report, err := rows[i].ParseDeliveryReport(); err == nil {
			overview.ErrorCount = int64(len(report.Errors))
		}
		overviews = append(overviews, overview)
	}

	return overviews, total, nil
}

// CountBroadcastAudience считает аудиторию фильтра. Запрос переиспользует
// broadcastAudienceQuery — тот же предикат, что и снапшот получателей,
// поэтому предпросмотр в панели не может разойтись с фактической рассылкой.
func (s *Service) CountBroadcastAudience(ctx context.Context, f BroadcastFilter) (int64, error) {
	var count int64

	err := s.broadcastAudienceQuery(ctx, f).Distinct("telegram_id").Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count broadcast audience: %w", err)
	}

	return count, nil
}

// ValidateBroadcastDraft проверяет поля черновика: название 1..255 символов,
// текст 1..4096 символов (лимит Telegram, бот режет длинный текст кусками),
// значения фильтров из белого списка. Нарушение — *BroadcastFieldError
// (errors.Is → ErrBroadcastInvalid).
func ValidateBroadcastDraft(b *Broadcast) error {
	if b == nil {
		return fmt.Errorf("validate broadcast draft: %w", ErrBroadcastInvalid)
	}

	name := countRunes(b.Name)
	if name == 0 || name > 255 {
		return invalidBroadcastField("name", "название обязательно, не длиннее 255 символов")
	}

	textLen := countRunes(b.MessageText)
	if textLen == 0 || textLen > 4096 {
		return invalidBroadcastField("message_text", "текст обязателен, не длиннее 4096 символов")
	}

	f, err := ParseBroadcastFilter(b.Filters)
	if err != nil {
		return invalidBroadcastField("filters", "неизвестное значение фильтра")
	}
	switch f.PlanType {
	case "", "paid", "free":
	default:
		return invalidBroadcastField("filters", "plan_type: допускаются paid и free")
	}

	return nil
}

// UpdateBroadcastDraft правит черновик: name, filters, message_text,
// planned_at, updated_at — и только пока status = 'scheduled'. Запущенную
// или завершённую рассылку править нельзя (ErrBroadcastLocked).
// UpdateBroadcast для этого не годится: он селектит другой набор колонок.
func (s *Service) UpdateBroadcastDraft(ctx context.Context, b *Broadcast) error {
	if b == nil || b.ID == 0 {
		return fmt.Errorf("update broadcast draft: %w", ErrBroadcastNotFound)
	}

	current, err := s.GetBroadcast(ctx, b.ID)
	if err != nil {
		return err
	}

	if current.Status != string(BroadcastStatusScheduled) {
		return fmt.Errorf("update broadcast draft %d (status %s): %w", b.ID, current.Status, ErrBroadcastLocked)
	}

	if err := ValidateBroadcastDraft(b); err != nil {
		return err
	}

	err = s.db.WithContext(ctx).Model(&Broadcast{}).Where("id = ?", b.ID).Updates(map[string]any{
		"name":         b.Name,
		"filters":      b.Filters,
		"message_text": b.MessageText,
		"planned_at":   b.PlannedAt,
		"updated_at":   time.Now().UTC(),
	}).Error
	if err != nil {
		return fmt.Errorf("update broadcast draft: %w", err)
	}

	return nil
}

// DeleteBroadcast удаляет строку рассылки. running удалять нельзя: воркер
// держит кампанию и пишет в неё счётчики (ErrBroadcastLocked); завершённые
// и отменённые удаляются вместе со своим отчётом.
func (s *Service) DeleteBroadcast(ctx context.Context, id uint) error {
	if id == 0 {
		return fmt.Errorf("delete broadcast: %w", ErrBroadcastNotFound)
	}

	current, err := s.GetBroadcast(ctx, id)
	if err != nil {
		return err
	}

	if current.Status == string(BroadcastStatusRunning) {
		return fmt.Errorf("delete broadcast %d: %w", id, ErrBroadcastLocked)
	}

	result := s.db.WithContext(ctx).Delete(&Broadcast{}, id)
	if result.Error != nil {
		return fmt.Errorf("delete broadcast: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return ErrBroadcastNotFound
	}

	return nil
}

// ResetBroadcastRetry возвращает провалившуюся рассылку в расписание:
// status='scheduled', retry_at=NULL. last_error сознательно сохраняется —
// администратор должен видеть, почему рассылка провалилась. Допустимо
// только для failed, иначе ErrBroadcastLocked.
func (s *Service) ResetBroadcastRetry(ctx context.Context, id uint, now time.Time) error {
	if id == 0 {
		return fmt.Errorf("reset broadcast retry: %w", ErrBroadcastNotFound)
	}

	current, err := s.GetBroadcast(ctx, id)
	if err != nil {
		return err
	}

	if current.Status != string(BroadcastStatusFailed) {
		return fmt.Errorf("reset broadcast retry %d (status %s): %w", id, current.Status, ErrBroadcastLocked)
	}

	err = s.db.WithContext(ctx).Model(&Broadcast{}).Where("id = ?", id).Updates(map[string]any{
		"status":     string(BroadcastStatusScheduled),
		"retry_at":   nil,
		"updated_at": now.UTC(),
	}).Error
	if err != nil {
		return fmt.Errorf("reset broadcast retry: %w", err)
	}

	return nil
}

// countRunes — длина строки в символах (лимиты Telegram считаются символами,
// не байтами).
func countRunes(s string) int { return len([]rune(s)) }
