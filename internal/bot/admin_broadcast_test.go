package bot

// These tests cover the administrator flow and the durable delivery contract:
// blocked users, unreachable chats, retries, and compact report rendering.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

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

func TestBroadcastDetailsCallbackKeepsFullBlockedListInDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	blocked := make([]int64, 0, 20)
	for i := int64(1); i <= 20; i++ {
		blocked = append(blocked, 800000+i)
	}
	b := &database.Broadcast{Name: "Полный отчёт", MessageText: "Текст", Status: string(database.BroadcastStatusCompleted), BlockedCount: int64(len(blocked))}
	require.NoError(t, b.SetDeliveryReport(&database.BroadcastDeliveryReport{Blocked: blocked}))
	require.NoError(t, mockDB.CreateBroadcast(ctx, b))
	handler := newBroadcastTestHandler(mockDB, mockBot)
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_details_1",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}))

	stored, err := mockDB.GetBroadcast(ctx, b.ID)
	require.NoError(t, err)
	report, err := stored.ParseDeliveryReport()
	require.NoError(t, err)
	assert.Equal(t, blocked, report.Blocked, "the complete blocked list must remain persisted")
	assert.Len(t, mockBot.GetAllSentMessages(), 1, "details must be one compact admin message")
	assert.NotContains(t, mockBot.LastSentTextSafe(), "800020", "the full list must not be sent to the admin")
}

func TestBroadcastErrorClassificationSeparatesBlockedAndUnreachable(t *testing.T) {
	t.Parallel()
	assert.True(t, isUserBlockedError(fmt.Errorf("Forbidden: bot was blocked by the user")))
	assert.False(t, isUserUnreachableError(fmt.Errorf("Forbidden: bot was blocked by the user")))
	assert.False(t, isUserBlockedError(fmt.Errorf("Forbidden: user is deactivated")))
	assert.True(t, isUserUnreachableError(fmt.Errorf("Forbidden: user is deactivated")))
	assert.False(t, isUserBlockedError(fmt.Errorf("Bad Request: chat not found")))
	assert.True(t, isUserUnreachableError(fmt.Errorf("Bad Request: chat not found")))
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

func confirmBroadcast(t *testing.T, handler *Handler, mockDB *testutil.DatabaseService) {
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

	// Sending is intentionally asynchronous. Wait for the durable worker result
	// instead of relying on a timing-sensitive sleep.
	require.Eventually(t, func() bool {
		broadcasts, err := mockDB.ListBroadcasts(ctx, 10)
		return err == nil && len(broadcasts) == 1 && broadcasts[0].Status == string(database.BroadcastStatusCompleted)
	}, 5*time.Second, 10*time.Millisecond)
}

func TestBroadcast_DuplicateFinalConfirmCreatesOneCampaign(t *testing.T) {
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

	update := tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{
		From: broadcastTestAdmin(), Data: "broadcast_final_confirm",
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1},
	}}
	var wg sync.WaitGroup
	var callbackErr error
	var callbackMu sync.Mutex
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := handler.HandleCallback(ctx, update); err != nil {
				callbackMu.Lock()
				callbackErr = err
				callbackMu.Unlock()
			}
		}()
	}
	wg.Wait()
	assert.NoError(t, callbackErr)

	require.Eventually(t, func() bool {
		broadcasts, err := mockDB.ListBroadcasts(ctx, 10)
		return err == nil && len(broadcasts) == 1
	}, 5*time.Second, 10*time.Millisecond)
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

	confirmBroadcast(t, handler, mockDB)

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

func TestBroadcast_CorruptFilterBecomesFailed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)
	b := &database.Broadcast{Name: "broken", Filters: "not-json", MessageText: "text", Status: string(database.BroadcastStatusScheduled)}
	require.NoError(t, mockDB.CreateBroadcast(ctx, b))
	err := handler.broadcastWorker.processCampaign(ctx, b)
	require.Error(t, err)
	stored, getErr := mockDB.GetBroadcast(ctx, b.ID)
	require.NoError(t, getErr)
	assert.Equal(t, string(database.BroadcastStatusFailed), stored.Status)
}

func TestBroadcast_ResultPersistenceFailureLeavesCampaignResumable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)
	prepareBroadcastSession(t, handler, mockDB)
	mockDB.FinishBroadcastRecipientFunc = func(ctx context.Context, broadcastID uint, id uint, attempts int, status database.BroadcastRecipientStatus, lastError string, now time.Time) error {
		return fmt.Errorf("state storage unavailable")
	}
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{From: broadcastTestAdmin(), Data: "broadcast_confirm", Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1}}}))
	require.NoError(t, handler.HandleCallback(ctx, tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{From: broadcastTestAdmin(), Data: "broadcast_final_confirm", Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: broadcastTestAdminID}, MessageID: 1}}}))
	require.Eventually(t, func() bool {
		broadcasts, err := mockDB.ListBroadcasts(ctx, 10)
		return err == nil && len(broadcasts) == 1 && broadcasts[0].Status == string(database.BroadcastStatusRunning)
	}, 5*time.Second, 10*time.Millisecond)
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

	confirmBroadcast(t, handler, mockDB)

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

