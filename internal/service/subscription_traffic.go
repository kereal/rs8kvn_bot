package service

import (
	"context"
	"errors"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	"go.uber.org/zap"
)

// TrafficInfo holds the display-oriented view of a subscription's traffic usage.
// It carries already-formatted strings so callers can render a Telegram message
// without re-deriving presentation logic.
type TrafficInfo struct {
	UsedGB      float64
	LimitGB     int
	Percentage  float64
	ProgressBar string
	ResetInfo   string
	// Available reports that the traffic figure is actually measured: at least
	// one panel read succeeded. False — no reader on any active node, or all
	// reads failed — the card shows an honest "измерение недоступно" line
	// instead of a misleading 0 ГБ.
	Available          bool
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

// GetWithTraffic returns subscription data including traffic information.
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

	// Если лимит трафика нулевой — не опрашиваем серверы: сброс трафика ни на
	// что не влияет, поэтому профиль панели не читается (best-effort договорённость).
	if limitGB == 0 {
		return sub, &TrafficInfo{
			UsedGB:             0,
			LimitGB:            0,
			ResetInfo:          resetNeverText,
			PlanName:           planName,
			CreatedAtFormatted: utils.FormatDateRu(sub.CreatedAt),
			ExpiresAtFormatted: formatExpiresAt(sub.ExpiresAt),
		}, nil
	}

	email := XUIEmail(sub.Username, sub.TelegramID)

	// Опрашиваем только ноды, на которых подписка фактически активна (active в subscription_nodes).
	// Это исключает ноды других планов и ноды в состоянии pending_*.
	activeSubNodeIDs, nodeErr := s.activeBindingNodeIDs(ctx, sub.ID)
	if nodeErr != nil {
		logger.Warn("GetWithTraffic: failed to load subscription nodes, falling back to all active nodes",
			zap.Uint("subscription_id", sub.ID),
			zap.Error(nodeErr))

		activeSubNodeIDs = nil
	}

	var (
		totalUp, totalDown int64
		anySuccess         bool
		profileSource      readerTarget
		panelTotal         int64
	)

	readers := s.activeReaders(activeSubNodeIDs, email)

	// Все активные привязки на узлах без читателя (proxman, fetch): трафик
	// измерить нечем. Карточка показывает честное «недоступен», а не вводящие
	// в заблуждение 0 ГБ. Пустой набор активных привязок (подписка нигде не
	// активна) и деградация чтения БД сохраняют прежнее поведение.
	if len(activeSubNodeIDs) > 0 && len(readers) == 0 {
		return sub, &TrafficInfo{
			LimitGB:            limitGB,
			ResetInfo:          resetUnknownText,
			Available:          false,
			CreatedAtFormatted: utils.FormatDateRu(sub.CreatedAt),
			ExpiresAtFormatted: formatExpiresAt(sub.ExpiresAt),
			PlanName:           planName,
		}, nil
	}

	for _, target := range readers {
		traffic, err := target.reader.GetClientTraffic(ctx, email)
		if err != nil {
			logger.Debug("GetClientTraffic failed on source",
				zap.Uint("node_id", target.nodeID),
				zap.Error(err))

			continue
		}

		totalUp += traffic.Up
		totalDown += traffic.Down
		anySuccess = true
		if profileSource.reader == nil {
			profileSource = target
			// Панельный лимит (traffic.Total в байтах) берём с того же
			// источника, что и профиль сброса, чтобы карточка была согласована.
			panelTotal = traffic.Total
		}
	}

	// не получилось опросить серверы: читатели есть, но ответили ошибкой —
	// трафик существует, просто не измерен (деградация, не отсутствие данных).
	// Available=false: ни одно чтение не удалось, карточка показывает честное
	// «измерение недоступно», а не вводящие в заблуждение 0 ГБ.
	if !anySuccess {
		return sub, &TrafficInfo{
			UsedGB:             0,
			LimitGB:            limitGB,
			Percentage:         0,
			ProgressBar:        utils.GenerateProgressBar(0, float64(limitGB)),
			ResetInfo:          resetUnknownText,
			Available:          false,
			CreatedAtFormatted: utils.FormatDateRu(sub.CreatedAt),
			ExpiresAtFormatted: formatExpiresAt(sub.ExpiresAt),
			PlanName:           planName,
		}, nil
	}

	// Лимит для отображения: по умолчанию плановый, но если админ вручную
	// выставил лимит в панели (traffic.Total, байты) и тот расходится с планом
	// более чем на 1% — показываем панельный, чтобы ручная правка была видна
	// пользователю. Логика уведомлений по-прежнему опирается на план
	// (planTrafficLimitGB): здесь меняется только отображаемый лимит.
	displayLimitGB := limitGB

	if panelTotal > 0 && plan != nil {
		planBytes := plan.TrafficLimit

		var divergence int64
		if panelTotal > planBytes {
			divergence = panelTotal - planBytes
		} else {
			divergence = planBytes - panelTotal
		}

		// divergence > 1% от плана (в целочисленной арифметике, без float).
		if divergence*100 > planBytes {
			displayLimitGB = int(float64(panelTotal) / 1024 / 1024 / 1024)
		}
	}

	usedGB := float64(totalUp+totalDown) / 1024 / 1024 / 1024
	percentage := 0.0

	if displayLimitGB > 0 {
		percentage = (usedGB / float64(displayLimitGB)) * 100
		if percentage > 100 {
			percentage = 100
		}
	}

	// Progress bar
	progressBar := utils.GenerateProgressBar(usedGB, float64(displayLimitGB))

	// «🔄 Сброс» показывает настоящий сброс трафика из профиля панели
	// (trafficReset/trafficResetDay через GET /clients/get), а не дни до
	// истечения подписки. Профиль читается best-effort с первой успешно
	// опрошенной ноды: ошибка не ломает карточку.
	var resetInfo string

	profile, pErr := profileSource.reader.GetClientByEmail(ctx, email)
	switch {
	case pErr == nil && profile != nil:
		resetInfo = describeTrafficReset(time.Now(), profile.Client).Text
	case errors.Is(pErr, xui.ErrClientNotFound):
		// Клиента на панели больше нет — сбрасывать нечего.
		resetInfo = resetNeverText
	default:
		if pErr != nil {
			logger.Debug("GetWithTraffic: failed to load client reset profile",
				zap.Uint("node_id", profileSource.nodeID),
				zap.Error(pErr))
		} else {
			logger.Debug("GetWithTraffic: client reset profile was nil without error",
				zap.Uint("node_id", profileSource.nodeID))
		}

		resetInfo = resetUnknownText
	}

	return sub, &TrafficInfo{
		UsedGB:             usedGB,
		LimitGB:            displayLimitGB,
		Percentage:         percentage,
		ProgressBar:        progressBar,
		ResetInfo:          resetInfo,
		Available:          true,
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
