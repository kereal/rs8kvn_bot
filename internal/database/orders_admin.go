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

// RevenueDay — выручка одного дня для экрана «Состояние». Дата — строка UTC
// "YYYY-MM-DD": панель рисует график по ней без дополнительной обработки.
type RevenueDay struct {
	Date         string
	Orders       int64
	RevenueCents int64
}

// RevenueTotals — сводные цифры за тот же период, что и дни.
type RevenueTotals struct {
	PaidOrders int64
	// RevenueCents — сумма фактических списаний (callback_amount_cents,
	// если провайдер сообщил и списание не нулевое, иначе amount_cents).
	RevenueCents int64
	// AvgCheckCents — RevenueCents / PaidOrders, целочисленно; 0 при пустом периоде.
	AvgCheckCents int64
	Canceled      int64
	Expired       int64
}

// RevenueStats — денежные агрегаты для GET /admin/api/revenue. Pending-заказы
// выручку не образуют и в totals не входят.
type RevenueStats struct {
	Days   []RevenueDay
	Totals RevenueTotals
	// Currency — единственная валюта оплаченных заказов периода; "" когда
	// валют несколько (суммы в разных валютах складывать нельзя).
	Currency string
}

// revenueDayRow — строка GROUP BY date(paid_at) из SQLite.
type revenueDayRow struct {
	Day     string
	Orders  int64
	Revenue int64
}

// revenuePaidTotalsRow — агрегаты оплаченных заказов одним SELECT'ом.
type revenuePaidTotalsRow struct {
	PaidOrders int64
	Revenue    int64
}

// revenueStatusCountsRow — отменённые/истёкшие за период одним SELECT'ом.
type revenueStatusCountsRow struct {
	Canceled int64
	Expired  int64
}

// revenueExpr — выручка одного заказа: фактическое списание, если оно известно
// и не нулевое, иначе заявленная сумма (миграция 035).
const revenueExpr = `COALESCE(NULLIF(callback_amount_cents, 0), amount_cents)`

// GetRevenueStats возвращает дневную выручку и сводные цифры за последние days
// дней (включая сегодня, UTC). days < 1 трактуется как 1. Дни без оплаченных
// заказов заполняются нулями в Go — SQL не порождает пустые дни. Отменённые и
// истёкшие считаются по created_at в том же окне (paid_at у них не заполняется);
// оплаченные — по paid_at.
func (s *Service) GetRevenueStats(ctx context.Context, days int) (RevenueStats, error) {
	if days < 1 {
		days = 1
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	from := today.AddDate(0, 0, -(days - 1))
	to := today.AddDate(0, 0, 1) // to — исключающая граница следующего дня

	var rows []revenueDayRow
	err := s.db.WithContext(ctx).Raw(`
		SELECT date(paid_at) AS day, COUNT(*) AS orders, SUM(`+revenueExpr+`) AS revenue
		FROM orders
		WHERE status = ? AND paid_at >= ? AND paid_at < ?
		GROUP BY date(paid_at)`, OrderStatusPaid, from, to).Scan(&rows).Error
	if err != nil {
		return RevenueStats{}, fmt.Errorf("failed to aggregate daily revenue: %w", err)
	}

	// Заполняем все дни периода нулями и накладываем SQL-итоги по дате.
	byDay := make(map[string]revenueDayRow, len(rows))
	for _, row := range rows {
		byDay[row.Day] = row
	}
	dayList := make([]RevenueDay, 0, days)
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		day := RevenueDay{Date: key}
		if row, ok := byDay[key]; ok {
			day.Orders = row.Orders
			day.RevenueCents = row.Revenue
		}
		dayList = append(dayList, day)
	}

	var paid revenuePaidTotalsRow
	err = s.db.WithContext(ctx).Raw(`
		SELECT COUNT(*) AS paid_orders, COALESCE(SUM(`+revenueExpr+`), 0) AS revenue
		FROM orders
		WHERE status = ? AND paid_at >= ? AND paid_at < ?`, OrderStatusPaid, from, to).Scan(&paid).Error
	if err != nil {
		return RevenueStats{}, fmt.Errorf("failed to aggregate paid totals: %w", err)
	}

	var counts revenueStatusCountsRow
	err = s.db.WithContext(ctx).Raw(`
		SELECT
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS canceled,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS expired
		FROM orders
		WHERE created_at >= ? AND created_at < ?`, OrderStatusCanceled, OrderStatusExpired, from, to).Scan(&counts).Error
	if err != nil {
		return RevenueStats{}, fmt.Errorf("failed to count canceled/expired orders: %w", err)
	}

	// Валюту отдаём одной строкой только если все оплаченные заказы периода
	// в одной валюте; иначе суммы несравнимы и currency пуст.
	var currencies []string
	err = s.db.WithContext(ctx).Raw(`
		SELECT DISTINCT currency FROM orders
		WHERE status = ? AND paid_at >= ? AND paid_at < ?`, OrderStatusPaid, from, to).Scan(&currencies).Error
	if err != nil {
		return RevenueStats{}, fmt.Errorf("failed to list revenue currencies: %w", err)
	}

	stats := RevenueStats{
		Days: dayList,
		Totals: RevenueTotals{
			PaidOrders:    paid.PaidOrders,
			RevenueCents:  paid.Revenue,
			AvgCheckCents: avgCheck(paid.Revenue, paid.PaidOrders),
			Canceled:      counts.Canceled,
			Expired:       counts.Expired,
		},
	}
	if len(currencies) == 1 {
		stats.Currency = currencies[0]
	}

	return stats, nil
}

// avgCheck — средний чек целочисленно (округление вниз); 0 без заказов.
func avgCheck(revenue, orders int64) int64 {
	if orders == 0 {
		return 0
	}

	return revenue / orders
}
