package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedOrderFixtures creates plan -> product -> two subscriptions (one normal,
// one trial with a negative telegram_id) -> four orders with distinct statuses,
// amounts and created_at values. Boundary times (end/start of day) are included
// on purpose: the from/to window test relies on them.
func seedOrderFixtures(t *testing.T, svc *Service) []Order {
	t.Helper()
	ctx := context.Background()

	plan := &Plan{Name: "paid-30d", IsActive: true, DevicesLimit: 2, TrafficLimit: 100 << 30}
	require.NoError(t, svc.CreatePlan(ctx, plan))

	product := &Product{PlanID: plan.ID, Name: "30 дней", DurationDays: 30, PriceCents: 39900, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	sub := &Subscription{TelegramID: 111000111, ClientID: "c-111000111", SubscriptionID: "s-111000111", Status: string(SubscriptionStatusActive), PlanID: plan.ID}
	require.NoError(t, svc.db.Create(sub).Error)

	trialSub := &Subscription{TelegramID: -555, ClientID: "c-trial", SubscriptionID: "s-trial", Status: string(SubscriptionStatusActive), PlanID: plan.ID}
	require.NoError(t, svc.db.Create(trialSub).Error)

	paidAt := time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)
	rows := []Order{
		{
			SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPending,
			AmountCents: 19900, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: "abc111",
			CreatedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
		},
		{
			SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid,
			AmountCents: 39900, CallbackAmountCents: new(int64(38703)), ProviderFeeCents: new(int64(1197)),
			Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: "def222",
			CreatedAt: time.Date(2026, 11, 2, 23, 59, 59, 0, time.UTC),
			PaidAt:    &paidAt,
		},
		{
			SubscriptionID: trialSub.ID, ProductID: product.ID, Status: OrderStatusExpired,
			AmountCents: 1000, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: "8f3cxyz",
			CreatedAt: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC),
		},
		{
			SubscriptionID: trialSub.ID, ProductID: product.ID, Status: OrderStatusCanceled,
			AmountCents: 500, Currency: "RUB", PaymentProvider: "platega", ProviderPaymentID: "ghi333",
			CreatedAt: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
		},
	}

	for i := range rows {
		require.NoError(t, svc.db.Create(&rows[i]).Error)
	}

	return rows
}

func dayUTC(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func TestListOrderOverviews_NoFilters(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	rows := seedOrderFixtures(t, svc)

	items, total, err := svc.ListOrderOverviews(ctx, OrderAdminFilter{Desc: true, Limit: 50})
	require.NoError(t, err)

	assert.Equal(t, int64(len(rows)), total)
	require.Len(t, items, len(rows))

	// Default order: created_at desc.
	for i := 1; i < len(items); i++ {
		assert.False(t, items[i-1].CreatedAt.Before(items[i].CreatedAt),
			"items must be sorted by created_at desc")
	}
}

func TestListOrderOverviews_StatusFilter_UniformWhere(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedOrderFixtures(t, svc)

	items, total, err := svc.ListOrderOverviews(ctx, OrderAdminFilter{Status: "paid", Limit: 50})
	require.NoError(t, err)

	require.Len(t, items, 1)
	// Единый WHERE: total и items строятся из одного query builder и не разъезжаются.
	assert.Equal(t, int64(len(items)), total)
	assert.Equal(t, OrderStatusPaid, items[0].Status)
}

func TestListOrderOverviews_IDFilter(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	rows := seedOrderFixtures(t, svc)

	exact := rows[0].ID
	items, total, err := svc.ListOrderOverviews(ctx, OrderAdminFilter{ID: &exact, Limit: 50})
	require.NoError(t, err)

	require.Len(t, items, 1)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, exact, items[0].ID)

	missing := uint(999999)
	items, total, err = svc.ListOrderOverviews(ctx, OrderAdminFilter{ID: &missing, Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, items)

	// id сужает, но не ломает остальные фильтры: несочетаемый статус даёт ноль.
	items, total, err = svc.ListOrderOverviews(ctx, OrderAdminFilter{ID: &exact, Status: string(OrderStatusCanceled), Limit: 50})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, items)
}

func TestListOrderOverviews_QueryByTelegramID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedOrderFixtures(t, svc)

	items, total, err := svc.ListOrderOverviews(ctx, OrderAdminFilter{Query: "111000111", Limit: 50})
	require.NoError(t, err)

	require.Len(t, items, 2)
	assert.Equal(t, int64(2), total)
	for _, item := range items {
		assert.Equal(t, int64(111000111), item.TelegramID)
	}

	// Trial-подписки имеют отрицательный telegram_id; минус — допустимый символ.
	items, total, err = svc.ListOrderOverviews(ctx, OrderAdminFilter{Query: "-555", Limit: 50})
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, int64(2), total)
	for _, item := range items {
		assert.Equal(t, int64(-555), item.TelegramID)
	}
}

