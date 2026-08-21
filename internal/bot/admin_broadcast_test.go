package bot

import (
	"context"
	"fmt"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const broadcastTestAdminID = int64(123456)

func newBroadcastTestHandler(mockDB *testutil.DatabaseService, mockBot *testutil.BotAPI) *Handler {
	cfg := &config.Config{TelegramAdminID: broadcastTestAdminID}
	return NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
}

func broadcastTestAdmin() *tgbotapi.User {
	return &tgbotapi.User{ID: broadcastTestAdminID, UserName: "admin"}
}

func TestBroadcastDetailsCallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()

	require.NoError(t, mockDB.CreateBroadcast(ctx, &database.Broadcast{Name: "Кнопка", MessageText: "Текст", Status: string(database.BroadcastStatusCompleted), SentCount: 7}))

	handler := newBroadcastTestHandler(mockDB, mockBot)
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: broadcastTestAdmin(),
			Data: "broadcast_details_1",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: broadcastTestAdminID},
				MessageID: 1,
			},
		},
	}))

	assert.Contains(t, mockBot.LastSentTextSafe(), "Кнопка")
	assert.Contains(t, mockBot.LastSentTextSafe(), "7")
}

// prepareBroadcastSession drives the flow up to the confirm callback: the
// preview send must succeed before SendFunc is installed by the test.
func prepareBroadcastSession(t *testing.T, handler *Handler, mockDB *testutil.DatabaseService) {
	t.Helper()

	ctx := context.Background()

	mockDB.GetTelegramIDsBatchFunc = func(ctx context.Context, offset, limit int) ([]int64, error) {
		if offset > 0 {
			return nil, nil
		}
		return []int64{111}, nil
	}

	handler.HandleBroadcast(ctx, createCommandUpdate(broadcastTestAdminID, broadcastTestAdmin(), "/broadcast"))
	handler.HandleBroadcastDraft(ctx, createTextUpdate(broadcastTestAdmin(), "Тестовая рассылка"))
	handler.HandleBroadcastDraft(ctx, createTextUpdate(broadcastTestAdmin(), "Привет всем!"))
}

func confirmBroadcast(t *testing.T, handler *Handler) {
	t.Helper()

	ctx := context.Background()

	// Шаг 1: показать количество получателей.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: broadcastTestAdmin(),
			Data: "broadcast_confirm",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: broadcastTestAdminID},
				MessageID: 1,
			},
		},
	}))

	// Шаг 2: подтвердить и запустить рассылку.
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: broadcastTestAdmin(),
			Data: "broadcast_final_confirm",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: broadcastTestAdminID},
				MessageID: 1,
			},
		},
	}))
}

func TestBroadcast_TransientFailureRetriedThenDelivered(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)

	prepareBroadcastSession(t, handler, mockDB)

	// Первая попытка отправки пользователю падает с временной ошибкой, повтор
	// успешен. Считаем только вызовы к получателю (111) — финальный отчёт
	// админу тоже проходит через SendFunc.
	var sendCalls int
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok && mc.ChatID == 111 {
			sendCalls++
			if sendCalls == 1 {
				return tgbotapi.Message{}, fmt.Errorf("network error")
			}
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	confirmBroadcast(t, handler)

	assert.Equal(t, 2, sendCalls, "one failed attempt + one retry")

	broadcasts, err := mockDB.ListBroadcasts(ctx, 10)
	require.NoError(t, err)
	require.Len(t, broadcasts, 1)

	b := broadcasts[0]
	assert.Equal(t, string(database.BroadcastStatusCompleted), b.Status)
	assert.Equal(t, int64(1), b.SentCount)
	assert.Equal(t, int64(0), b.FailedCount)

	report, err := b.ParseDeliveryReport()
	require.NoError(t, err)
	assert.Equal(t, []int64{111}, report.Delivered)
}

