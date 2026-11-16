package bot

// Юнит-тесты durable-воркера рассылок и классификации ошибок доставки.
// Восстановлены после переноса UI /broadcast в админ-панель: покрывают
// живой код воркера (processCampaign, scheduleRetry, resume-чанков, flood/
// blocked/unreachable-классификацию), сея recipient-state напрямую в БД без
// UI-флоу черновика (которого больше нет).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

func newBroadcastTestHandler(mockDB *testutil.BotStoreFake, mockBot *testutil.BotAPI) *Handler {
	cfg := &config.Config{TelegramAdminID: broadcastTestAdminID}
	return NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")
}

// newBroadcastSQLiteHandler wires a handler to a real SQLite service: the
// recipient lease machine (Snapshot/Claim/Finish/Progress/Stats) is domain
// logic the in-memory fake deliberately does not mirror (ADR-0001).
func newBroadcastSQLiteHandler(t *testing.T, mockBot *testutil.BotAPI) (*Handler, *database.Service) {
	t.Helper()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})
	cfg := &config.Config{TelegramAdminID: broadcastTestAdminID}
	return NewHandler(mockBot, cfg, db, NewTestBotConfig(), nil, ""), db
}

// stubBroadcastRecipientLease scripts the recipient lease machine on the fake:
// one recipient (telegram 111) claimed once, otherwise-successful
// finish/stats. Tests override individual Funcs for failure injection.
func stubBroadcastRecipientLease(mockDB *testutil.BotStoreFake) {
	claimed := false
	mockDB.SnapshotBroadcastRecipientsFunc = func(context.Context, uint, database.BroadcastFilter) (int64, error) {
		return 1, nil
	}
	mockDB.RecoverStaleBroadcastRecipientsFunc = func(context.Context, uint, time.Time) error {
		return nil
	}
	mockDB.ClaimBroadcastRecipientsFunc = func(_ context.Context, broadcastID uint, now time.Time, _ int) ([]database.BroadcastRecipient, error) {
		if claimed {
			return nil, nil
		}
		claimed = true
		return []database.BroadcastRecipient{{
			ID: 1, BroadcastID: broadcastID, TelegramID: 111,
			Status: database.BroadcastRecipientSending, Attempts: 1, UpdatedAt: now,
		}}, nil
	}
	mockDB.FinishBroadcastRecipientFunc = func(context.Context, uint, uint, int, database.BroadcastRecipientStatus, string, time.Time) error {
		return nil
	}
	mockDB.GetBroadcastRecipientsStatsFunc = func(context.Context, uint) (total, sent, blocked, unreachable, failed int64, report database.BroadcastDeliveryReport, err error) {
		return 1, 1, 0, 0, 0, database.BroadcastDeliveryReport{Delivered: []int64{111}}, nil
	}
}

// broadcastRecipientsStateJSON удобно собирает recipients_state из снапшота.
func broadcastRecipientsStateJSON(t *testing.T, recipients []database.BroadcastRecipient) string {
	t.Helper()

	raw, err := json.Marshal(map[string]any{"snapshot": true, "recipients": recipients})
	require.NoError(t, err)

	return string(raw)
}

