package database

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SubscriptionAdminFilter — фильтры экрана «Подписки». Zero value = без
// фильтра. Валидация строковых полей (статус, колонка сортировки) — в
// хендлере (белые списки), здесь — только SQL.
type SubscriptionFilter struct {
	Status     string        // "" | active | expired | paused | canceled | revoked
	PlanID     *uint         // точное plan_id
	Query      string        // telegram_id (цифры) | username (LIKE) | subscription_id (точное)
	ExpiringIn time.Duration // expires_at в окне (now, now+h)
	Sort       string        // id | created_at | expires_at | last_request
	Desc       bool
	Limit      int
	Offset     int
}

// SubscriptionOverview — подписка с denormalized полями для панели.
// Тяжёлые поля (devices, ips, JSON) в список не отдаются.
type SubscriptionOverview struct {
	ID             uint
	TelegramID     int64
	Username       string
	SubscriptionID string
	Status         string
	PlanID         *uint
	PlanName       string
	ExpiresAt      *time.Time
	LastRequest    *time.Time
	NodesActive    int64
	NodesTotal     int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SubscriptionNodeView — одна строка таблицы узлов в детали подписки.
type SubscriptionNodeView struct {
	NodeID    uint
	NodeName  string
	Status    string
	LastError *string
}

// subscriptionOverviewSelect — проекция одного SELECT для всех read-методов
// экрана. Счётчики узлов считаются подзапросом по subscription_nodes с group
// by subscription_id: один JOIN вместо построчных запросов (N+1 запрещён).
const subscriptionOverviewSelect = `subscriptions.id, subscriptions.telegram_id, subscriptions.username,
	subscriptions.subscription_id, subscriptions.status, subscriptions.plan_id,
	subscriptions.expires_at, subscriptions.last_request,
	subscriptions.created_at, subscriptions.updated_at,
	plans.name AS plan_name,
	COALESCE(node_stats.nodes_active, 0) AS nodes_active,
	COALESCE(node_stats.nodes_total, 0) AS nodes_total`

// subscriptionNodeStats — подзапрос счётчиков узлов. JOIN по числовому id:
// subscription_nodes.subscription_id ссылается на subscriptions.id, а не на
// строковый публичный идентификатор (миграция 018).
const subscriptionNodeStats = `(SELECT subscription_id,
		SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END) AS nodes_active,
		COUNT(*) AS nodes_total
	FROM subscription_nodes
	GROUP BY subscription_id) AS node_stats`

// buildSubscriptionAdminQuery возвращает свежую цепочку с общим WHERE и
// JOINs. Используется и для COUNT, и для SELECT: WHERE описан один раз,
// фильтры total и items не могут разъехаться. Сортировка и пагинация
// применяются вызывающей стороной (COUNT их не принимает).
func buildSubscriptionAdminQuery(s *Service, ctx context.Context, f SubscriptionFilter) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&Subscription{}).
		Joins("LEFT JOIN plans ON plans.id = subscriptions.plan_id").
		Joins("LEFT JOIN " + subscriptionNodeStats + " ON node_stats.subscription_id = subscriptions.id")

	if f.Status != "" {
		q = q.Where("subscriptions.status = ?", f.Status)
	}

	if f.PlanID != nil {
		q = q.Where("subscriptions.plan_id = ?", *f.PlanID)
	}

	if f.Query != "" {
		if tgid, ok := numericSubscriptionQuery(f.Query); ok {
			q = q.Where("subscriptions.telegram_id = ?", tgid)
		} else {
			// Строковый запрос: точное совпадение по публичному
			// subscription_id или частичное по username — в одном WHERE,
			// чтобы поиск «на глаз» находил обе сущности.
			q = q.Where("(subscriptions.subscription_id = ? OR subscriptions.username LIKE ?)", f.Query, "%"+f.Query+"%")
		}
	}

	if f.ExpiringIn > 0 {
		now := time.Now().UTC()
		q = q.Where("subscriptions.expires_at > ? AND subscriptions.expires_at <= ?", now, now.Add(f.ExpiringIn))
	}

	return q
}

// numericSubscriptionQuery распознаёт поиск по цифрам со знаком (trial
// подписки имеют отрицательный telegram_id). Переполнение int64 трактуется
// как поиск по username, а не как ошибка.
func numericSubscriptionQuery(q string) (int64, bool) {
	digits := strings.TrimPrefix(q, "-")
	if digits == "" {
		return 0, false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	tgid, err := strconv.ParseInt(q, 10, 64)
	if err != nil {
		return 0, false
	}
	return tgid, true
}

// ListSubscriptionOverviews возвращает страницу подписок с denormalized
// полями и общее число строк по тем же фильтрам. Ровно два SQL (count +
// select), N+1 нет. Цепочка строится дважды намеренно: GORM наследует
// накопленные clauses при повторном терминальном вызове, а независимые
// цепочки детерминированы.
func (s *Service) ListSubscriptionOverviews(ctx context.Context, f SubscriptionFilter) (items []SubscriptionOverview, total int64, err error) {
	err = buildSubscriptionAdminQuery(s, ctx, f).Count(&total).Error
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count subscriptions: %w", err)
	}

	sort := f.Sort
	if sort == "" {
		sort = "id"
	}
	dir := "ASC"
	if f.Desc {
		dir = "DESC"
	}

	err = buildSubscriptionAdminQuery(s, ctx, f).
		Select(subscriptionOverviewSelect).
		Order("subscriptions." + sort + " " + dir).
		Limit(f.Limit).
		Offset(f.Offset).
		Scan(&items).Error
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list subscriptions: %w", err)
	}

	return items, total, nil
}

// GetSubscriptionDetail возвращает подписку для экрана детали вместе со
// списком её узлов (с именами из nodes). Если подписки нет —
// ErrSubscriptionNotFound; список узлов пустой, но не nil.
func (s *Service) GetSubscriptionDetail(ctx context.Context, id uint) (*SubscriptionOverview, []SubscriptionNodeView, error) {
	var row SubscriptionOverview

	err := buildSubscriptionAdminQuery(s, ctx, SubscriptionFilter{}).
		Select(subscriptionOverviewSelect).
		Where("subscriptions.id = ?", id).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrSubscriptionNotFound
		}
		return nil, nil, fmt.Errorf("failed to get subscription detail %d: %w", id, err)
	}

	nodes, err := s.listSubscriptionNodeViews(ctx, row.ID)
	if err != nil {
		return nil, nil, err
	}

	return &row, nodes, nil
}

// listSubscriptionNodeViews загружает привязки узлов одним запросом: имена
// приходят LEFT JOIN'ом (привязка может ссылаться на удалённый узел).
func (s *Service) listSubscriptionNodeViews(ctx context.Context, subscriptionID uint) ([]SubscriptionNodeView, error) {
	var nodes []SubscriptionNodeView

	err := s.db.WithContext(ctx).
		Table("subscription_nodes").
		Select(`subscription_nodes.node_id,
			COALESCE(nodes.name, '') AS node_name,
			subscription_nodes.status,
			subscription_nodes.last_error`).
		Joins("LEFT JOIN nodes ON nodes.id = subscription_nodes.node_id").
		Where("subscription_nodes.subscription_id = ?", subscriptionID).
		Order("subscription_nodes.node_id ASC").
		Scan(&nodes).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list subscription nodes for %d: %w", subscriptionID, err)
	}

	if nodes == nil {
		nodes = []SubscriptionNodeView{}
	}

	return nodes, nil
}