func TestBroadcast_RetriesExhaustedRecordsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)

	prepareBroadcastSession(t, handler, mockDB)

	// Постоянная временная ошибка: все повторы исчерпаны, ошибка в отчёте.
	// Считаем только вызовы к получателю (111).
	var sendCalls int
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok && mc.ChatID == 111 {
			sendCalls++
			return tgbotapi.Message{}, fmt.Errorf("network error")
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	confirmBroadcast(t, handler)

	assert.Equal(t, 1+2, sendCalls, "initial attempt + 2 retries")

	broadcasts, err := mockDB.ListBroadcasts(ctx, 10)
	require.NoError(t, err)
	require.Len(t, broadcasts, 1)

	b := broadcasts[0]
	assert.Equal(t, string(database.BroadcastStatusCompleted), b.Status)
	assert.Equal(t, int64(0), b.SentCount)
	assert.Equal(t, int64(1), b.FailedCount)

	report, err := b.ParseDeliveryReport()
	require.NoError(t, err)
	require.Len(t, report.Errors, 1)
	assert.Equal(t, int64(111), report.Errors[0].TelegramID)
	assert.Contains(t, report.Errors[0].Error, "network error")
}

func TestBroadcast_BlockedUserNeverRetried(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)

	prepareBroadcastSession(t, handler, mockDB)

	var sendCalls int
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok && mc.ChatID == 111 {
			sendCalls++
			return tgbotapi.Message{}, fmt.Errorf("Forbidden: bot was blocked by the user")
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	confirmBroadcast(t, handler)

	assert.Equal(t, 1, sendCalls, "blocked error must not be retried")

	broadcasts, err := mockDB.ListBroadcasts(ctx, 10)
	require.NoError(t, err)
	require.Len(t, broadcasts, 1)

	b := broadcasts[0]
	assert.Equal(t, string(database.BroadcastStatusCompleted), b.Status)
	assert.Equal(t, int64(1), b.BlockedCount)
	assert.Equal(t, int64(0), b.FailedCount)

	report, err := b.ParseDeliveryReport()
	require.NoError(t, err)
	assert.Equal(t, []int64{111}, report.Blocked)
	assert.Empty(t, report.Errors)
}

func TestBroadcast_ConfirmCreatesBroadcastAndShowsHeader(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)

	prepareBroadcastSession(t, handler, mockDB)
	confirmBroadcast(t, handler)

	assert.Contains(t, mockBot.LastSentTextSafe(), "Рассылка завершена")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Рассылка #1")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Тестовая рассылка")

	broadcasts, err := mockDB.ListBroadcasts(ctx, 10)
	require.NoError(t, err)
	require.Len(t, broadcasts, 1)
	assert.Equal(t, "Тестовая рассылка", broadcasts[0].Name)
	assert.Equal(t, "Привет всем!", broadcasts[0].MessageText)
	assert.Equal(t, string(database.BroadcastStatusCompleted), broadcasts[0].Status)
	assert.Equal(t, int64(1), broadcasts[0].RecipientsTotal)
	assert.Equal(t, int64(1), broadcasts[0].SentCount)
}

func TestBroadcast_ConfirmDBFailureAborts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)

	mockDB.CreateBroadcastFunc = func(ctx context.Context, b *database.Broadcast) error {
		return fmt.Errorf("db is down")
	}

	prepareBroadcastSession(t, handler, mockDB)

	// Шаг 1: показать количество получателей (успешно).
	err := handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: broadcastTestAdmin(),
			Data: "broadcast_confirm",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: broadcastTestAdminID},
				MessageID: 1,
			},
		},
	})
	require.NoError(t, err)

	// Шаг 2: Ошибка DB-фазы — CreateBroadcast падает, рассылка отменена.
	err = handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			From: broadcastTestAdmin(),
			Data: "broadcast_final_confirm",
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: broadcastTestAdminID},
				MessageID: 1,
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, mockBot.LastSentTextSafe(), "Не удалось сохранить рассылку")

	broadcasts, err := mockDB.ListBroadcasts(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, broadcasts)
}