func TestListOrderOverviews_QueryByProviderPaymentID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedOrderFixtures(t, svc)

	// Частичный LIKE-поиск по id платежа.
	items, total, err := svc.ListOrderOverviews(ctx, OrderAdminFilter{Query: "8f3c", Limit: 50})
	require.NoError(t, err)

	require.Len(t, items, 1)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "8f3cxyz", items[0].ProviderPaymentID)

	// Цифровой запрос ищет и по telegram_id, и по provider_payment_id.
	items, _, err = svc.ListOrderOverviews(ctx, OrderAdminFilter{Query: "333", Limit: 50})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "ghi333", items[0].ProviderPaymentID)
}

func TestListOrderOverviews_CreatedAtWindow(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedOrderFixtures(t, svc)

	// Однодневное окно 2026-11-02: граница конца дня включительно.
	from := dayUTC(2026, 11, 2)
	to := dayUTC(2026, 11, 2).AddDate(0, 0, 1).Add(-time.Nanosecond)

	items, total, err := svc.ListOrderOverviews(ctx, OrderAdminFilter{From: &from, To: &to, Limit: 50})
	require.NoError(t, err)

	require.Len(t, items, 1)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, OrderStatusPaid, items[0].Status)
	assert.True(t, items[0].CreatedAt.Equal(time.Date(2026, 11, 2, 23, 59, 59, 0, time.UTC)))
}

func TestListOrderOverviews_Pagination(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedOrderFixtures(t, svc)

	first, total1, err := svc.ListOrderOverviews(ctx, OrderAdminFilter{Limit: 2, Offset: 0})
	require.NoError(t, err)
	second, total2, err := svc.ListOrderOverviews(ctx, OrderAdminFilter{Limit: 2, Offset: 2})
	require.NoError(t, err)

	require.Len(t, first, 2)
	require.Len(t, second, 2)
	// Страницы не пересекаются, total одинаков на обеих страницах.
	assertNoPageOverlap(t, func(row OrderOverview) uint { return row.ID }, first, second)
	assert.Equal(t, int64(4), total1)
	assert.Equal(t, total1, total2)
}

func TestListOrderOverviews_JoinFields(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedOrderFixtures(t, svc)

	items, _, err := svc.ListOrderOverviews(ctx, OrderAdminFilter{Limit: 50})
	require.NoError(t, err)
	require.Len(t, items, 4)

	for _, item := range items {
		assert.Equal(t, "paid-30d", item.PlanName)
		assert.Equal(t, "30 дней", item.ProductName)
		assert.Equal(t, 30, item.DurationDays)
		assert.NotZero(t, item.TelegramID)
	}
}

func TestListOrderOverviews_SortAmountAsc(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedOrderFixtures(t, svc)

	items, _, err := svc.ListOrderOverviews(ctx, OrderAdminFilter{Sort: "amount_cents", Desc: false, Limit: 50})
	require.NoError(t, err)
	require.Len(t, items, 4)

	for i := 1; i < len(items); i++ {
		assert.LessOrEqual(t, items[i-1].AmountCents, items[i].AmountCents,
			"items must be sorted by amount_cents asc")
	}
}

func TestGetOrderOverview_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetOrderOverview(ctx, 9999)
	require.ErrorIs(t, err, ErrOrderNotFound)
}

func TestGetOrderOverview_FullRow(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedOrderFixtures(t, svc)

	row, err := svc.GetOrderOverview(ctx, 2)
	require.NoError(t, err)

	assert.Equal(t, uint(2), row.ID)
	assert.Equal(t, OrderStatusPaid, row.Status)
	assert.Equal(t, int64(111000111), row.TelegramID)
	assert.Equal(t, "paid-30d", row.PlanName)
	assert.Equal(t, "30 дней", row.ProductName)
	require.NotNil(t, row.CallbackAmountCents)
	assert.Equal(t, int64(38703), *row.CallbackAmountCents)
	require.NotNil(t, row.ProviderFeeCents)
	assert.Equal(t, int64(1197), *row.ProviderFeeCents)
	require.NotNil(t, row.PaidAt)
}

// seedRevenueBase создаёт план -> продукт -> подписку для тестов выручки.
func seedRevenueBase(t *testing.T, svc *Service) (*Product, *Subscription) {
	t.Helper()
	ctx := context.Background()

	plan := &Plan{Name: "rev-30d", IsActive: true, DevicesLimit: 1, TrafficLimit: 100 << 30}
	require.NoError(t, svc.CreatePlan(ctx, plan))

	product := &Product{PlanID: plan.ID, Name: "rev product", DurationDays: 30, PriceCents: 10000, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)

	sub := &Subscription{TelegramID: 222000222, ClientID: "c-rev", SubscriptionID: "s-rev", Status: string(SubscriptionStatusActive), PlanID: plan.ID}
	require.NoError(t, svc.db.Create(sub).Error)

	return product, sub
}

