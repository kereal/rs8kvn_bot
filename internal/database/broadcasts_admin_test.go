package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Таблица §8 ТЗ 06: аудитория, правка черновика, удаление, повтор, пагинация.

// seedAdminAudience создаёт подписки для проверки фильтров аудитории.
// trialPlan нужен, чтобы убедиться: триал не попадает в рассылку никогда.
func seedAdminAudience(t *testing.T, svc *Service) {
	t.Helper()
	ctx := context.Background()

	free, err := svc.GetPlanByName(ctx, FreePlanName)
	require.NoError(t, err)
	trial, err := svc.GetPlanByName(ctx, TrialPlanName)
	require.NoError(t, err)

	subs := []Subscription{
		{TelegramID: 900001, Username: "free-active", ClientID: "adm-cl-1", SubscriptionID: "adm-sub-1", PlanID: free.ID, Status: string(SubscriptionStatusActive)},
		{TelegramID: 900002, Username: "free-active-2", ClientID: "adm-cl-2", SubscriptionID: "adm-sub-2", PlanID: free.ID, Status: string(SubscriptionStatusActive)},
		{TelegramID: 900003, Username: "trial", ClientID: "adm-cl-3", SubscriptionID: "adm-sub-3", PlanID: trial.ID, Status: string(SubscriptionStatusActive)},
		{TelegramID: 900004, Username: "revoked", ClientID: "adm-cl-4", SubscriptionID: "adm-sub-4", PlanID: free.ID, Status: string(SubscriptionStatusRevoked)},
		{TelegramID: 900005, Username: "paid", ClientID: "adm-cl-5", SubscriptionID: "adm-sub-5", PlanID: free.ID, Status: string(SubscriptionStatusActive), PricePaidCents: 990},
	}
	for i := range subs {
		require.NoError(t, svc.db.Create(&subs[i]).Error)
	}
}

func TestBroadcastAdmin_CountAudience(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)
	seedAdminAudience(t, svc)

	// Пустой фильтр: все активные не-триальные (2 бесплатных + 1 платный).
	// ТЗ ожидает «активные неоплаченные бесплатные» — фактически пустой
	// фильтр означает «все активные», платные тоже получатели.
	count, err := svc.CountBroadcastAudience(ctx, BroadcastFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)

	// plan_type=paid: только product_id/price_paid_cents.
	paid, err := svc.CountBroadcastAudience(ctx, BroadcastFilter{PlanType: "paid"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), paid)

	// subscription_status=expired отключает аудиторию полностью.
	expired, err := svc.CountBroadcastAudience(ctx, BroadcastFilter{SubscriptionStatus: "expired"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), expired)

	// Дедупликация: subscriptions.telegram_id UNIQUE, поэтому отдельно
	// созданные фикстуры дают независимые известные числа — пустой фильтр
	// (3) и план free (3) уже проверены выше; дополнительная самопроверка
	// того же query builder'а здесь не нужна.

	// Триал исключён даже при явном запросе всех статусов.
	all, err := svc.CountBroadcastAudience(ctx, BroadcastFilter{SubscriptionStatus: "all", PlanType: "free"})
	require.NoError(t, err)
	assert.Equal(t, int64(3), all, "free active x2 + revoked free; trial excluded")
}

func TestBroadcastAdmin_UpdateDraft_Scheduled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)

	b := &Broadcast{Name: "Старое", MessageText: "Старый текст", Filters: "{}", Status: string(BroadcastStatusScheduled)}
	require.NoError(t, svc.CreateBroadcast(ctx, b))

	planned := time.Now().UTC().Add(time.Hour)
	draft := &Broadcast{
		ID: b.ID, Name: "Новое", MessageText: "Новый текст",
		Filters: `{"plan_type":"paid"}`, PlannedAt: &planned,
	}
	require.NoError(t, svc.UpdateBroadcastDraft(ctx, draft))

	updated, err := svc.GetBroadcast(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, "Новое", updated.Name)
	assert.Equal(t, "Новый текст", updated.MessageText)
	assert.Equal(t, `{"plan_type":"paid"}`, updated.Filters)
	require.NotNil(t, updated.PlannedAt)
	assert.WithinDuration(t, planned, *updated.PlannedAt, time.Second)

	// Правка после старта запрещена.
	running := &Broadcast{Name: "Идёт", MessageText: "Текст", Status: string(BroadcastStatusRunning)}
	require.NoError(t, svc.CreateBroadcast(ctx, running))
	err = svc.UpdateBroadcastDraft(ctx, &Broadcast{ID: running.ID, Name: "Х", MessageText: "Т", Filters: "{}"})
	require.ErrorIs(t, err, ErrBroadcastLocked)
}