// multiChunkText возвращает текст из трёх чанков по config.MaxTelegramMessageLen
// байт, чтобы проверить возобновление отправки с сохранённого чанка.
func multiChunkText() string {
	return strings.Repeat("1", config.MaxTelegramMessageLen) +
		strings.Repeat("2", config.MaxTelegramMessageLen) +
		strings.Repeat("3", config.MaxTelegramMessageLen)
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

func TestBroadcastFloodClassificationAndRetryDelay(t *testing.T) {
	t.Parallel()
	// 429 from Telegram is a transient flood error, not a permanent delivery failure.
	flood := &tgbotapi.Error{Code: 429, Message: "Too Many Requests: retry after some time"}
	assert.True(t, isFloodError(flood))
	assert.False(t, isUserBlockedError(flood))
	assert.False(t, isUserUnreachableError(flood))
	// Plain infrastructure errors are not flood errors.
	assert.False(t, isFloodError(fmt.Errorf("network error")))
	// A small hint is honoured verbatim.
	small := &tgbotapi.Error{Code: 429, Message: "flood", ResponseParameters: tgbotapi.ResponseParameters{RetryAfter: 3}}
	assert.Equal(t, 3*time.Second, floodRetryDelay(small))
	// A huge hint is clamped so one delay cannot consume the whole time slice.
	huge := &tgbotapi.Error{Code: 429, Message: "flood", ResponseParameters: tgbotapi.ResponseParameters{RetryAfter: int(broadcastFloodMaxDelay.Seconds()) + 9999}}
	assert.Equal(t, broadcastFloodMaxDelay, floodRetryDelay(huge))
	// Missing hint falls back to the default delay.
	assert.Equal(t, broadcastFloodDefaultDelay, floodRetryDelay(flood))
}

func TestBroadcast_CorruptFilterBecomesFailed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockDB := testutil.NewBotStoreFake()
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

func TestBroadcast_ProcessFailurePersistsRetryMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockDB := testutil.NewBotStoreFake()
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
	mockBot := testutil.NewBotAPI()
	handler, db := newBroadcastSQLiteHandler(t, mockBot)

	state := broadcastRecipientsStateJSON(t, []database.BroadcastRecipient{
		{ID: 1, TelegramID: 710001, Status: database.BroadcastRecipientSent, UpdatedAt: time.Now().UTC()},
		{ID: 2, TelegramID: 710002, Status: database.BroadcastRecipientSending, UpdatedAt: time.Now().UTC()},
	})
	campaign := &database.Broadcast{Name: "resume", MessageText: "text", Status: string(database.BroadcastStatusScheduled), RecipientsState: state}
	require.NoError(t, db.CreateBroadcast(ctx, campaign))

	err := handler.broadcastWorker.processCampaign(ctx, campaign)
	require.ErrorIs(t, err, errBroadcastIncomplete)

	stored, getErr := db.GetBroadcast(ctx, campaign.ID)
	require.NoError(t, getErr)
	assert.Equal(t, string(database.BroadcastStatusRunning), stored.Status, "incomplete campaign stays resumable")
	assert.Zero(t, stored.RetryCount, "a planned resume must not count as a retry")
	assert.Nil(t, stored.RetryAt)
}

func TestBroadcast_RecipientResumesFromSavedChunk(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockBot := testutil.NewBotAPI()
	handler, db := newBroadcastSQLiteHandler(t, mockBot)

	// Первый чанк уже был доставлен до краша: в снапшоте сохранён NextChunk=1.
	state := broadcastRecipientsStateJSON(t, []database.BroadcastRecipient{
		{ID: 1, TelegramID: 111, Status: database.BroadcastRecipientPending, NextChunk: 1, UpdatedAt: time.Now().UTC()},
	})
	b := &database.Broadcast{
		Name: "resume", MessageText: multiChunkText(),
		Status: string(database.BroadcastStatusScheduled), RecipientsState: state,
	}
	require.NoError(t, db.CreateBroadcast(ctx, b))

	var sends []string
	var mu sync.Mutex
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok && mc.ChatID == 111 {
			mu.Lock()
			sends = append(sends, mc.Text)
			mu.Unlock()
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	require.NoError(t, handler.broadcastWorker.processCampaign(ctx, b))

	require.Len(t, sends, 2, "первый чанк уже доставлен — отправляются только второй и третий")
	assert.Contains(t, sends[0], strings.Repeat("2", 64), "отправка возобновляется со второго чанка")
	assert.Contains(t, sends[1], strings.Repeat("3", 64))

	stored, err := db.GetBroadcast(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, string(database.BroadcastStatusCompleted), stored.Status)
	assert.Equal(t, int64(1), stored.SentCount)
}

func TestBroadcast_ProgressPersistedAfterEachChunk(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := newBroadcastTestHandler(mockDB, mockBot)
	stubBroadcastRecipientLease(mockDB)

	state := broadcastRecipientsStateJSON(t, []database.BroadcastRecipient{
		{ID: 1, TelegramID: 111, Status: database.BroadcastRecipientPending, UpdatedAt: time.Now().UTC()},
	})
	b := &database.Broadcast{
		Name: "progress", MessageText: multiChunkText(),
		Status: string(database.BroadcastStatusScheduled), RecipientsState: state,
	}
	require.NoError(t, mockDB.CreateBroadcast(ctx, b))

	var progressMu sync.Mutex
	var checkpoints []int
	mockDB.UpdateBroadcastRecipientProgressFunc = func(ctx context.Context, broadcastID uint, id uint, expectedAttempts int, nextChunk int, now time.Time) error {
		progressMu.Lock()
		checkpoints = append(checkpoints, nextChunk)
		progressMu.Unlock()
		return nil
	}
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		return tgbotapi.Message{MessageID: 1}, nil
	}

	require.NoError(t, handler.broadcastWorker.processCampaign(ctx, b))

	assert.Equal(t, []int{1, 2, 3}, checkpoints, "прогресс фиксируется после каждого доставленного чанка")
}

func TestBroadcast_ScheduleRetryPersistsBackoffMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mockDB := testutil.NewBotStoreFake()
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

