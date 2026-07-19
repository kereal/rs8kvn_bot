package service

import (
	"context"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
)

// TrafficInfo holds the display-oriented view of a subscription's traffic usage.
// It carries already-formatted strings so callers can render a Telegram message
// without re-deriving presentation logic.
type TrafficInfo struct {
	UsedGB             float64
	LimitGB            int
	Percentage         float64
	ProgressBar        string
	DaysUntilReset     int
	ResetInfo          string
	CreatedAtFormatted string
	ExpiresAtFormatted string
	PlanName           string
}

// PlanTrafficLimitGB returns the traffic limit in GB for the user's current plan.
// Best-effort: DB failures are logged and fall back to 0 (unlimited) rather than
// surfaced to the user.
func (s *SubscriptionService) PlanTrafficLimitGB(ctx context.Context, telegramID int64) int {
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		logger.Warn("PlanTrafficLimitGB: failed to load subscription", zap.Int64("telegram_id", telegramID), zap.Error(err))
		return 0
	}
	if sub == nil {
		return 0
	}
	plan, planErr := s.db.GetPlanByID(ctx, sub.PlanID)
	if planErr != nil {
		logger.Warn("PlanTrafficLimitGB: failed to load plan, reporting unlimited", zap.Uint("plan_id", sub.PlanID), zap.Error(planErr))
		return 0
	}
	return planTrafficLimitGB(plan)
}

// planTrafficLimitGB derives the traffic limit in GB from an already-fetched plan.
func planTrafficLimitGB(plan *database.Plan) int {
	if plan == nil {
		return 0
	}
	return int(float64(plan.TrafficLimit) / 1024 / 1024 / 1024)
}

// Получаем данные подписки, содержащие информацию о трафике
func (s *SubscriptionService) GetWithTraffic(ctx context.Context, telegramID int64) (*database.Subscription, *TrafficInfo, error) {
	// получили подписку
	sub, err := s.db.GetByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, nil, err
	}

	// Получаем план один раз и переиспользуем для лимита и названия.
	plan, planErr := s.db.GetPlanByID(ctx, sub.PlanID)
	limitGB := planTrafficLimitGB(plan)
	var planName string
	if planErr == nil && plan != nil {
		planName = plan.Name
	}

	if sub.ProductID != nil && *sub.ProductID != 0 {
		product, productErr := s.db.GetProductByID(ctx, *sub.ProductID)
		if productErr == nil && product != nil && product.Name != "" {
			planName = product.Name
		}
	}

	// Если лимит трафика нулевой — не опрашиваем серверы
	if limitGB == 0 {
		return sub, &TrafficInfo{
			UsedGB:             0,
			LimitGB:            0,
			PlanName:           planName,
			CreatedAtFormatted: utils.FormatDateRu(sub.CreatedAt),
			ExpiresAtFormatted: formatExpiresAt(sub.ExpiresAt),
		}, nil
	}

	email := XUIEmail(sub.Username, sub.TelegramID)

	// Опрашиваем только ноды, на которых подписка фактически активна (active в subscription_nodes).
	// Это исключает ноды других планов и ноды в состоянии pending_*.
	subNodes, err := s.db.GetBySubscriptionID(ctx, sub.ID)
	if err != nil {
		logger.Warn("GetWithTraffic: failed to load subscription nodes, falling back to all active nodes",
			zap.Uint("subscription_id", sub.ID),
			zap.Error(err))
		subNodes = nil
	}
	activeSubNodeIDs := make(map[uint]struct{}, len(subNodes))
	for _, sn := range subNodes {
		if sn.Status == database.SyncStatusActive {
			activeSubNodeIDs[sn.NodeID] = struct{}{}
		}
	}

	var totalUp, totalDown int64
	var anySuccess bool
	var panelResetExpiry int64
	var panelResetDays int
	for _, node := range s.activeNodes() {
		if len(activeSubNodeIDs) > 0 {
			if _, ok := activeSubNodeIDs[node.ID]; !ok {
				continue
			}
		}
		client, ok := s.xuiClients[node.ID]
		if !ok {
			continue
		}
		traffic, err := client.GetClientTraffic(ctx, email)
		if err != nil {
			logger.Debug("GetClientTraffic failed on source",
				zap.Uint("node_id", node.ID),
				zap.Error(err))
			continue
		}
		totalUp += traffic.Up
		totalDown += traffic.Down
		panelResetExpiry = traffic.ExpiresAt
		panelResetDays = traffic.Reset
		anySuccess = true
	}

	// не получилось опросить серверы
	if !anySuccess {
		return sub, &TrafficInfo{
			UsedGB:             0,
			LimitGB:            limitGB,
			Percentage:         0,
			ProgressBar:        utils.GenerateProgressBar(0, float64(limitGB)),
			CreatedAtFormatted: utils.FormatDateRu(sub.CreatedAt),
			ExpiresAtFormatted: formatExpiresAt(sub.ExpiresAt),
			PlanName:           planName,
		}, nil
	}

	usedGB := float64(totalUp+totalDown) / 1024 / 1024 / 1024
	percentage := 0.0

	if limitGB > 0 {
		percentage = (usedGB / float64(limitGB)) * 100
		if percentage > 100 {
			percentage = 100
		}
	}

	// Progress bar
	progressBar := utils.GenerateProgressBar(usedGB, float64(limitGB))

	// Calculate reset time from panel: expiryTime + reset days
	var resetInfo string
	var daysUntilReset int
	if panelResetExpiry > 0 && panelResetDays > 0 {
		resetTime := time.UnixMilli(panelResetExpiry)
		daysUntilReset = utils.DaysUntilReset(time.Now(), resetTime)
		var resetText string
		switch {
		case daysUntilReset < 0:
			resetText = "отключен"
		case daysUntilReset == 0:
			resetText = "сегодня"
		default:
			resetText = fmt.Sprintf("через %d дн.", daysUntilReset)
		}
		resetInfo = resetText
	}

	return sub, &TrafficInfo{
		UsedGB:             usedGB,
		LimitGB:            limitGB,
		Percentage:         percentage,
		ProgressBar:        progressBar,
		DaysUntilReset:     daysUntilReset,
		ResetInfo:          resetInfo,
		CreatedAtFormatted: utils.FormatDateRu(sub.CreatedAt),
		ExpiresAtFormatted: formatExpiresAt(sub.ExpiresAt),
		PlanName:           planName,
	}, nil
}

// formatExpiresAt formats ExpiresAt for display. NULL = "бессрочно".
func formatExpiresAt(t *time.Time) string {
	if t == nil {
		return "бессрочно"
	}
	return utils.FormatDateRu(*t)
}
