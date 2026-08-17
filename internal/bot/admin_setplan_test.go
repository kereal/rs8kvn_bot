package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestHandleSetPlan_AdminOnly(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, testutil.NewXUIClient(), mockBot)

	user := &tgbotapi.User{ID: 999, UserName: "notadmin"}
	update := createCommandUpdate(1, user, "/setplan 5 3 30")

	err := handler.HandleSetPlan(context.Background(), update)
	require.NoError(t, err)
	assert.False(t, mockBot.SendCalledSafe(), "non-admin must not get a reply")
}

func TestHandleSetPlan_UsageAndValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cmd     string
		wantMsg string
	}{
		{name: "no_args", cmd: "/setplan", wantMsg: "Использование: /setplan"},
		{name: "missing_plan", cmd: "/setplan 5", wantMsg: "Использование: /setplan"},
		{name: "invalid_sub_id", cmd: "/setplan abc 3", wantMsg: "Неверный ID подписки"},
		{name: "zero_sub_id", cmd: "/setplan 0 3", wantMsg: "Неверный ID подписки"},
		{name: "invalid_plan_id", cmd: "/setplan 5 abc", wantMsg: "Неверный ID тарифа"},
		{name: "invalid_days", cmd: "/setplan 5 3 abc", wantMsg: "Количество дней должно быть положительным"},
		{name: "zero_days", cmd: "/setplan 5 3 0", wantMsg: "Количество дней должно быть положительным"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{TelegramAdminID: 123456}
			mockDB := testutil.NewDatabaseService()
			mockBot := testutil.NewBotAPI()
			handler := newTestAdminHandler(cfg, mockDB, testutil.NewXUIClient(), mockBot)

			admin := &tgbotapi.User{ID: 123456, UserName: "admin"}
			update := createCommandUpdate(1, admin, tt.cmd)

			err := handler.HandleSetPlan(context.Background(), update)
			require.NoError(t, err)
			assert.True(t, mockBot.SendCalledSafe())
			assert.Contains(t, mockBot.LastSentText, tt.wantMsg)
		})
	}
}

func TestHandleSetPlan_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, testutil.NewXUIClient(), mockBot)

	sub := &database.Subscription{
		ID:             5,
		TelegramID:     777,
		Username:       "planuser",
		ClientID:       "c-planuser",
		SubscriptionID: "s-planuser",
		Status:         string(database.SubscriptionStatusActive),
		PlanID:         1,
		ExpiresAt:      testutil.PtrTime(time.Now().Add(10 * 24 * time.Hour)),
	}
	updateCalled := false

	mockDB.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
		return sub, nil
	}
	mockDB.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return &database.Plan{ID: 3, Name: "premium"}, nil
	}
	mockDB.UpdateSubscriptionFunc = func(_ context.Context, updated *database.Subscription) error {
		updateCalled = true
		assert.Equal(t, uint(3), updated.PlanID)
		assert.Equal(t, string(database.SubscriptionStatusActive), updated.Status)
		return nil
	}

	admin := &tgbotapi.User{ID: 123456, UserName: "admin"}
	update := createCommandUpdate(1, admin, "/setplan 5 3 30")

	err := handler.HandleSetPlan(context.Background(), update)
	require.NoError(t, err)

	assert.True(t, updateCalled, "service must persist the plan change")
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentText, "Тариф подписки изменён")
	assert.Contains(t, mockBot.LastSentText, "Тариф: 3")
}

func TestHandleSetPlan_ServiceError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newTestAdminHandler(cfg, mockDB, testutil.NewXUIClient(), mockBot)

	planErr := errors.New("plan not found")
	mockDB.GetByIDFunc = func(context.Context, uint) (*database.Subscription, error) {
		return &database.Subscription{ID: 5, TelegramID: 777, ClientID: "c", SubscriptionID: "s", Status: "active"}, nil
	}
	mockDB.GetPlanByIDFunc = func(context.Context, uint) (*database.Plan, error) {
		return nil, planErr
	}

	admin := &tgbotapi.User{ID: 123456, UserName: "admin"}
	update := createCommandUpdate(1, admin, "/setplan 5 99 30")

	err := handler.HandleSetPlan(context.Background(), update)
	require.Error(t, err)
	assert.ErrorIs(t, err, planErr)
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentText, "Ошибка смены тарифа")
}