// revenueNow — фиксированные «сейчас» для всех тестов выручки: окно дня
// считается от этого времени, поэтому тесты не зависят от 00:00 UTC и от
// стенных часов машины.
var revenueNow = time.Date(2026, 11, 15, 13, 45, 0, 0, time.UTC)

func TestGetRevenueStats_DayBucketsAndCallbackFallback(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	product, sub := seedRevenueBase(t, svc)

	today := revenueNow.Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1)

	// Вчера: callback известен и ненулевой — выручка по фактическому списанию.
	paidAt := yesterday.Add(12 * time.Hour)
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid,
		AmountCents: 10000, CallbackAmountCents: new(int64(9700)), Currency: "RUB", PaidAt: &paidAt,
	}).Error)

	// Сегодня: callback нулевой — фолбэк на amount_cents.
	paidAtZero := today.Add(9 * time.Hour)
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid,
		AmountCents: 5000, CallbackAmountCents: new(int64(0)), Currency: "RUB", PaidAt: &paidAtZero,
	}).Error)

	// Сегодня: callback неизвестен (NULL) — тоже фолбэк на amount_cents.
	paidAtNull := today.Add(10 * time.Hour)
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid,
		AmountCents: 3000, Currency: "RUB", PaidAt: &paidAtNull,
	}).Error)

	stats, err := svc.GetRevenueStats(ctx, 3, "", revenueNow)
	require.NoError(t, err)

	// Окно из 3 дней, заканчивающееся сегодня: [today-2, вчера, сегодня].
	require.Len(t, stats.Days, 3)
	assert.Equal(t, today.AddDate(0, 0, -2).Format("2006-01-02"), stats.Days[0].Date)
	assert.Equal(t, yesterday.Format("2006-01-02"), stats.Days[1].Date)
	assert.Equal(t, today.Format("2006-01-02"), stats.Days[2].Date)

	// Первый день окна пуст — нулевой, но присутствует в выдаче.
	assert.Equal(t, int64(0), stats.Days[0].RevenueCents)
	assert.Equal(t, int64(0), stats.Days[0].Orders)

	assert.Equal(t, int64(9700), stats.Days[1].RevenueCents)
	assert.Equal(t, int64(1), stats.Days[1].Orders)

	assert.Equal(t, int64(8000), stats.Days[2].RevenueCents)
	assert.Equal(t, int64(2), stats.Days[2].Orders)
}

func TestGetRevenueStats_Totals(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	product, sub := seedRevenueBase(t, svc)

	today := revenueNow.Truncate(24 * time.Hour)

	newOrder := func(status OrderStatus, amount int64, paidAt *time.Time, createdAt time.Time) {
		t.Helper()
		require.NoError(t, svc.db.Create(&Order{
			SubscriptionID: sub.ID, ProductID: product.ID, Status: status,
			AmountCents: amount, Currency: "RUB", PaidAt: paidAt, CreatedAt: createdAt,
		}).Error)
	}

	paidYesterday := today.AddDate(0, 0, -1).Add(8 * time.Hour)
	newOrder(OrderStatusPaid, 2000, &paidYesterday, paidYesterday)
	paidToday := today.Add(8 * time.Hour)
	newOrder(OrderStatusPaid, 5000, &paidToday, paidToday)
	paidToday2 := today.Add(9 * time.Hour)
	newOrder(OrderStatusPaid, 3000, &paidToday2, paidToday2)
	pendingToday := today.Add(10 * time.Hour)
	newOrder(OrderStatusPending, 7000, nil, pendingToday)
	canceledToday := today.Add(11 * time.Hour)
	newOrder(OrderStatusCanceled, 7000, nil, canceledToday)
	expiredToday := today.Add(12 * time.Hour)
	newOrder(OrderStatusExpired, 7000, nil, expiredToday)

	stats, err := svc.GetRevenueStats(ctx, 2, "", revenueNow)
	require.NoError(t, err)

	assert.Equal(t, int64(3), stats.Totals.PaidOrders)
	assert.Equal(t, int64(10000), stats.Totals.RevenueCents)
	// 10000 / 3 = 3333 (целочисленно).
	assert.Equal(t, int64(3333), stats.Totals.AvgCheckCents)
	assert.Equal(t, int64(1), stats.Totals.Canceled)
	assert.Equal(t, int64(1), stats.Totals.Expired)
}

