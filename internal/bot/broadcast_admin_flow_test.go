package bot

// These tests cover the admin broadcast session controls: filter callbacks,
// back-to-filters navigation, campaign cancel/retry callbacks, callback ID
// validation, and the compact report formatters.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBroadcastCallbackID(t *testing.T) {
	t.Parallel()

	valid, err := parseBroadcastCallbackID("broadcast_cancel_5", "broadcast_cancel_")
	require.NoError(t, err)
	assert.Equal(t, uint(5), valid)

	cases := []string{
		"broadcast_cancel_0",      // zero is invalid
		"broadcast_cancel_",       // empty value
		"broadcast_cancel_abc",    // not a number
		"broadcast_cancel_-3",     // negative
		"broadcast_cancel_18446744073709551616", // uint64 overflow
	}
	for _, data := range cases {
		t.Run(data, func(t *testing.T) {
			_, err := parseBroadcastCallbackID(data, "broadcast_cancel_")
			assert.Error(t, err, "payload %q must be rejected", data)
		})
	}
}

func TestBroadcastCancelCallback_Scheduled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	cancelCalled := false
	mockDB.CancelBroadcastFunc = func(ctx context.Context, id uint, now time.Time) (bool, error) {
		cancelCalled = true
		return true, nil
	}
	handler := newBroadcastTestHandler(mockDB, mockBot)

	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_cancel_7",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	assert.True(t, cancelCalled, "CancelBroadcast must be called for a scheduled campaign")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Рассылка #7 отменена")
}

func TestBroadcastCancelCallback_AlreadyTerminal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	mockDB.CancelBroadcastFunc = func(ctx context.Context, id uint, now time.Time) (bool, error) {
		return false, nil
	}
	handler := newBroadcastTestHandler(mockDB, mockBot)

	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_cancel_7",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	assert.Contains(t, mockBot.LastSentTextSafe(), "уже завершена")
}

func TestBroadcastRetryCallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	retryCalled := false
	mockDB.ResetBroadcastFailedRecipientsFunc = func(ctx context.Context, id uint, now time.Time) error {
		retryCalled = true
		assert.Equal(t, uint(9), id)
		return nil
	}
	handler := newBroadcastTestHandler(mockDB, mockBot)

	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_retry_9",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	assert.True(t, retryCalled, "ResetBroadcastFailedRecipients must be called")
	assert.Contains(t, mockBot.LastSentTextSafe(), "рассылки #9")
}

func TestBroadcastCancelRejectedForNonAdmin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockDB.CancelBroadcastFunc = func(ctx context.Context, id uint, now time.Time) (bool, error) {
		t.Fatal("CancelBroadcast must not be called for a non-admin")
		return false, nil
	}
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)

	nonAdmin := &tgbotapi.User{ID: 999, UserName: "intruder"}
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: nonAdmin, Data: "broadcast_cancel_7",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 999}, MessageID: 1},
	}}))
}

func TestHandleBroadcastFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		data       string
		wantFilter func(*testing.T, database.BroadcastFilter)
	}{
		{
			name: "plan paid",
			data: "bfilter_plan_paid",
			wantFilter: func(t *testing.T, f database.BroadcastFilter) {
				assert.Equal(t, "paid", f.PlanType)
			},
		},
		{
			name: "plan toggle off",
			data: "bfilter_plan_free",
			wantFilter: func(t *testing.T, f database.BroadcastFilter) {
				assert.Equal(t, "free", f.PlanType)
			},
		},
		{
			name: "status all",
			data: "bfilter_status_all",
			wantFilter: func(t *testing.T, f database.BroadcastFilter) {
				assert.Equal(t, "all", f.SubscriptionStatus)
			},
		},
		{
			name: "date 3 months",
			data: "bfilter_date_90",
			wantFilter: func(t *testing.T, f database.BroadcastFilter) {
				require.NotNil(t, f.RegisteredAfter)
			},
		},
		{
			name: "inactive 1 month",
			data: "bfilter_inactive_30",
			wantFilter: func(t *testing.T, f database.BroadcastFilter) {
				require.NotNil(t, f.InactiveDays)
				assert.Equal(t, 30, *f.InactiveDays)
			},
		},
		{
			name: "ever paid",
			data: "bfilter_ever_paid_true",
			wantFilter: func(t *testing.T, f database.BroadcastFilter) {
				require.NotNil(t, f.EverPaid)
				assert.True(t, *f.EverPaid)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			mockDB := testutil.NewDatabaseService()
			mockBot := testutil.NewBotAPI()
			handler := newBroadcastTestHandler(mockDB, mockBot)
			prepareBroadcastSession(t, handler, mockDB)

			require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
				From: broadcastTestAdmin(), Data: tt.data,
				Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
			}}))

			s := handler.getBroadcastSession(broadcastTestAdminID)
			require.NotNil(t, s, "the draft session must survive a filter click")
			tt.wantFilter(t, s.filter)
		})
	}
}