// seedBroadcastAudience создаёт активные подписки, которые попадают в
// снапшот аудитории по пустому фильтру.
func seedBroadcastAudience(t *testing.T, db *database.Service, ids ...int64) {
	t.Helper()

	ctx := context.Background()
	for _, id := range ids {
		sub := testutil.CreateTestSubscription(id, fmt.Sprintf("user%d", id), string(database.SubscriptionStatusActive), testutil.PtrTime(time.Now().Add(24*time.Hour)))
		require.NoError(t, db.CreateSubscription(ctx, sub, ""))
	}
}

// processCampaignToCompletion прогоняет воркер по пустой-фильтровой кампании
// с одним получателем из снапшота аудитории и возвращает финальное состояние.
func processCampaignToCompletion(t *testing.T, handler *Handler, db *database.Service, text string) (*database.Broadcast, error) {
	t.Helper()

	ctx := context.Background()
	b := &database.Broadcast{
		Name: "delivery", MessageText: text,
		Filters: "{}", Status: string(database.BroadcastStatusScheduled),
	}
	require.NoError(t, db.CreateBroadcast(ctx, b))

	err := handler.broadcastWorker.processCampaign(ctx, b)
	stored, getErr := db.GetBroadcast(ctx, b.ID)
	require.NoError(t, getErr)
	return stored, err
}

