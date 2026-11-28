package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// OrderAdminFilter — фильтры экрана «Заказы». Zero value = без фильтра.
// Валидация строковых полей — в хендлере (белые списки P8), здесь — только SQL.
type OrderAdminFilter struct {
	ID     *uint  // точный orders.id (глобальный поиск «order:42»)
	Status string // "" | pending | paid | expired | canceled
	// Uncertain — признак ручной сверки: true = только заказы с
	// payment_creation_uncertain, false = только однозначные, nil = все.
	Uncertain *bool
	Query     string     // telegram_id (цифры) или provider_payment_id
	From      *time.Time // окно created_at, from включительно
	To        *time.Time // to включительно (конец дня)
	Sort      string     // id | created_at | paid_at | amount_cents
	Desc      bool
	Limit     int
	Offset    int
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

	if f.ID != nil {
		q = q.Where("orders.id = ?", *f.ID)
	}

	if f.Status != "" {
		q = q.Where("orders.status = ?", f.Status)
	}

	if f.Uncertain != nil {
		q = q.Where("orders.payment_creation_uncertain = ?", *f.Uncertain)
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
	// RevenueCents — gross: сумма фактических списаний (callback_amount_cents,
	// если провайдер сообщил и списание не нулевое, иначе amount_cents).
	RevenueCents int64
	// AvgCheckCents — RevenueCents / PaidOrders, целочисленно; 0 при пустом периоде.
	AvgCheckCents int64
	Canceled      int64
	Expired       int64
	// RefundedCents — сумма финансовых возвратов (refund + chargeback) в
	// валюте Currency, случившихся в периоде. Источник — события аудита,
	// которые retention не удаляет (см. auditRefundEvents).
	RefundedCents int64
	// NetRevenueCents — RevenueCents - RefundedCents. Может быть отрицательным,
	// если возврат в периоде относится к оплате прошлого периода.
	NetRevenueCents int64
	// RefundCount — число возвратов в периоде (refund + chargeback).
	RefundCount int64
	// ChargebackCount — сколько из RefundCount пришло как chargeback_received.
	// Отдельный счётчик различает банковский чарджбэк и обычный refund.
	ChargebackCount int64
}

// RevenueStats — денежные агрегаты для GET /admin/api/revenue. Pending-заказы
// выручку не образуют и в totals не входят. Денежные суммы и число оплат
// относятся строго к одной валюте Currency; canceled/expired остаются
// операционными счётчиками за период и валютой не измеряются.
type RevenueStats struct {
	Days   []RevenueDay
	Totals RevenueTotals
	// Currency — валюта, к которой относятся все суммы и счётчики: запрошенная,
	// либо выбранная автоматически (RUB, если среди оплат и возвратов есть RUB,
	// иначе первая в стабильном порядке), либо "" — когда валюта не запрошена и
	// ни оплат, ни возвратов в периоде нет.
	Currency string
	// Currencies — все валюты оплат и возвратов периода в стабильном
	// (алфавитном) порядке; список не зависит от выбранной валюты.
	Currencies []string
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

// auditRefundEvents — финансовые возвраты в журнале аудита. Отдельной колонки
// в orders нет (решение владельца: без миграции), поэтому единственный
// долговечный источник факта возврата — эти события. Их не удаляет retention
// (auditRetentionExemptEvents): сумма возврата должна пережить срок хранения.
var auditRefundEvents = []string{
	AuditEventPaymentRefunded,
	AuditEventPaymentAutoRefunded,
	AuditEventChargebackReceived,
}

// auditRefundDetails — поля details, которые несут финансовый факт возврата.
type auditRefundDetails struct {
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
	Downgraded  bool   `json:"downgraded"`
}

// OrderRefund — финансовый возврат по заказу, прочитанный из audit_log.
// Экран заказа показывает его как историю: панель возвраты не инициирует.
type OrderRefund struct {
	Event       string
	AmountCents int64
	Currency    string
	// Downgraded — подписка была понижена до free в той же транзакции.
	Downgraded bool
	ActorType  string
	CreatedAt  time.Time
}

// revenueRefundTotals — агрегаты возвратов в выбранной валюте за период.
type revenueRefundTotals struct {
	RefundedCents   int64
	RefundCount     int64
	ChargebackCount int64
}

// parseRefundDetails разбирает details события возврата. Пустые/битые details
// или отсутствие суммы дают ok=false: факт без суммы в агрегат не попадает.
func parseRefundDetails(raw string) (auditRefundDetails, bool) {
	var details auditRefundDetails
	err := json.Unmarshal([]byte(raw), &details)
	if err != nil {
		return details, false
	}

	if details.Currency == "" {
		return details, false
	}

	return details, true
}

// loadRevenueRefunds читает возвраты периода из журнала аудита и раскладывает
// их по валютам. Возвраты редки, поэтому строки разбираются в Go: SQLite
// JSON-функции не нужны, а разбор details остаётся явным. Валюты возвратов
// участвуют в выборе валюты агрегата: период с возвратом, но без оплат, не
// должен терять net-выручку.
func (s *Service) loadRevenueRefunds(ctx context.Context, from, to time.Time) (map[string]revenueRefundTotals, error) {
	var rows []AuditLog

	err := s.db.WithContext(ctx).
		Select("event", "details", "created_at").
		Where("event IN ?", auditRefundEvents).
		Where("created_at >= ? AND created_at < ?", from, to).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to load refunds: %w", err)
	}

	byCurrency := make(map[string]revenueRefundTotals, 2)
	for _, row := range rows {
		details, ok := parseRefundDetails(row.Details)
		if !ok {
			continue
		}

		totals := byCurrency[details.Currency]
		totals.RefundedCents += details.AmountCents
		totals.RefundCount++
		if row.Event == AuditEventChargebackReceived {
			totals.ChargebackCount++
		}
		byCurrency[details.Currency] = totals
	}

	return byCurrency, nil
}

// pickRevenueCurrency выбирает валюту агрегата, когда клиент её не задал:
// RUB, если среди оплаченных заказов периода есть RUB, иначе первая в
// стабильном (алфавитном) порядке; пустой список — пустая строка.
func pickRevenueCurrency(currencies []string) string {
	for _, c := range currencies {
		if c == "RUB" {
			return "RUB"
		}
	}
	if len(currencies) > 0 {
		return currencies[0]
	}
	return ""
}

// GetRevenueStats возвращает дневную выручку и сводные цифры за последние days
// дней (включая «сегодня» — now, UTC) в валюте currency. days < 1 трактуется
// как 1. Пустая currency означает авто-выбор: RUB, если среди оплат и возвратов
// периода есть RUB, иначе первая валюта в стабильном (алфавитном) порядке; без
// них Currency остаётся пустой. Запрошенная валюта, которой нет среди оплат,
// даёт нулевую статистику этой валюты и полный список Currencies. Все суммы,
// число оплат и средний чек фильтруются выбранной валютой и валют не
// смешивают. canceled/expired — общие операционные счётчики за период.
//
// Выручка двухуровневая: RevenueCents — gross по оплатам периода, RefundedCents
// — возвраты, случившиеся в периоде (refund + chargeback), NetRevenueCents — их
// разность. Возврат приходит из audit_log, поэтому возврат в периоде может
// относиться к оплате прошлого периода — это период возврата, а не оплаты.
//
// Дни без оплаченных заказов заполняются нулями в Go — SQL не порождает пустые
// дни. Отменённые и истёкшие считаются по created_at в том же окне (paid_at у
// них не заполняется); оплаченные — по paid_at. now передаётся вызывающей
// стороной (хендлер — time.Now().UTC()): окно фиксируется один раз и не
// может разъехаться между SQL-запросами на границе суток.
func (s *Service) GetRevenueStats(ctx context.Context, days int, currency string, now time.Time) (RevenueStats, error) {
	if days < 1 {
		days = 1
	}

	today := now.UTC().Truncate(24 * time.Hour)
	from := today.AddDate(0, 0, -(days - 1))
	to := today.AddDate(0, 0, 1) // to — исключающая граница следующего дня

	// Все валюты оплаченных заказов периода в стабильном порядке. Выбор
	// валюты делается по оплаченным: сумма всегда относится к валюте, в
	// которой были оплаты; canceled/expired фильтруются той же валютой.
	var currencies []string
	err := s.db.WithContext(ctx).Raw(`
		SELECT DISTINCT currency FROM orders
		WHERE status = ? AND paid_at >= ? AND paid_at < ?
		ORDER BY currency`, OrderStatusPaid, from, to).Scan(&currencies).Error
	if err != nil {
		return RevenueStats{}, fmt.Errorf("failed to list revenue currencies: %w", err)
	}

	refundsByCurrency, err := s.loadRevenueRefunds(ctx, from, to)
	if err != nil {
		return RevenueStats{}, err
	}

	// Валюты возвратов входят в набор: иначе возврат в периоде без оплат
	// потерял бы net-выручку при авто-выборе.
	for cur := range refundsByCurrency {
		if !slices.Contains(currencies, cur) {
			currencies = append(currencies, cur)
		}
	}
	slices.Sort(currencies)

	selected := currency
	if selected == "" {
		selected = pickRevenueCurrency(currencies)
	}

	var rows []revenueDayRow
	err = s.db.WithContext(ctx).Raw(`
		SELECT date(paid_at) AS day, COUNT(*) AS orders, SUM(`+revenueExpr+`) AS revenue
		FROM orders
		WHERE status = ? AND currency = ? AND paid_at >= ? AND paid_at < ?
		GROUP BY date(paid_at)`, OrderStatusPaid, selected, from, to).Scan(&rows).Error
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
		WHERE status = ? AND currency = ? AND paid_at >= ? AND paid_at < ?`,
		OrderStatusPaid, selected, from, to).Scan(&paid).Error
	if err != nil {
		return RevenueStats{}, fmt.Errorf("failed to aggregate paid totals: %w", err)
	}

	var counts revenueStatusCountsRow
	err = s.db.WithContext(ctx).Raw(`
		SELECT
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS canceled,
			COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS expired
		FROM orders
		WHERE created_at >= ? AND created_at < ?`,
		OrderStatusCanceled, OrderStatusExpired, from, to).Scan(&counts).Error
	if err != nil {
		return RevenueStats{}, fmt.Errorf("failed to count canceled/expired orders: %w", err)
	}

	refunds := refundsByCurrency[selected]

	stats := RevenueStats{
		Days: dayList,
		Totals: RevenueTotals{
			PaidOrders:      paid.PaidOrders,
			RevenueCents:    paid.Revenue,
			AvgCheckCents:   avgCheck(paid.Revenue, paid.PaidOrders),
			Canceled:        counts.Canceled,
			Expired:         counts.Expired,
			RefundedCents:   refunds.RefundedCents,
			NetRevenueCents: paid.Revenue - refunds.RefundedCents,
			RefundCount:     refunds.RefundCount,
			ChargebackCount: refunds.ChargebackCount,
		},
		Currency:   selected,
		Currencies: currencies,
	}

	return stats, nil
}

// ListOrderRefunds возвращает финансовые возвраты конкретного заказа из
// журнала аудита в хронологическом порядке. Панель показывает их как историю:
// ни один путь записи здесь не живёт.
func (s *Service) ListOrderRefunds(ctx context.Context, orderID uint) ([]OrderRefund, error) {
	var rows []AuditLog

	err := s.db.WithContext(ctx).
		Select("event", "details", "actor_type", "created_at").
		Where("entity_type = ? AND entity_id = ? AND event IN ?",
			EntityOrder, strconv.FormatUint(uint64(orderID), 10), auditRefundEvents).
		Order("created_at ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list refunds for order %d: %w", orderID, err)
	}

	refunds := make([]OrderRefund, 0, len(rows))
	for _, row := range rows {
		details, ok := parseRefundDetails(row.Details)
		if !ok {
			continue
		}

		refunds = append(refunds, OrderRefund{
			Event:       row.Event,
			AmountCents: details.AmountCents,
			Currency:    details.Currency,
			Downgraded:  details.Downgraded,
			ActorType:   string(row.ActorType),
			CreatedAt:   row.CreatedAt.UTC(),
		})
	}

	return refunds, nil
}

// avgCheck — средний чек целочисленно (округление вниз); 0 без заказов.
func avgCheck(revenue, orders int64) int64 {
	if orders == 0 {
		return 0
	}

	return revenue / orders
}
