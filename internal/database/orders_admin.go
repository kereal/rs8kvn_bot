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

// OrderAdminFilter — фильтры экрана «Заказы». Zero value = без фильтра.
// Валидация строковых полей — в хендлере (белые списки P8), здесь — только SQL.
type OrderAdminFilter struct {
	Status string     // "" | pending | paid | expired | canceled
	Query  string     // telegram_id (цифры) или provider_payment_id
	From   *time.Time // окно created_at, from включительно
	To     *time.Time // to включительно (конец дня)
	Sort   string     // id | created_at | paid_at | amount_cents
	Desc   bool
	Limit  int
	Offset int
}

// OrderOverview — заказ с denormalized полями для панели.
type OrderOverview struct {
	Order
	TelegramID   int64  // из subscriptions (JOIN)
	PlanName     string // из products -> plans (JOIN)
	ProductName  string // из products (JOIN)
	DurationDays int    // из products (JOIN)
}

// orderOverviewSelect — проекция одного SELECT для всех read-методов экрана.
// orders.* отдаёт колонки модели Order, алиасы — denormalized поля.
const orderOverviewSelect = `orders.*, subscriptions.telegram_id AS telegram_id,
	products.name AS product_name, products.duration_days AS duration_days,
	plans.name AS plan_name`

// buildOrderAdminQuery возвращает свежую цепочку с общим WHERE (статус, поиск,
// окно дат) и JOINs. Используется и для COUNT, и для SELECT — фильтры total и
// items не могут разъехаться: WHERE описан один раз. Сортировка и пагинация
// применяются вызывающей стороной (COUNT их не принимает).
func buildOrderAdminQuery(s *Service, ctx context.Context, f OrderAdminFilter) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&Order{}).
		Joins("LEFT JOIN subscriptions ON subscriptions.id = orders.subscription_id").
		Joins("LEFT JOIN products ON products.id = orders.product_id").
		Joins("LEFT JOIN plans ON products.plan_id = plans.id")

	if f.Status != "" {
		q = q.Where("orders.status = ?", f.Status)
	}

	if f.Query != "" {
		if tgid, ok := numericTelegramID(f.Query); ok {
			// Равенство по telegram_id использует idx_subscriptions_telegram_id.
			q = q.Where(
				"(CAST(subscriptions.telegram_id AS TEXT) = ? OR orders.provider_payment_id LIKE ?)",
				strconv.FormatInt(tgid, 10), "%"+f.Query+"%",
			)
		} else {
			q = q.Where("orders.provider_payment_id LIKE ?", "%"+f.Query+"%")
		}
	}

	if f.From != nil {
		q = q.Where("orders.created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("orders.created_at <= ?", *f.To)
	}

	return q
}

// numericTelegramID распознаёт поиск по цифрам со знаком (trial-подписки имеют
// отрицательный telegram_id). Переполнение int64 трактуется как поиск по
// provider_payment_id, а не как ошибка.
func numericTelegramID(q string) (int64, bool) {
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

// ListOrderOverviews возвращает страницу заказов с denormalized полями и
// общее число строк по тем же фильтрам. Два SQL (count + select), N+1 нет.
// Вызов buildOrderAdminQuery дважды вместо повторного использования одной
// цепочки детерминирован: GORM наследует накопленные clauses при повторном
// терминальном вызове на той же цепочке.
func (s *Service) ListOrderOverviews(ctx context.Context, f OrderAdminFilter) (items []OrderOverview, total int64, err error) {
	err = buildOrderAdminQuery(s, ctx, f).Count(&total).Error
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count orders: %w", err)
	}

	sort := f.Sort
	if sort == "" {
		sort = "created_at"
	}
	dir := "ASC"
	if f.Desc {
		dir = "DESC"
	}

	err = buildOrderAdminQuery(s, ctx, f).
		Select(orderOverviewSelect).
		Order("orders." + sort + " " + dir).
		Limit(f.Limit).
		Offset(f.Offset).
		Scan(&items).Error
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list orders: %w", err)
	}

	return items, total, nil
}

// GetOrderOverview возвращает заказ с denormalized полями или
// ErrOrderNotFound, если заказа нет.
func (s *Service) GetOrderOverview(ctx context.Context, id uint) (*OrderOverview, error) {
	var row OrderOverview
	err := buildOrderAdminQuery(s, ctx, OrderAdminFilter{}).
		Select(orderOverviewSelect).
		Where("orders.id = ?", id).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("failed to get order %d: %w", id, err)
	}

	return &row, nil
}
