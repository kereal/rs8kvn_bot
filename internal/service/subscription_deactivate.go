package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"go.uber.org/zap"
)

// DeactivationResult summarizes a broadcast-recipient deactivation run.
// Partial failures are reported here in addition to the aggregated error, so the
// caller can show the admin what actually happened instead of a binary result.
type DeactivationResult struct {
	// Targets — подписки в статусе active среди заблокировавших/недоступных.
	Targets int
	// Revoked — подписки, переведённые в revoked (VPN-доступ снимается).
	Revoked int
	// Failed — подписки, которые не удалось обработать (статус не изменён или
	// депровижининг не поставлен в очередь ретраев).
	Failed int
}

// DeactivateBlockedBroadcastRecipients анализирует завершенную рассылку,
// находит всех пользователей, заблокировавших бота (и/или недоступных),
// переводит их подписки в статус 'revoked' (выключает без удаления строки),
// снимает VPN-доступ через тот же путь депровижининга, что и admin /del,
// и записывает события в аудит-лог.
//
// БД-фаза выполняется синхронно для каждого получателя (статус + pending_remove),
// немедленный sync с панелями — только для первых maxImmediateSyncs подписок:
// остальные строки pending_remove снимает фоновый SyncPendingNodes, поэтому
// администратор не ждёт панель на каждом пользователе.
//
// Возвращает сводку по прогону и агрегированную ошибку частичных сбоев:
// ошибка чтения или записи отдельного пользователя не отменяет обработку
// остальных, но и не маскируется под успех.
func (s *SubscriptionService) DeactivateBlockedBroadcastRecipients(ctx context.Context, broadcastID uint) (*DeactivationResult, error) {
	total, sent, blockedCount, unreachableCount, failedCount, report, err := s.db.GetBroadcastRecipientsStats(ctx, broadcastID)
	if err != nil {
		return nil, fmt.Errorf("failed to get broadcast stats for id %d: %w", broadcastID, err)
	}

	targetTelegramIDs := make(map[int64]bool)
	for _, id := range report.Blocked {
		targetTelegramIDs[id] = true
	}
	for _, id := range report.Unreachable {
		targetTelegramIDs[id] = true
	}

	result := &DeactivationResult{}

	if len(targetTelegramIDs) == 0 {
		return result, nil
	}

	reason := fmt.Sprintf("broadcast_%d_blocked", broadcastID)
	var errs []error

	// Внешний sync (обращения к панелям) ограничен по числу подписок за прогон:
	// панель может отвечать медленно, а callback администратора не должен
	// висеть минуты. Для всех остальных привязки уже pending_remove — фоновый
	// SyncPendingNodes снимет клиентов в пределах своего тика.
	const maxImmediateSyncs = 25

	var queued []uint

	for telegramID := range targetTelegramIDs {
		sub, err := s.db.GetByTelegramID(ctx, telegramID)
		if err != nil {
			if errors.Is(err, database.ErrSubscriptionNotFound) {
				continue
			}

			result.Failed++
			errs = append(errs, fmt.Errorf("get subscription for telegram %d: %w", telegramID, err))

			continue
		}

		if sub.Status != string(database.SubscriptionStatusActive) {
			continue
		}

		result.Targets++

		err = s.markRevokedSub(ctx, sub)
		if err != nil {
			result.Failed++
			errs = append(errs, fmt.Errorf("revoke subscription %d (telegram %d): %w", sub.ID, telegramID, err))

			continue
		}

		result.Revoked++

		// Статус изменён — событие фиксируется сразу, до внешних вызовов:
		// даже если депровижининг ниже упадёт, отзыв уже состоявшийся факт.
		s.logAudit(ctx, database.AuditSubscriptionRevoked(sub, reason))

		if sub.SubscriptionID != "" {
			s.InvalidateBySubID(ctx, sub.SubscriptionID)
		}

		err = s.queueForDeprovision(ctx, sub.ID)
		if err != nil {
			// Подписка уже revoked (subserver отдаёт 404), но привязки не
			// помечены pending_remove — фоновой синхронизации нечего повторять,
			// поэтому сбой возвращается администратору.
			result.Failed++
			errs = append(errs, fmt.Errorf("deprovision subscription %d (telegram %d): %w", sub.ID, telegramID, err))

			continue
		}

		queued = append(queued, sub.ID)

		logger.Info("Deactivated subscription for user who blocked bot",
			zap.Uint("subscription_id", sub.ID),
			zap.Int64("telegram_id", telegramID),
		)
	}

	for i := 0; i < len(queued) && i < maxImmediateSyncs; i++ {
		s.syncDeprovisionNow(ctx, queued[i])
	}
	if len(queued) > maxImmediateSyncs {
		logger.Info("Broadcast deactivation: remaining removals left to the background sync worker",
			zap.Uint("broadcast_id", broadcastID),
			zap.Int("queued", len(queued)),
			zap.Int("immediate", maxImmediateSyncs))
	}

	logger.Info("Broadcast blocked-users deactivation completed",
		zap.Uint("broadcast_id", broadcastID),
		zap.Int64("recipients_total", total),
		zap.Int64("delivered", sent),
		zap.Int("blocked_total", int(blockedCount+unreachableCount)),
		zap.Int64("unreachable", unreachableCount),
		zap.Int64("failed", failedCount),
		zap.Int("targets", result.Targets),
		zap.Int("revoked", result.Revoked),
		zap.Int("failed_deactivations", result.Failed),
	)

	if result.Revoked > 0 {
		s.RefreshActiveSubscriptionsMetric(ctx)
	}

	return result, errors.Join(errs...)
}
