package bot

// G1: HandleDel coverage — argument validation, admin gate, audit-context
// propagation, and post-success side effects (cache invalidation + referral
// decrement only after a successful deletion).

import (
	"context"
	"errors"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleDel_ArgumentValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     string
		wantText string
	}{
		{"no arguments", "/del", "Использование: /del"},
		{"invalid format", "/del abc", "Неверный формат ID"},
		{"zero id", "/del 0", "ID должен быть положительным"},
		{"negative id", "/del -5", "ID должен быть положительным"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{TelegramAdminID: 123456}
			mockDB := testutil.NewBotStoreFake()
			mockBot := testutil.NewBotAPI()
			handler := newTestHandlerWithSubService(t, cfg, mockDB, testutil.NewXUIClient(), mockBot)

			ctx := context.Background()
			admin := &tgbotapi.User{ID: 123456, UserName: "admin"}
			require.NoError(t, handler.HandleDel(ctx, createCommandUpdate(1, admin, tt.args)))

			assert.True(t, mockBot.SendCalledSafe(), "validation failure must be reported to the admin")
			assert.Contains(t, mockBot.LastSentTextSafe(), tt.wantText)
		})
	}
}

func TestHandleDel_AdminGate(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, testutil.NewXUIClient(), mockBot)

	deleteCalled := false
	mockDB.DeleteSubscriptionByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		deleteCalled = true

		return nil, errors.New("must not be called")
	}

	ctx := context.Background()
	nonAdmin := &tgbotapi.User{ID: 999, UserName: "intruder"}
	require.NoError(t, handler.HandleDel(ctx, createCommandUpdate(1, nonAdmin, "/del 5")))

	assert.False(t, deleteCalled, "non-admin must never reach the delete service call")
	assert.False(t, mockBot.SendCalledSafe(), "non-admin gets a silent ignore")
}

func TestHandleDel_ServiceError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, testutil.NewXUIClient(), mockBot)

	// Row loaded before the failed delete carries referred_by — the count must
	// survive untouched when deletion fails.
	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return &database.Subscription{ID: id, TelegramID: 777, Username: "victim", ReferredBy: testutil.PtrInt64(999999)}, nil
	}
	mockDB.UpdateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		return errors.New("database error")
	}

	// Pre-seed the side effects: they must stay untouched when deletion fails.
	victim := &database.Subscription{TelegramID: 777, Username: "victim", Status: "active"}
	handler.cache.Set(777, victim)
	handler.IncrementReferralCount(999999)

	ctx := context.Background()
	admin := &tgbotapi.User{ID: 123456, UserName: "admin"}
	err := handler.HandleDel(ctx, createCommandUpdate(1, admin, "/del 5"))

	require.Error(t, err, "delete failure must surface to the command layer")
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка удаления подписки")
	assert.NotNil(t, handler.cache.Get(777), "cache must be kept when deletion fails")
	assert.Equal(t, int64(1), handler.GetReferralCount(999999),
		"referral count must not be decremented when deletion fails")
}

func TestHandleDel_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, testutil.NewXUIClient(), mockBot)

	var gotAuditAdmin int64
	gotAuditCtx := false

	// DeleteByID возвращает строку, загруженную GetByID (в проде — полная строка
	// с referred_by), поэтому ReferredBy должен стоять здесь.
	mockDB.GetByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		return &database.Subscription{ID: id, TelegramID: 777, Username: "victim", ReferredBy: testutil.PtrInt64(999999)}, nil
	}
	mockDB.UpdateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription) error {
		return nil
	}
	mockDB.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return nil, nil
	}
	mockDB.DeleteSubscriptionByIDFunc = func(ctx context.Context, id uint) (*database.Subscription, error) {
		// WithAuditAdmin must be propagated by HandleDel so the audit event
		// records the acting admin.
		if v, ok := ctx.Value(service.AuditAdminIDKey).(int64); ok {
			gotAuditCtx = true
			gotAuditAdmin = v
		}

		return &database.Subscription{
			ID:         id,
			TelegramID: 777,
			Username:   "victim",
		}, nil
	}

	// Pre-seed side effects: both must be cleared by the successful delete.
	victim := &database.Subscription{TelegramID: 777, Username: "victim", Status: "active"}
	handler.cache.Set(777, victim)
	handler.IncrementReferralCount(999999)

	ctx := context.Background()
	admin := &tgbotapi.User{ID: 123456, UserName: "admin"}
	require.NoError(t, handler.HandleDel(ctx, createCommandUpdate(1, admin, "/del 5")))

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "✅ Подписка успешно удалена")
	assert.True(t, gotAuditCtx, "HandleDel must attach the admin audit context")
	assert.Equal(t, cfg.TelegramAdminID, gotAuditAdmin, "audit actor must be the admin's Telegram ID")
	assert.Nil(t, handler.cache.Get(777), "cache must be invalidated after successful deletion")
	assert.Equal(t, int64(0), handler.GetReferralCount(999999),
		"referral count must be decremented after successful deletion")
}