func TestGetRevenueStats_Currency(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	product, sub := seedRevenueBase(t, svc)

	today := revenueNow.Truncate(24 * time.Hour)
	paidAt := today.Add(8 * time.Hour)
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid,
		AmountCents: 5000, Currency: "RUB", PaidAt: &paidAt,
	}).Error)

	// Одна валюта: авто-выбор даёт RUB и полный список.
	stats, err := svc.GetRevenueStats(ctx, 7, "", revenueNow)
	require.NoError(t, err)
	assert.Equal(t, "RUB", stats.Currency)
	assert.Equal(t, []string{"RUB"}, stats.Currencies)

	// Вторая валюта: авто-выбор остаётся на RUB, список полный, суммы —
	// строго RUB (XTR-заказ в totals не входит).
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid,
		AmountCents: 500, Currency: "XTR", PaidAt: &paidAt,
	}).Error)

	stats, err = svc.GetRevenueStats(ctx, 7, "", revenueNow)
	require.NoError(t, err)
	assert.Equal(t, "RUB", stats.Currency)
	assert.Equal(t, []string{"RUB", "XTR"}, stats.Currencies)
	assert.Equal(t, int64(1), stats.Totals.PaidOrders)
	assert.Equal(t, int64(5000), stats.Totals.RevenueCents)

	// Явно запрошенная XTR: те же периоды в валюте Stars.
	stats, err = svc.GetRevenueStats(ctx, 7, "XTR", revenueNow)
	require.NoError(t, err)
	assert.Equal(t, "XTR", stats.Currency)
	assert.Equal(t, []string{"RUB", "XTR"}, stats.Currencies)
	assert.Equal(t, int64(1), stats.Totals.PaidOrders)
	assert.Equal(t, int64(500), stats.Totals.RevenueCents)
}

func TestGetRevenueStats_CurrencyAutoPicksFirstWithoutRUB(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	product, sub := seedRevenueBase(t, svc)

	today := revenueNow.Truncate(24 * time.Hour)
	paidAt := today.Add(8 * time.Hour)
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid,
		AmountCents: 300, Currency: "XTR", PaidAt: &paidAt,
	}).Error)

	stats, err := svc.GetRevenueStats(ctx, 7, "", revenueNow)
	require.NoError(t, err)
	assert.Equal(t, "XTR", stats.Currency)
	assert.Equal(t, []string{"XTR"}, stats.Currencies)
	assert.Equal(t, int64(1), stats.Totals.PaidOrders)
}

func TestGetRevenueStats_MissingCurrencyIsEmptyStats(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	product, sub := seedRevenueBase(t, svc)

	today := revenueNow.Truncate(24 * time.Hour)
	paidAt := today.Add(8 * time.Hour)
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusPaid,
		AmountCents: 5000, Currency: "RUB", PaidAt: &paidAt,
	}).Error)

	// Запрошенной валюты нет: нулевая статистика этой валюты, список
	// доступных не зависит от выбора.
	stats, err := svc.GetRevenueStats(ctx, 3, "USD", revenueNow)
	require.NoError(t, err)
	assert.Equal(t, "USD", stats.Currency)
	assert.Equal(t, []string{"RUB"}, stats.Currencies)
	assert.Equal(t, int64(0), stats.Totals.PaidOrders)
	assert.Equal(t, int64(0), stats.Totals.RevenueCents)
	assert.Equal(t, int64(0), stats.Totals.AvgCheckCents)
	assert.Len(t, stats.Days, 3)
	for _, day := range stats.Days {
		assert.Equal(t, int64(0), day.RevenueCents)
		assert.Equal(t, int64(0), day.Orders)
	}
}

func TestGetRevenueStats_CanceledExpiredRemainGlobal(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	product, sub := seedRevenueBase(t, svc)

	today := revenueNow.Truncate(24 * time.Hour)
	canceledAt := today.Add(9 * time.Hour)
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusCanceled,
		AmountCents: 7000, Currency: "XTR", CreatedAt: canceledAt,
	}).Error)
	require.NoError(t, svc.db.Create(&Order{
		SubscriptionID: sub.ID, ProductID: product.ID, Status: OrderStatusExpired,
		AmountCents: 7000, Currency: "RUB", CreatedAt: canceledAt,
	}).Error)

	// Статусы — общие операционные счётчики: валютная выборка их не меняет.
	stats, err := svc.GetRevenueStats(ctx, 2, "RUB", revenueNow)
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats.Totals.PaidOrders)
	assert.Equal(t, int64(1), stats.Totals.Canceled)
	assert.Equal(t, int64(1), stats.Totals.Expired)

	stats, err = svc.GetRevenueStats(ctx, 2, "XTR", revenueNow)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.Totals.Canceled)
	assert.Equal(t, int64(1), stats.Totals.Expired)
}

func TestGetRevenueStats_DaysClampedToAtLeastOne(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	stats, err := svc.GetRevenueStats(ctx, 0, "", revenueNow)
	require.NoError(t, err)
	assert.Len(t, stats.Days, 1)
	assert.Equal(t, revenueNow.Format("2006-01-02"), stats.Days[0].Date)
}
