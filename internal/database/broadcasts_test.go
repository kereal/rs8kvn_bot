package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroadcastCRUD(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	b := &Broadcast{
		Name:        "Акция",
		Filters:     "{}",
		MessageText: "Привет всем!",
		Status:      string(BroadcastStatusCompleted),
	}
	require.NoError(t, svc.CreateBroadcast(ctx, b))
	require.NotZero(t, b.ID)

	got, err := svc.GetBroadcast(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, "Акция", got.Name)
	assert.Equal(t, string(BroadcastStatusCompleted), got.Status)

	finished := time.Now().UTC()
	upd := &Broadcast{
		ID:              b.ID,
		Name:            got.Name,
		Filters:         got.Filters,
		MessageText:     got.MessageText,
		Status:          string(BroadcastStatusCompleted),
		FinishedAt:      &finished,
		RecipientsTotal: 4,
		SentCount:       2,
		BlockedCount:    1,
		FailedCount:     1,
	}
	require.NoError(t, upd.SetDeliveryReport(&BroadcastDeliveryReport{
		Delivered: []int64{111, 222},
		Blocked:   []int64{333},
		Errors:    []BroadcastSendError{{TelegramID: 444, Error: "boom"}},
	}))
	require.NoError(t, svc.UpdateBroadcast(ctx, upd))

	got2, err := svc.GetBroadcast(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), got2.SentCount)
	assert.Equal(t, int64(1), got2.BlockedCount)
	assert.Equal(t, int64(1), got2.FailedCount)

	parsed, err := got2.ParseDeliveryReport()
	require.NoError(t, err)
	assert.Equal(t, []int64{111, 222}, parsed.Delivered)
	assert.Equal(t, []int64{333}, parsed.Blocked)
	require.Len(t, parsed.Errors, 1)
	assert.Equal(t, int64(444), parsed.Errors[0].TelegramID)
	assert.Equal(t, "boom", parsed.Errors[0].Error)
}

func TestBroadcastNotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.GetBroadcast(ctx, 999)
	assert.ErrorIs(t, err, ErrBroadcastNotFound)

	err = svc.UpdateBroadcast(ctx, &Broadcast{ID: 999})
	assert.ErrorIs(t, err, ErrBroadcastNotFound)
}

func TestBroadcastListNewestFirst(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	for i := range 3 {
		require.NoError(t, svc.CreateBroadcast(ctx, &Broadcast{
			Name:        fmt.Sprintf("Рассылка %d", i+1),
			MessageText: "x",
		}))
	}

	broadcasts, err := svc.ListBroadcasts(ctx, 10)
	require.NoError(t, err)
	require.Len(t, broadcasts, 3)
	assert.Equal(t, "Рассылка 3", broadcasts[0].Name)
	assert.Equal(t, "Рассылка 1", broadcasts[2].Name)

	limited, err := svc.ListBroadcasts(ctx, 2)
	require.NoError(t, err)
	require.Len(t, limited, 2)
	assert.Equal(t, "Рассылка 3", limited[0].Name)
}

func TestBroadcastStatusCheckConstraint(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	// CHECK-constraint миграции 036: недопустимый статус не сохраняется.
	err := svc.db.WithContext(ctx).Create(&Broadcast{
		Name:        "bad",
		MessageText: "x",
		Status:      "not-a-status",
	}).Error
	assert.Error(t, err)
}

func TestBroadcastJSONHelpers(t *testing.T) {
	t.Parallel()

	b := &Broadcast{}

	require.NoError(t, b.SetFilters(map[string]any{"plan": "free"}))
	filters, err := b.ParseFilters()
	require.NoError(t, err)
	assert.Equal(t, "free", filters["plan"])

	// Пустой отчёт возвращает инициализированные (не nil) списки.
	empty, err := b.ParseDeliveryReport()
	require.NoError(t, err)
	assert.NotNil(t, empty.Delivered)
	assert.Empty(t, empty.Delivered)
	assert.NotNil(t, empty.Blocked)
	assert.NotNil(t, empty.Errors)
}