func TestBroadcast_UnreachableUserIsNotReportedAsBlocked(t *testing.T) {
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
			return tgbotapi.Message{}, fmt.Errorf("Bad Request: chat not found")
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	confirmBroadcast(t, handler, mockDB)
	assert.Equal(t, 1, sendCalls, "permanent unreachable errors must not be retried")

	broadcasts, err := mockDB.ListBroadcasts(ctx, 10)
	require.NoError(t, err)
	require.Len(t, broadcasts, 1)
	assert.Equal(t, int64(0), broadcasts[0].BlockedCount)
	assert.Equal(t, int64(1), broadcasts[0].UnreachableCount)
	report, err := broadcasts[0].ParseDeliveryReport()
	require.NoError(t, err)
	assert.Empty(t, report.Blocked)
	assert.Equal(t, []int64{111}, report.Unreachable)
}

func TestBroadcast_ProcessFailurePersistsRetryMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)
	campaign := &database.Broadcast{Name: "snapshot retry", MessageText: "text", Status: string(database.BroadcastStatusScheduled)}
	require.NoError(t, mockDB.CreateBroadcast(ctx, campaign))
	mockDB.SnapshotBroadcastRecipientsFunc = func(context.Context, uint, database.BroadcastFilter) (int64, error) {
		return 0, fmt.Errorf("snapshot storage unavailable")
	}

	err := handler.broadcastWorker.processCampaign(ctx, campaign)
	require.Error(t, err)
	stored, getErr := mockDB.GetBroadcast(ctx, campaign.ID)
	require.NoError(t, getErr)
	assert.Equal(t, string(database.BroadcastStatusRunning), stored.Status)
	assert.Equal(t, 1, stored.RetryCount)
	assert.Contains(t, stored.LastError, "snapshot storage unavailable")
	assert.NotNil(t, stored.RetryAt)
}

func TestBroadcast_PlannedResumeDoesNotCountAsRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)
	campaign := &database.Broadcast{Name: "resume", MessageText: "text", Status: string(database.BroadcastStatusScheduled)}
	require.NoError(t, mockDB.CreateBroadcast(ctx, campaign))

	// One recipient already delivered, one still in flight: the campaign ends
	// its time slice incomplete and must resume on the next pass without being
	// counted as a failed launch.
	mockDB.ClaimBroadcastRecipientsFunc = func(context.Context, uint, time.Time, int) ([]database.BroadcastRecipient, error) {
		return nil, nil
	}
	mockDB.GetBroadcastRecipientsStatsFunc = func(context.Context, uint) (total, sent, blocked, unreachable, failed int64, report database.BroadcastDeliveryReport, err error) {
		return 2, 1, 0, 0, 0, database.BroadcastDeliveryReport{Delivered: []int64{710001}}, nil
	}

	err := handler.broadcastWorker.processCampaign(ctx, campaign)
	require.ErrorIs(t, err, errBroadcastIncomplete)

	stored, getErr := mockDB.GetBroadcast(ctx, campaign.ID)
	require.NoError(t, getErr)
	assert.Equal(t, string(database.BroadcastStatusRunning), stored.Status, "incomplete campaign stays resumable")
	assert.Zero(t, stored.RetryCount, "a planned resume must not count as a retry")
	assert.Nil(t, stored.RetryAt)
}

func TestBroadcast_ScheduleRetryPersistsBackoffMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockDB := testutil.NewDatabaseService()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)
	campaign := &database.Broadcast{Name: "retry", MessageText: "text", Status: string(database.BroadcastStatusScheduled)}
	require.NoError(t, mockDB.CreateBroadcast(ctx, campaign))

	before := time.Now().UTC()
	require.NoError(t, handler.broadcastWorker.scheduleRetry(ctx, campaign.ID, fmt.Errorf("database unavailable")))
	stored, err := mockDB.GetBroadcast(ctx, campaign.ID)
	require.NoError(t, err)
	assert.Equal(t, string(database.BroadcastStatusRunning), stored.Status)
	assert.Equal(t, 1, stored.RetryCount)
	assert.Contains(t, stored.LastError, "database unavailable")
	require.NotNil(t, stored.RetryAt)
	assert.True(t, stored.RetryAt.After(before.Add(4*time.Second)))
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

	confirmBroadcast(t, handler, mockDB)

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
	confirmBroadcast(t, handler, mockDB)

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
