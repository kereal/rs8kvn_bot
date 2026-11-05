package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

// InviteOrigin помечает в аудите, откуда взят код: сгенерирован сервером или
// введён администратором вручную.
type InviteOrigin string

const (
	InviteOriginGenerated InviteOrigin = "generated"
	InviteOriginManual    InviteOrigin = "manual"
)

// inviteCodeRe — формат кода, который бот подставляет в ссылки
// t.me/{bot}?start=share_{code} и %s/i/{code}: строго lowercase [a-z0-9],
// длина 6–16. Код вне формата ломал бы генерацию ботовых ссылок.
var inviteCodeRe = regexp.MustCompile(`^[a-z0-9]{6,16}$`)

// InviteOverview — инвайт со счётчиками использования для списка и guard'а
// удаления: SubscriptionsCount > 0 означает, что код уже использован.
type InviteOverview struct {
	Invite

	ReferredCount      int64
	SubscriptionsCount int64
}

// ListInviteOverviews возвращает все инвайты с двумя счётчиками одним запросом
// (без N+1): referred_count — привязанные рефералы (subscriptions.referred_by),
// subscriptions_count — подписки, созданные по коду (subscriptions.invite_code).
// Счётчики независимы: trial без привязки пишет invite_code, но не referred_by.
func (s *Service) ListInviteOverviews(ctx context.Context) ([]InviteOverview, error) {
	var rows []InviteOverview

	err := s.db.WithContext(ctx).
		Model(&Invite{}).
		Select(`invites.code, invites.referrer_tg_id, invites.created_at,
			COALESCE(ref.referred_count, 0) AS referred_count,
			COALESCE(subs.subscriptions_count, 0) AS subscriptions_count`).
		Joins(`LEFT JOIN (
			SELECT referred_by, COUNT(*) AS referred_count FROM subscriptions
			WHERE referred_by IS NOT NULL GROUP BY referred_by
		) ref ON ref.referred_by = invites.referrer_tg_id`).
		Joins(`LEFT JOIN (
			SELECT invite_code, COUNT(*) AS subscriptions_count FROM subscriptions
			WHERE invite_code IS NOT NULL GROUP BY invite_code
		) subs ON subs.invite_code = invites.code`).
		Order("invites.created_at DESC, invites.code ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list invite overviews: %w", err)
	}

	return rows, nil
}

// CreateInviteAdmin создаёт инвайт от имени администратора.
//
// origin ("generated" | "manual") попадает в аудит: панель различает
// сгенерированные и введённые вручную коды. Репозиторий не генерирует коды —
// он получает готовый код и валидирует его тем же правилом, что и бот
// (см. inviteCodeRe), плюс referrer_tg_id > 0.
//
// Уникальность кода (PK) и referrer_tg_id (индекс из миграции 004) держит БД;
// предварительные SELECT'ы дают понятный 409, INSERT-конфликт из-за гонки
// маппится в тот же sentinel.
func (s *Service) CreateInviteAdmin(ctx context.Context, invite *Invite, origin InviteOrigin) error {
	if invite == nil {
		return fmt.Errorf("create invite: %w", ErrInviteInvalid)
	}

	if !inviteCodeRe.MatchString(invite.Code) {
		return fmt.Errorf("invite code must match ^[a-z0-9]{6,16}$: %w", ErrInviteInvalid)
	}

	if invite.ReferrerTGID <= 0 {
		return fmt.Errorf("referrer_tg_id must be positive: %w", ErrInviteInvalid)
	}

	var codeOwner Invite

	err := s.db.WithContext(ctx).Where("code = ?", invite.Code).First(&codeOwner).Error
	switch {
	case err == nil:
		return &InviteNameTakenError{Details: map[string]string{"field": "code"}}
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return fmt.Errorf("check invite code: %w", err)
	}

	var referrerOwner Invite

	err = s.db.WithContext(ctx).
		Where("referrer_tg_id = ?", invite.ReferrerTGID).
		First(&referrerOwner).Error
	switch {
	case err == nil:
		return &InviteNameTakenError{Details: map[string]string{"field": "referrer_tg_id"}}
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return fmt.Errorf("check invite referrer: %w", err)
	}

	err = s.db.WithContext(ctx).Create(invite).Error
	if err != nil {
		return classifyInviteWriteError("create invite", err)
	}

	_ = s.insertAuditLogBestEffort(ctx, AuditInviteCreated(invite, origin))

	return nil
}

// DeleteInvite удаляет неиспользованный инвайт и возвращает удалённую строку.
//
// Полей «активен/отозван» в схеме нет (см. ТЗ 03 §3), поэтому использованный
// код защищён физически: существует subscriptions.invite_code = code — удаление
// запрещено, ботовые ссылки на этот код продолжают работать.
func (s *Service) DeleteInvite(ctx context.Context, code string) (Invite, error) {
	var invite Invite

	err := s.db.WithContext(ctx).Where("code = ?", code).First(&invite).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Invite{}, fmt.Errorf("invite %q: %w", code, ErrInviteNotFound)
		}

		return Invite{}, fmt.Errorf("load invite for delete: %w", err)
	}

	var used int64

	err = s.db.WithContext(ctx).
		Model(&Subscription{}).
		Where("invite_code = ?", code).
		Count(&used).Error
	if err != nil {
		return Invite{}, fmt.Errorf("count invite subscriptions: %w", err)
	}

	if used > 0 {
		return Invite{}, &InviteInUseError{Details: map[string]int64{"subscriptions": used}}
	}

	err = s.db.WithContext(ctx).Where("code = ?", code).Delete(&Invite{}).Error
	if err != nil {
		return Invite{}, fmt.Errorf("delete invite: %w", err)
	}

	_ = s.insertAuditLogBestEffort(ctx, AuditInviteDeleted(&invite))

	return invite, nil
}

// classifyInviteWriteError маппит UNIQUE-нарушение из-за гонки на
// ErrInviteNameTaken: текст ошибки SQLite содержит имя колонки, по которой
// случился конфликт (образец — classifyPlanWriteError).
func classifyInviteWriteError(op string, err error) error {
	if err == nil {
		return nil
	}

	text := err.Error()
	if !strings.Contains(text, "UNIQUE constraint failed") || !strings.Contains(text, "invites.") {
		return fmt.Errorf("%s: %w", op, err)
	}

	field := "code"
	if strings.Contains(text, "invites.referrer_tg_id") {
		field = "referrer_tg_id"
	}

	return fmt.Errorf("%s: %w", op, &InviteNameTakenError{Details: map[string]string{"field": field}})
}
