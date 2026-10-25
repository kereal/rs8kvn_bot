package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"go.uber.org/zap"
)

// DeactivateBlockedBroadcastRecipients анализирует завершенную рассылку,
// находит всех пользователей, заблокировавших бота (и/или недоступных),
// переводит их подписки в статус 'revoked' (выключает без удаления)
// и записывает события в аудит-лог.
func (s *SubscriptionService) DeactivateBlockedBroadcastRecipients(ctx context.Context, broadcastID uint) (int, error) {
	_, _, blockedCount, unreachableCount, _, report, err := s.db.GetBroadcastRecipientsStats(ctx, broadcastID)
	if err != nil {
		return 0, fmt.Errorf("failed to get broadcast stats for id %d: %w", broadcastID, err)
	}

	targetTelegramIDs := make(map[int64]bool)
	for _, id := range report.Blocked {
		targetTelegramIDs[id] = true
	}
	for _, id := range report.Unreachable {
		targetTelegramIDs[id] = true
	}

	if len(targetTelegramIDs) == 0 {
		return 0, nil
	}

	deactivatedCount := 0

	for telegramID := range targetTelegramIDs {
		sub, err := s.db.GetByTelegramID(ctx, telegramID)
		if err != nil {
			if errors.Is(err, database.ErrSubscriptionNotFound) {
				continue
			}
			logger.Warn("Failed to fetch subscription for broadcast recipient deactivation",
				zap.Int64("telegram_id", telegramID),
				zap.Error(err),
			)
			continue
		}

		if sub.Status != string(database.SubscriptionStatusActive) {
			continue
		}

		sub.Status = string(database.SubscriptionStatusRevoked)
		err = s.db.UpdateSubscription(ctx, sub)
		if err != nil {
			logger.Error("Failed to revoke subscription for blocked user",
				zap.Uint("subscription_id", sub.ID),
				zap.Int64("telegram_id", telegramID),
				zap.Error(err),
			)
			continue
		}

		s.logAudit(ctx, database.AuditSubscriptionRevoked(sub, fmt.Sprintf("broadcast_%d_blocked", broadcastID)))

		if s.invalidateBySubID != nil {
			s.invalidateBySubID(sub.SubscriptionID)
		}

		logger.Info("Deactivated subscription for user who blocked bot",
			zap.Uint("subscription_id", sub.ID),
			zap.Int64("telegram_id", telegramID),
		)

		deactivatedCount++
	}

	logger.Info("Broadcast blocked-users deactivation completed",
		zap.Uint("broadcast_id", broadcastID),
		zap.Int("blocked_total", int(blockedCount+unreachableCount)),
		zap.Int("deactivated_count", deactivatedCount),
	)

	return deactivatedCount, nil
}
