package service

import (
	"context"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// NodeLiveInfo — снимок живых данных ноды для карточки детали в админ-панели.
// Собирается best-effort: недоступность панели или отдельных клиентов не
// ошибка API, а поля с Available=false / нулями.
type NodeLiveInfo struct {
	NodeID uint
	// Available — панель ответила на Ping. false для нод без ридера
	// (fetch/proxman, выключенные ноды) и для недоступных панелей.
	Available bool
	// PingLatencyMS — задержка пинга; значима только при Available.
	PingLatencyMS int64
	// PingError — почему панель недоступна (для отображения оператору).
	PingError string
	// Clients — число прочитанных клиентов (активных привязок ноды).
	Clients int
	// Disabled — сколько из прочитанных клиентов панель отключила (Enable=false).
	Disabled int
	// TrafficBytes — суммарный up+down по всем прочитанным клиентам.
	TrafficBytes int64
	// LastOnline — максимальный LastOnline из панели (unix ms), 0 = нет данных.
	LastOnline int64
}

// GetNodeLive собирает живые данные ноды: пинг панели и трафик всех клиентов,
// чья подписка активна на этой ноде (subscription_nodes, status='active').
// Best-effort по образцу GetWithTraffic: ошибка на клиенте — Warn и продолжаем;
// ни один клиент не ответил и пинг не прошёл — Available=false.
func (s *SubscriptionService) GetNodeLive(ctx context.Context, nodeID uint) (*NodeLiveInfo, error) {
	info := &NodeLiveInfo{NodeID: nodeID}

	reader, ok := s.clients.Reader(nodeID)
	if !ok {
		info.PingError = "у ноды нет читателя панели (тип или нода выключена)"

		return info, nil
	}

	// Общий таймаут на весь опрос — как в ручной диагностике панели.
	ctx, cancel := context.WithTimeout(ctx, config.XUIRequestTimeout)
	defer cancel()

	start := time.Now()
	if err := reader.Ping(ctx); err != nil {
		info.PingError = err.Error()

		return info, nil
	}
	info.Available = true
	info.PingLatencyMS = time.Since(start).Milliseconds()

	bindings, err := s.db.GetActiveByNodeID(ctx, nodeID)
	if err != nil {
		// Панель жива, список привязок прочитать не удалось: пинг показываем,
		// трафик честно пустой.
		logger.Warn("GetNodeLive: failed to load active bindings", zap.Uint("node_id", nodeID), zap.Error(err))

		return info, nil
	}

	for _, binding := range bindings {
		sub, err := s.db.GetByID(ctx, binding.SubscriptionID)
		if err != nil || sub == nil {
			logger.Warn("GetNodeLive: failed to load subscription for binding",
				zap.Uint("node_id", nodeID),
				zap.Uint("subscription_id", binding.SubscriptionID),
				zap.Error(err))

			continue
		}

		email := XUIEmail(sub.Username, sub.TelegramID)

		traffic, err := reader.GetClientTraffic(ctx, email)
		if err != nil {
			logger.Warn("GetNodeLive: GetClientTraffic failed",
				zap.Uint("node_id", nodeID),
				zap.String("email", email),
				zap.Error(err))

			continue
		}

		info.Clients++
		info.TrafficBytes += traffic.Up + traffic.Down
		if !traffic.Enable {
			info.Disabled++
		}
		if traffic.LastOnline > info.LastOnline {
			info.LastOnline = traffic.LastOnline
		}
	}

	return info, nil
}