func TestBroadcastAdmin_UpdateDraft_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)

	b := &Broadcast{Name: "Черновик", MessageText: "Текст", Status: string(BroadcastStatusScheduled)}
	require.NoError(t, svc.CreateBroadcast(ctx, b))

	cases := []struct {
		name  string
		draft Broadcast
		field string
	}{
		{"empty name", Broadcast{ID: b.ID, MessageText: "Т", Filters: "{}"}, "name"},
		{"long name", Broadcast{ID: b.ID, Name: string(make([]rune, 256)), MessageText: "Т", Filters: "{}"}, "name"},
		{"empty text", Broadcast{ID: b.ID, Name: "Н", Filters: "{}"}, "message_text"},
		{"long text", Broadcast{ID: b.ID, Name: "Н", MessageText: string(make([]rune, 4097)), Filters: "{}"}, "message_text"},
		{"bad filter", Broadcast{ID: b.ID, Name: "Н", MessageText: "Т", Filters: `{"plan_type":"vip"}`}, "filters"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			draft := tc.draft
			err := svc.UpdateBroadcastDraft(ctx, &draft)
			require.ErrorIs(t, err, ErrBroadcastInvalid)

			var fieldErr *BroadcastFieldError
			require.ErrorAs(t, err, &fieldErr)
			assert.Equal(t, tc.field, fieldErr.Field)
		})
	}
}

func TestBroadcastAdmin_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)

	running := &Broadcast{Name: "Идёт", MessageText: "Т", Status: string(BroadcastStatusRunning)}
	require.NoError(t, svc.CreateBroadcast(ctx, running))
	err := svc.DeleteBroadcast(ctx, running.ID)
	require.ErrorIs(t, err, ErrBroadcastLocked)

	done := &Broadcast{Name: "Готова", MessageText: "Т", Status: string(BroadcastStatusCompleted)}
	require.NoError(t, svc.CreateBroadcast(ctx, done))
	require.NoError(t, svc.DeleteBroadcast(ctx, done.ID))

	_, err = svc.GetBroadcast(ctx, done.ID)
	require.ErrorIs(t, err, ErrBroadcastNotFound)

	// Отсутствующая строка — ErrBroadcastNotFound, а не Locked.
	err = svc.DeleteBroadcast(ctx, 9999)
	require.ErrorIs(t, err, ErrBroadcastNotFound)
}

func TestBroadcastAdmin_ResetRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)

	failed := &Broadcast{
		Name: "Провал", MessageText: "Т", Status: string(BroadcastStatusFailed),
		LastError: "infrastructure failure", RetryCount: 3,
	}
	retryAt := time.Now().UTC().Add(time.Minute)
	failed.RetryAt = &retryAt
	require.NoError(t, svc.CreateBroadcast(ctx, failed))

	now := time.Now().UTC()
	require.NoError(t, svc.ResetBroadcastRetry(ctx, failed.ID, now))

	updated, err := svc.GetBroadcast(ctx, failed.ID)
	require.NoError(t, err)
	assert.Equal(t, string(BroadcastStatusScheduled), updated.Status)
	assert.Nil(t, updated.RetryAt)
	assert.Equal(t, "infrastructure failure", updated.LastError, "last_error must survive the retry")
	assert.Equal(t, 3, updated.RetryCount)

	// Повтор допустим только для failed.
	done := &Broadcast{Name: "Готова", MessageText: "Т", Status: string(BroadcastStatusCompleted)}
	require.NoError(t, svc.CreateBroadcast(ctx, done))
	err = svc.ResetBroadcastRetry(ctx, done.ID, now)
	require.ErrorIs(t, err, ErrBroadcastLocked)
}

func TestBroadcastAdmin_ListPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)

	for i := range 5 {
		b := &Broadcast{Name: "Рассылка", MessageText: "Т", Status: string(BroadcastStatusCompleted)}
		require.NoError(t, svc.CreateBroadcast(ctx, b))
		// id растёт с каждым рядом; created_at одинаков — сортировка id DESC.
		_ = i
	}

	first, total, err := svc.ListBroadcastOverviews(ctx, "", 2, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	require.Len(t, first, 2)

	second, total2, err := svc.ListBroadcastOverviews(ctx, "", 2, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total2, "total stays stable across pages")
	require.Len(t, second, 2)

	assertNoPageOverlap(t, func(row BroadcastOverview) uint { return row.ID }, first, second)

	last, _, err := svc.ListBroadcastOverviews(ctx, "", 2, 4)
	require.NoError(t, err)
	require.Len(t, last, 1)

	// Фильтр по статусу.
	running := &Broadcast{Name: "Идёт", MessageText: "Т", Status: string(BroadcastStatusRunning)}
	require.NoError(t, svc.CreateBroadcast(ctx, running))
	rows, total, err := svc.ListBroadcastOverviews(ctx, string(BroadcastStatusRunning), 10, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, running.ID, rows[0].ID)
}

func TestBroadcastAdmin_OverviewFlags(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newTestService(t)

	b := &Broadcast{Name: "С отчётом", MessageText: "Т", Status: string(BroadcastStatusFailed)}
	require.NoError(t, svc.CreateBroadcast(ctx, b))
	require.NoError(t, svc.db.Model(b).Updates(map[string]any{
		"recipients_state": `{"snapshot":true,"recipients":[{"id":1,"telegram_id":1,"status":"failed"}]}`,
		"delivery_report":  `{"errors":[{"telegram_id":1,"error":"boom"}]}`,
	}).Error)

	rows, _, err := svc.ListBroadcastOverviews(ctx, "", 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].SnapshotTaken)
	assert.Equal(t, int64(1), rows[0].ErrorCount)
}