func TestHandleBroadcastFilter_ToggleOff(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)
	prepareBroadcastSession(t, handler, mockDB)

	// First click enables the paid filter.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "bfilter_plan_paid",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	assert.Equal(t, "paid", handler.getBroadcastSession(broadcastTestAdminID).filter.PlanType)

	// Second click toggles it back off.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "bfilter_plan_paid",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	assert.Equal(t, "", handler.getBroadcastSession(broadcastTestAdminID).filter.PlanType)
}

func TestHandleBroadcastBackToFilters(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	mockDB.GetFilteredTelegramIDCountFunc = func(ctx context.Context, filter database.BroadcastFilter) (int64, error) {
		return 5, nil
	}
	handler := newBroadcastTestHandler(mockDB, mockBot)
	prepareBroadcastSession(t, handler, mockDB)

	// Move to the confirmation stage first.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_confirm",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	assert.Equal(t, broadcastStageConfirming, handler.getBroadcastSession(broadcastTestAdminID).stage)

	// Back to filters restores the filtering stage.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_back_to_filters",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	assert.Equal(t, broadcastStageFiltering, handler.getBroadcastSession(broadcastTestAdminID).stage)
}

func TestBroadcastScheduleFlow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)
	prepareBroadcastSession(t, handler, mockDB)

	// Количество получателей → подтверждение.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_confirm",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	assert.Equal(t, broadcastStageConfirming, handler.getBroadcastSession(broadcastTestAdminID).stage)

	// Переход к планированию: выбор дня.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_schedule",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	s := handler.getBroadcastSession(broadcastTestAdminID)
	require.NotNil(t, s)
	assert.Equal(t, broadcastStageScheduling, s.stage)
	assert.Nil(t, s.plannedAt)
	assert.Equal(t, -1, s.scheduleDay)

	// Выбор дня «Завтра» (offset 1).
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "bsched_day_1",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	assert.Equal(t, 1, handler.getBroadcastSession(broadcastTestAdminID).scheduleDay)

	// Выбор часа 18:00.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "bsched_hour_18",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	s = handler.getBroadcastSession(broadcastTestAdminID)
	require.NotNil(t, s.plannedAt)
	tomorrow := time.Now().AddDate(0, 0, 1)
	assert.Equal(t, tomorrow.Year(), s.plannedAt.Year())
	assert.Equal(t, tomorrow.Month(), s.plannedAt.Month())
	assert.Equal(t, tomorrow.Day(), s.plannedAt.Day())
	assert.Equal(t, 18, s.plannedAt.Hour())

	// Подтверждение создаёт запланированную кампанию.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_final_confirm",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))

	stored, err := mockDB.GetBroadcast(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, stored.PlannedAt, "scheduled campaign must persist planned_at")
	assert.Equal(t, string(database.BroadcastStatusScheduled), stored.Status, "future campaign stays scheduled until its time")
	assert.Contains(t, mockBot.LastSentTextSafe(), "запланирована")
	assert.Nil(t, handler.getBroadcastSession(broadcastTestAdminID), "session must be closed after launch")
}

func TestBroadcastScheduleBackToConfirm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)
	prepareBroadcastSession(t, handler, mockDB)

	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_confirm",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_schedule",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))

	// Из выбора дня — обратно к подтверждению.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "bsched_back",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	assert.Equal(t, broadcastStageConfirming, handler.getBroadcastSession(broadcastTestAdminID).stage)

	// Снова в планирование: день → час → «Изменить время» возвращает к выбору дня.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_schedule",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "bsched_day_2",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "bsched_hour_10",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "bsched_back",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))
	s := handler.getBroadcastSession(broadcastTestAdminID)
	require.NotNil(t, s)
	assert.Equal(t, broadcastStageScheduling, s.stage)
	assert.Nil(t, s.plannedAt, "changing the time must reset planned_at")
	assert.Equal(t, -1, s.scheduleDay)
}

func TestTruncateRunes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", truncateRunes("hello", 0))
	assert.Equal(t, "hello", truncateRunes("hello", 10))
	assert.Equal(t, "hel…", truncateRunes("hello", 3))
	assert.Equal(t, "прив…", truncateRunes("привет", 4), "must not split a UTF-8 rune")
}

func TestFormatIDList(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "—", formatIDList(nil, 15))
	assert.Equal(t, "—", formatIDList([]int64{}, 15))
	assert.Equal(t, "1, 2, 3", formatIDList([]int64{1, 2, 3}, 15))
	assert.Equal(t, "1, 2 … и ещё 3", formatIDList([]int64{1, 2, 3, 4, 5}, 2))
}

func TestFormatErrorList(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "—", formatErrorList(nil, 15))
	one := []database.BroadcastSendError{{TelegramID: 111, Error: "chat not found"}}
	assert.Equal(t, "111 (chat not found)", formatErrorList(one, 15))
	many := []database.BroadcastSendError{
		{TelegramID: 1, Error: strings.Repeat("x", 500)},
		{TelegramID: 2, Error: "boom"},
		{TelegramID: 3, Error: "bang"},
	}
	out := formatErrorList(many, 2)
	assert.Contains(t, out, "… и ещё 1")
	assert.NotContains(t, out, "3 (", "only the visible preview items may be rendered")
}