func TestBroadcast_FloodWaitsRetryAfterThenDelivers(t *testing.T) {
	t.Parallel()

	mockBot := testutil.NewBotAPI()
	handler, db := newBroadcastSQLiteHandler(t, mockBot)

	seedBroadcastAudience(t, db, 111)

	// Telegram rate-limits the bot once (429, retry_after 1s). Delivery must wait
	// out the hint and deliver WITHOUT spending a normal retry.
	var sendCalls int
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok && mc.ChatID == 111 && mc.Text != "" {
			sendCalls++
			if sendCalls == 1 {
				return tgbotapi.Message{}, &tgbotapi.Error{Code: 429, Message: "Too Many Requests", ResponseParameters: tgbotapi.ResponseParameters{RetryAfter: 1}}
			}
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	stored, err := processCampaignToCompletion(t, handler, db, "привет")
	require.NoError(t, err)
	assert.Equal(t, 2, sendCalls, "one flood wait + one successful send, normal retry not consumed")
	assert.Equal(t, string(database.BroadcastStatusCompleted), stored.Status)
	assert.Equal(t, int64(1), stored.SentCount)
	assert.Equal(t, int64(0), stored.FailedCount)
	report, err := stored.ParseDeliveryReport()
	require.NoError(t, err)
	assert.Equal(t, []int64{111}, report.Delivered)
}

func TestBroadcast_TransientFailureRetriedThenDelivered(t *testing.T) {
	t.Parallel()

	mockBot := testutil.NewBotAPI()
	handler, db := newBroadcastSQLiteHandler(t, mockBot)

	seedBroadcastAudience(t, db, 111)

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

	stored, err := processCampaignToCompletion(t, handler, db, "привет")
	require.NoError(t, err)
	assert.Equal(t, 2, sendCalls, "one failed attempt + one retry")
	assert.Equal(t, string(database.BroadcastStatusCompleted), stored.Status)
	assert.Equal(t, int64(1), stored.SentCount)
	assert.Equal(t, int64(0), stored.FailedCount)
	report, err := stored.ParseDeliveryReport()
	require.NoError(t, err)
	assert.Equal(t, []int64{111}, report.Delivered)
}

func TestBroadcast_RetriesExhaustedRecordsError(t *testing.T) {
	t.Parallel()

	mockBot := testutil.NewBotAPI()
	handler, db := newBroadcastSQLiteHandler(t, mockBot)

	seedBroadcastAudience(t, db, 111)

	var sendCalls int
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok && mc.ChatID == 111 {
			sendCalls++
			return tgbotapi.Message{}, fmt.Errorf("network error")
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	stored, err := processCampaignToCompletion(t, handler, db, "привет")
	require.NoError(t, err)
	assert.Equal(t, 1+broadcastRetries, sendCalls, "initial attempt + retries")
	assert.Equal(t, string(database.BroadcastStatusCompleted), stored.Status)
	assert.Equal(t, int64(0), stored.SentCount)
	assert.Equal(t, int64(1), stored.FailedCount)
	report, err := stored.ParseDeliveryReport()
	require.NoError(t, err)
	require.Len(t, report.Errors, 1)
	assert.Equal(t, int64(111), report.Errors[0].TelegramID)
	assert.Contains(t, report.Errors[0].Error, "network error")
}

func TestBroadcast_UnreachableUserIsNotReportedAsBlocked(t *testing.T) {
	t.Parallel()

	mockBot := testutil.NewBotAPI()
	handler, db := newBroadcastSQLiteHandler(t, mockBot)
	seedBroadcastAudience(t, db, 111)

	var sendCalls int
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok && mc.ChatID == 111 {
			sendCalls++
			return tgbotapi.Message{}, fmt.Errorf("Bad Request: chat not found")
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	stored, err := processCampaignToCompletion(t, handler, db, "привет")
	require.NoError(t, err)
	assert.Equal(t, 1, sendCalls, "permanent unreachable errors must not be retried")
	assert.Equal(t, int64(0), stored.BlockedCount)
	assert.Equal(t, int64(1), stored.UnreachableCount)
	report, err := stored.ParseDeliveryReport()
	require.NoError(t, err)
	assert.Empty(t, report.Blocked)
	assert.Equal(t, []int64{111}, report.Unreachable)
}

func TestBroadcast_BlockedUserNeverRetried(t *testing.T) {
	t.Parallel()

	mockBot := testutil.NewBotAPI()
	handler, db := newBroadcastSQLiteHandler(t, mockBot)
	seedBroadcastAudience(t, db, 111)

	var sendCalls int
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok && mc.ChatID == 111 {
			sendCalls++
			return tgbotapi.Message{}, fmt.Errorf("Forbidden: bot was blocked by the user")
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	stored, err := processCampaignToCompletion(t, handler, db, "привет")
	require.NoError(t, err)
	assert.Equal(t, 1, sendCalls, "blocked error must not be retried")
	assert.Equal(t, string(database.BroadcastStatusCompleted), stored.Status)
	assert.Equal(t, int64(1), stored.BlockedCount)
	assert.Equal(t, int64(0), stored.FailedCount)
	report, err := stored.ParseDeliveryReport()
	require.NoError(t, err)
	assert.Equal(t, []int64{111}, report.Blocked)
	assert.Empty(t, report.Errors)
}

// TestBroadcast_StartedEventBackfilledOnResume покрывает краш между ClaimBroadcast
// и первой записью broadcast_started: claim прошёл в другом процессе (claimedNow
// == false), события ещё нет — resume-путь обязан дописать его, иначе кампания
// дойдёт до broadcast_completed со стартом в аудите-трейле пропущенным.
func TestBroadcast_StartedEventBackfilledOnResume(t *testing.T) {
	t.Parallel()

	mockBot := testutil.NewBotAPI()
	handler, db := newBroadcastSQLiteHandler(t, mockBot)

	state := broadcastRecipientsStateJSON(t, []database.BroadcastRecipient{
		{ID: 1, TelegramID: 710001, Status: database.BroadcastRecipientSent, UpdatedAt: time.Now().UTC()},
	})
	b := &database.Broadcast{
		Name: "crashed-early", MessageText: "text",
		Status: string(database.BroadcastStatusRunning), RecipientsState: state,
	}
	require.NoError(t, db.CreateBroadcast(context.Background(), b))

	require.NoError(t, handler.broadcastWorker.processCampaign(context.Background(), b))

	events, err := db.GetAuditLogByEntity(context.Background(), database.EntityBroadcast, fmt.Sprintf("%d", b.ID), 20)
	require.NoError(t, err)

	var started, completed int
	for i := range events {
		switch events[i].Event {
		case database.AuditEventBroadcastStarted:
			started++
		case database.AuditEventBroadcastCompleted:
			completed++
		}
	}
	assert.Equal(t, 1, started, "start event must be backfilled exactly once on resume")
	assert.Equal(t, 1, completed)
}

// TestBroadcast_StartedEventNotDuplicated: кампания, уже отработавшая один проход
// со стартом, на повторном resume не пишет повторное событие.
func TestBroadcast_StartedEventNotDuplicatedOnResume(t *testing.T) {
	t.Parallel()

	mockBot := testutil.NewBotAPI()
	handler, db := newBroadcastSQLiteHandler(t, mockBot)

	state := broadcastRecipientsStateJSON(t, []database.BroadcastRecipient{
		{ID: 1, TelegramID: 710001, Status: database.BroadcastRecipientSent, UpdatedAt: time.Now().UTC()},
	})
	b := &database.Broadcast{
		Name: "resumed-once", MessageText: "text",
		Status: string(database.BroadcastStatusRunning), RecipientsState: state,
	}
	require.NoError(t, db.CreateBroadcast(context.Background(), b))

	// Первый проход записывает broadcast_started.
	require.NoError(t, handler.broadcastWorker.processCampaign(context.Background(), b))

	// Второй проход по той же (уже завершённой) кампании не должен его дублировать.
	require.NoError(t, handler.broadcastWorker.processCampaign(context.Background(), b))

	events, err := db.GetAuditLogByEntity(context.Background(), database.EntityBroadcast, fmt.Sprintf("%d", b.ID), 20)
	require.NoError(t, err)

	var started int
	for i := range events {
		if events[i].Event == database.AuditEventBroadcastStarted {
			started++
		}
	}
	assert.Equal(t, 1, started, "resume must not duplicate the started event")
}

// TestBroadcast_RecipientSendPanicIsolated: паника в отправке одного получателя
// не срывает процесс кампании. Получатель фиксируется как failed с текстом
// паники, второй получатель той же партии доставляется; кампания остаётся
// resumable и переносится на следующий проход. Ловит восстановление в
// processRecipientSafely без test-only хуков.
func TestBroadcast_RecipientSendPanicIsolated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mockBot := testutil.NewBotAPI()
	handler, db := newBroadcastSQLiteHandler(t, mockBot)

	state := broadcastRecipientsStateJSON(t, []database.BroadcastRecipient{
		{ID: 1, TelegramID: 111, Status: database.BroadcastRecipientPending, UpdatedAt: time.Now().UTC()},
		{ID: 2, TelegramID: 222, Status: database.BroadcastRecipientPending, UpdatedAt: time.Now().UTC()},
	})
	b := &database.Broadcast{
		Name: "panic", MessageText: "text",
		Status: string(database.BroadcastStatusScheduled), RecipientsState: state,
	}
	require.NoError(t, db.CreateBroadcast(ctx, b))

	var panicMu sync.Mutex
	panicked := false
	mockBot.SendFunc = func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
		if mc, ok := c.(tgbotapi.MessageConfig); ok && mc.ChatID == 111 {
			panicMu.Lock()
			firstPanic := !panicked
			panicked = true
			panicMu.Unlock()
			if firstPanic {
				panic("telegram send exploded")
			}
		}
		return tgbotapi.Message{MessageID: 1}, nil
	}

	var processErr error
	require.NotPanics(t, func() {
		processErr = handler.broadcastWorker.processCampaign(ctx, b)
	})

	stored, err := db.GetBroadcast(ctx, b.ID)
	require.NoError(t, err)
	// Доменная семантика: ошибка партии (в т.ч. паника получателя) не финализирует
	// кампанию сразу — она остаётся запущенной и переносится в следующий проход
	// согласно scheduleRetry, а исход каждого получателя уже зафиксирован.
	assert.Equal(t, string(database.BroadcastStatusRunning), stored.Status)
	assert.Equal(t, 1, stored.RetryCount)
	require.NotNil(t, stored.RetryAt)

	var parsed struct {
		Recipients []database.BroadcastRecipient `json:"recipients"`
	}
	require.NoError(t, json.Unmarshal([]byte(stored.RecipientsState), &parsed))
	require.Len(t, parsed.Recipients, 2)

	var panickedRecipient, deliveredRecipient *database.BroadcastRecipient
	for i := range parsed.Recipients {
		switch parsed.Recipients[i].TelegramID {
		case 111:
			panickedRecipient = &parsed.Recipients[i]
		case 222:
			deliveredRecipient = &parsed.Recipients[i]
		}
	}
	require.NotNil(t, panickedRecipient)
	require.NotNil(t, deliveredRecipient)
	assert.Equal(t, database.BroadcastRecipientFailed, panickedRecipient.Status, "panicking recipient is marked failed, not sending")
	assert.Contains(t, panickedRecipient.LastError, "telegram send exploded")
	assert.Equal(t, database.BroadcastRecipientSent, deliveredRecipient.Status, "co-recipient from the same batch still delivers")

	total, sent, blocked, unreachable, failed, report, err := db.GetBroadcastRecipientsStats(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Equal(t, int64(1), sent)
	assert.Equal(t, int64(0), blocked)
	assert.Equal(t, int64(0), unreachable)
	assert.Equal(t, int64(1), failed)
	assert.Equal(t, []int64{222}, report.Delivered)
	assert.Empty(t, report.Blocked)
	assert.Empty(t, report.Unreachable)
	require.Len(t, report.Errors, 1)
	assert.Equal(t, int64(111), report.Errors[0].TelegramID)
	assert.Contains(t, report.Errors[0].Error, "telegram send exploded")
	assert.Empty(t, report.NotProcessed)

	require.Error(t, processErr)
	assert.Contains(t, processErr.Error(), "telegram send exploded")
}
