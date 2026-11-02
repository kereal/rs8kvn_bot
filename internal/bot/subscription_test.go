package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"

	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShowLoadingMessage_WithMessageID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 100

	resultID := handler.showLoadingMessage(chatID, messageID)

	assert.Equal(t, messageID, resultID, "Should return same messageID when editing")
	assert.True(t, mockBot.SendCalledSafe(), "Send should be called")
}

func TestShowLoadingMessage_WithoutMessageID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 0

	resultID := handler.showLoadingMessage(chatID, messageID)

	assert.NotEqual(t, 0, resultID, "Should return new messageID when sending new message")
	assert.True(t, mockBot.SendCalledSafe(), "Send should be called")
}

func TestShowLoadingMessage_SendFails(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	mockBot.SendError = errors.New("send error")
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	messageID := 0

	resultID := handler.showLoadingMessage(chatID, messageID)

	assert.Equal(t, 0, resultID, "Should return 0 when send fails")
}

func TestShowLoadingMessage_EditFails(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	// Fail-once fake: the first call (edit of the existing message) fails,
	// the second (if any) would succeed.
	sendCalls := 0
	mockBot := &testutil.BotAPI{
		SendFunc: func(c tgbotapi.Chattable) (tgbotapi.Message, error) {
			sendCalls++
			if sendCalls == 1 {
				return tgbotapi.Message{}, errors.New("edit error")
			}

			return tgbotapi.Message{MessageID: 555}, nil
		},
	}
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)

	resultID := handler.showLoadingMessage(chatID, 100)

	// Edit failure has no fallback to a fresh send: the caller gets 0 and
	// createSubscription aborts with "failed to show loading message" instead
	// of editing a message that does not exist.
	assert.Equal(t, 1, sendCalls, "edit failure must not trigger a second send")
	assert.Equal(t, 0, resultID, "edit failure must report 0 so the caller aborts")
}

func TestCreateSubscription_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	// DB-first: mock DB methods for Create() + ensureSubscriptionNodes()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
	}
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		assert.Equal(t, int64(123456), sub.TelegramID)
		assert.Equal(t, "testuser", sub.Username)
		sub.ID = 1

		return nil
	}
	mockDB.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		return nil, nil
	}
	mockDB.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return nil, nil
	}

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "подписк", "successful create must land on the subscription screen")
}

func TestCreateSubscription_DbFirst_NoNodes_ReturnsSuccess(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	// DB-first: mock DB methods for Create() + ensureSubscriptionNodes().
	// No nodes configured → createSubscription never reaches XUI; the
	// subscription is persisted in DB and the user gets a success message.
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
	}
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		sub.ID = 1
		return nil
	}
	mockDB.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		return nil, nil
	}
	mockDB.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return nil, nil
	}

	ctx := context.Background()
	err := handler.createSubscription(ctx, 123456, "testuser", 1)

	require.NoError(t, err, "DB-first create must succeed even with no nodes provisioned")
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "подписк")
}

func TestCreateSubscription_DatabaseFailure_RollbackSuccess(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	// DB-first: mock DB methods - CreateSubscription fails
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
	}
	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		return errors.New("database error")
	}

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	// DB-first: no XUI rollback needed, error is shown to user
	assert.True(t, mockBot.SendCalledSafe())
}

func TestCreateSubscription_CacheUpdate(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	// DB-first: mock DB methods for Create() + ensureSubscriptionNodes()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
	}

	var savedSub *database.Subscription

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		savedSub = sub
		sub.ID = 1

		return nil
	}
	mockDB.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		return nil, nil
	}
	mockDB.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return nil, nil
	}

	ctx := context.Background()
	chatID := int64(123456)

	// Verify cache is empty initially
	cached := handler.cache.Get(chatID)
	assert.Nil(t, cached)

	handler.createSubscription(ctx, chatID, "testuser", 1)

	// Verify cache was updated
	cached = handler.cache.Get(chatID)
	require.NotNil(t, cached)
	assert.Equal(t, savedSub.TelegramID, cached.TelegramID)
}

func TestHandleCreateSubscription_AlreadyInProgress(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)

	// Simulate subscription already in progress (set by another goroutine)
	handler.inProgressSyncMap.Store(chatID, true)

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, chatID, "testuser", 1)

	// inProgress entry should NOT be cleaned up — it belongs to the goroutine that set it.
	// The early-return path doesn't register the defer cleanup.
	_, stillInProgress := handler.inProgressSyncMap.Load(chatID)
	assert.True(t, stillInProgress, "inProgress entry should remain (belongs to other goroutine)")

	// No bot interaction should have occurred (early return)
	assert.False(t, mockBot.SendCalledSafe(), "Bot should not be called when already in progress")
}

func TestHandleCreateSubscription_ExistingActiveSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",

		ExpiresAt: testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour)), // Not expired
		Status:    "active",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ваша подписка")
	// Verify no new client was created on XUI (user already has active sub)
	assert.False(t, mockXUI.AddClientWithIDCalled, "Should not create new XUI client for existing active subscription")
}
func TestHandleCreateSubscription_ExpiredSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",

		ExpiresAt: testutil.PtrTime(time.Now().Add(-24 * time.Hour)), // Expired
		Status:    "expired",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	clientConfig := &xui.ClientConfig{
		ID:    "new-client-uuid",
		SubID: "new-sub-id",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
		return clientConfig, nil
	}

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		return nil
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	// An expired subscription is still a row: the handler shows the existing
	// subscription screen and must NOT provision a new XUI client.
	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ваша подписка")
	assert.False(t, mockXUI.AddClientWithIDCalled, "expired sub must not trigger a new XUI client")
}

func TestHandleCreateSubscription_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Временная ошибка")
}

func TestHandleCreateSubscription_NoSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	clientConfig := &xui.ClientConfig{
		ID:    "new-client-uuid",
		SubID: "new-sub-id",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
		return clientConfig, nil
	}

	// DB-first plan/nodes wiring: no nodes configured → the create flow
	// persists the row and answers the user without touching XUI.
	mockDB.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		return nil, nil
	}
	mockDB.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return nil, nil
	}

	ctx := context.Background()
	handler.handleCreateSubscription(ctx, 123456, "testuser", 1)

	// No prior row → the create flow must run: the user ends up on the
	// subscription screen (loading + final message), and the sub is persisted.
	assert.True(t, mockBot.SendCalledSafe())
	assert.GreaterOrEqual(t, mockBot.SendCountSafe(), 2, "create flow sends loading + final message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "подписк")
	assert.False(t, mockXUI.AddClientWithIDCalled, "no nodes configured → no XUI client creation")
}

func TestHandleMySubscription_NotFound(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "нет активной подписки")
}

func TestHandleMySubscription_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Временная ошибка")
}

func TestHandleMySubscription_ActiveSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",
		PlanID:     1,

		ExpiresAt: testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour)),
		Status:    "active",
		CreatedAt: time.Now().Add(-7 * 24 * time.Hour), // Created 7 days ago
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}
	mockDB.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return []database.SubscriptionNode{{SubscriptionID: subscriptionID, NodeID: 1, Status: database.SyncStatusActive}}, nil
	}
	mockDB.GetPlanByIDFunc = func(ctx context.Context, planID uint) (*database.Plan, error) {
		return &database.Plan{
			ID:           planID,
			Name:         "test",
			TrafficLimit: 100 * 1024 * 1024 * 1024,
		}, nil
	}

	traffic := &xui.ClientTraffic{
		Up:        1024 * 1024 * 1024,                      // 1 GB
		Down:      2048 * 1024 * 1024,                      // 2 GB
		ExpiresAt: time.Now().AddDate(0, 1, 0).UnixMilli(), // +1 month
		Reset:     30,
	}

	mockXUI.GetClientTrafficFunc = func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
		return traffic, nil
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ваша подписка")
	assert.Contains(t, mockBot.LastSentTextSafe(), "3.00 из 100 Гб (3%)") // 1 + 2 = 3 GB
	assert.Contains(t, mockBot.LastSentTextSafe(), "⬜⬜⬜⬜⬜⬜⬜⬜⬜⬜")          // Empty progress bar (3%)
	assert.Contains(t, mockBot.LastSentTextSafe(), "📅 Создана:")
	assert.Contains(t, mockBot.LastSentTextSafe(), "🔄 Сброс:")
}

func TestHandleMySubscription_TrafficFetchError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",

		ExpiresAt: testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour)),
		Status:    "active",
		CreatedAt: time.Now().Add(-7 * 24 * time.Hour), // Created 7 days ago
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	mockDB.GetPlanByIDFunc = func(ctx context.Context, planID uint) (*database.Plan, error) {
		return &database.Plan{TrafficLimit: 107374182400}, nil // 100GB in bytes
	}

	mockXUI.GetClientTrafficFunc = func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
		return nil, errors.New("traffic error")
	}

	mockDB.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		// Одна активная привязка на узел 1: читатель опрашивается, но отвечает
		// ошибкой — трафик не измерен, поэтому карточка честно сообщает
		// «измерение недоступно», а не показывает нулевой расход.
		return []database.SubscriptionNode{{SubscriptionID: subscriptionID, NodeID: 1, Status: database.SyncStatusActive}}, nil
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ваша подписка")
	// Should still show subscription even if traffic fetch fails.
	assert.Contains(t, mockBot.LastSentTextSafe(), "измерение недоступно")
}

// TestHandleMySubscription_FetchesSubscriptionViaService documents the data
// path: handleMySubscription reads the subscription through the service
// (GetWithTraffic → DB), NOT through the handler-level subscription cache —
// the pre-warmed cache entry is ignored on this screen.
func TestHandleMySubscription_FetchesSubscriptionViaService(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	sub := &database.Subscription{
		TelegramID: 123456,
		Username:   "testuser",

		ExpiresAt: testutil.PtrTime(time.Now().Add(30 * 24 * time.Hour)),
		Status:    "active",
	}

	// Pre-warm the handler cache — it must be irrelevant for this path.
	handler.cache.Set(123456, sub)

	traffic := &xui.ClientTraffic{
		Up:   1024 * 1024 * 1024,
		Down: 1024 * 1024 * 1024,
	}

	mockXUI.GetClientTrafficFunc = func(ctx context.Context, email string) (*xui.ClientTraffic, error) {
		return traffic, nil
	}

	dbCalled := false
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		dbCalled = true

		return sub, nil
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.SendCalledSafe())
	assert.True(t, dbCalled, "subscription screen must go through the service (DB), not the handler cache")
}

func TestHandleQRCode_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	err := handler.handleQRCode(ctx, 123456, "testuser", 1)

	// Real DB errors should be returned, not masked with a user message.
	require.Error(t, err, "handleQRCode should return an error for DB failure")
	require.Contains(t, err.Error(), "database error", "error should wrap DB error")
	// No user-facing message should be sent for internal errors.
	assert.False(t, mockBot.SendCalledSafe(), "should not send user message on DB error")
}

func TestGetSubscriptionWithCache_CacheHit(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	sub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
		Status:     "active",
	}

	// Set cache
	handler.cache.Set(chatID, sub)

	// Database should not be called
	dbCalled := false
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		dbCalled = true
		return nil, nil
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.NoError(t, err)
	assert.Equal(t, sub, result)
	assert.False(t, dbCalled, "Database should not be called on cache hit")
}

func TestGetSubscriptionWithCache_CacheMiss(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	sub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
		Status:     "active",
	}

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return sub, nil
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.NoError(t, err)
	assert.Equal(t, sub, result)

	// Verify cache was updated
	cached := handler.cache.Get(chatID)
	assert.Equal(t, sub, cached)
}

func TestGetSubscriptionWithCache_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetSubscriptionWithCache_StaleCacheNonActive(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	// Put a non-active subscription in cache
	staleSub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
		Status:     "revoked",
	}
	handler.cache.Set(chatID, staleSub)

	// DB should return nothing
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	result, err := handler.getSubscriptionWithCache(ctx, chatID)

	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
	assert.Nil(t, result)
	// Verify stale cache was invalidated
	assert.Nil(t, handler.cache.Get(chatID))
}

func TestInvalidateCache(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	sub := &database.Subscription{
		TelegramID: chatID,
		Username:   "testuser",
	}

	// Set cache
	handler.cache.Set(chatID, sub)
	require.NotNil(t, handler.cache.Get(chatID))

	// Invalidate
	handler.invalidateCache(context.Background(), chatID)

	// Verify cache was cleared
	assert.Nil(t, handler.cache.Get(chatID))
}

func TestHandleQRCode_WithSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID: telegramID,
			Username:   "testuser",
			Status:     "active",
		}, nil
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called for QR photo")
}

func TestHandleQRCode_NoSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called with error message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "нет активной подписки", "message should mention no subscription")
}

func TestHandleBackToSubscription(t *testing.T) {
	t.Parallel()

	// Back must only delete the QR photo; the subscription card stays open
	// underneath, so no re-show (Bot.Send) is expected — even when the
	// delete request itself fails.
	tests := []struct {
		name         string
		requestError error
	}{
		{name: "delete succeeds"},
		{name: "delete fails", requestError: errors.New("delete failed")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler, mockBot := newQRTestHandler(t)
			if tt.requestError != nil {
				mockBot.RequestError = tt.requestError
			}

			ctx := context.Background()
			handler.handleBackToSubscription(ctx, 42, "u", 789)

			assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called to delete QR message")
			assert.False(t, mockBot.SendCalledSafe(), "Bot.Send must NOT re-show the card on Back")
		})
	}
}

func TestNotifyAdmin_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	err := handler.notifyAdmin(ctx, "testuser", 789012, "https://sub.url")

	assert.NoError(t, err)
	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called to notify admin")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Новая подписка", "message should mention new subscription")
}

func TestNotifyAdmin_ZeroAdminID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 0,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	err := handler.notifyAdmin(ctx, "testuser", 789012, "https://sub.url")

	assert.NoError(t, err)
	assert.False(t, mockBot.SendCalledSafe(), "Bot.Send should not be called when admin ID is zero")
}

func TestNotifyAdminError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	handler.notifyAdminError(ctx, "⚠️ Test error message")

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called for error notification")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Test error message", "message should contain error text")
}

func TestNotifyAdminError_ZeroAdminID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 0,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	handler.notifyAdminError(ctx, "⚠️ Test error")

	assert.False(t, mockBot.SendCalledSafe(), "Bot.Send should not be called when admin ID is zero")
}

func TestClearAdminSendRateLimit(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)
	handler.ClearAdminSendRateLimit(chatID)

	// Should not panic and rate limit should be cleared
	assert.True(t, handler.checkAdminSendRateLimit(chatID), "Rate limit should allow send after clear")
}

func TestHandleCreateError_RollbackFailed(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
		SiteURL:         "https://vpn.site",
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	rollbackErr := errors.New("create subscription: database error (rollback failed: rollback failed)")
	handler.handleCreateError(ctx, 123456, 100, "testuser", rollbackErr)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called with error message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Обратитесь к администратору", "message should mention contacting admin")
	assert.True(t, mockBot.SendCountSafe() >= 2, "Should send error message and admin notification")
}

func TestNotifyAdmin_SendError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	mockBot.SendError = errors.New("send failed")
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	err := handler.notifyAdmin(ctx, "testuser", 789012, "https://sub.url")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "notify admin")
}

func TestHandleQRCode_NilSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, nil
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "нет активной подписки")
}

func TestCreateSubscription_WithPendingInvite(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	// DB-first: mock DB methods for Create() + ensureSubscriptionNodes()
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, database.ErrSubscriptionNotFound
	}
	mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
		return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
	}

	// Simulate production DB behavior: resolve invite → set InviteCode + ReferredBy.
	// This is what real db.Service.CreateSubscription does inside the tx.
	var (
		savedSub      *database.Subscription
		gotInviteCode string
	)

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		gotInviteCode = inviteCode
		if inviteCode == "ABC123" {
			inviteVal := inviteCode
			sub.InviteCode = &inviteVal
			referredBy := int64(999999)
			sub.ReferredBy = &referredBy
		}

		sub.ID = 1
		// Store a value copy so assertions see a stable snapshot.
		stored := *sub
		savedSub = &stored

		return nil
	}
	mockDB.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
		return nil, nil
	}
	mockDB.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
		return nil, nil
	}

	inviteCode := "ABC123"

	handler.pendingMu.Lock()
	handler.pendingInvites[123456] = pendingInvite{
		code:      inviteCode,
		expiresAt: time.Now().Add(60 * time.Minute),
	}
	handler.pendingMu.Unlock()

	ctx := context.Background()
	result := handler.createSubscription(ctx, 123456, "testuser", 1)

	require.NoError(t, result)
	assert.Equal(t, "ABC123", gotInviteCode, "handler must forward inviteCode to db.CreateSubscription")
	require.NotNil(t, savedSub)
	assert.Equal(t, "ABC123", *savedSub.InviteCode, "InviteCode must be persisted on the sub")
	assert.Equal(t, int64(999999), *savedSub.ReferredBy, "ReferredBy must be persisted on the sub")

	// handler must consume the pending invite and bump the referral cache.
	handler.pendingMu.Lock()
	_, exists := handler.pendingInvites[123456]
	handler.pendingMu.Unlock()
	assert.False(t, exists, "Pending invite should be removed after use")
	assert.Equal(t, int64(1), handler.GetReferralCount(999999), "IncrementReferralCount should fire when invite resolved")
}

func TestCreateSubscription_WithExpiredPendingInvite(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	clientConfig := &xui.ClientConfig{
		ID:    "client-uuid-123",
		SubID: "sub-id-456",
	}

	mockXUI.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
		return clientConfig, nil
	}

	var gotInviteCode string = "NON_EMPTY_DEFAULT"

	mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
		gotInviteCode = inviteCode
		return nil
	}

	handler.pendingMu.Lock()
	handler.pendingInvites[123456] = pendingInvite{
		code:      "EXPIRED",
		expiresAt: time.Now().Add(-60 * time.Minute),
	}
	handler.pendingMu.Unlock()

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.Equal(t, "", gotInviteCode, "expired pending invite must not be forwarded to db")
}

func TestHandleBackToInvite_RequestError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockBot.RequestError = errors.New("request failed")

	ctx := context.Background()
	handler.handleBackToInvite(ctx, 123456, "testuser", 1)

	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called")
}

func TestCreateSubscription_ShowLoadingMessageFails(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()
	mockXUI := testutil.NewXUIClient()
	mockBot := testutil.NewBotAPI()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

	mockBot.SendError = errors.New("send failed")

	xuiCalled := false
	mockXUI.AddClientWithIDFunc = func(ctx context.Context, req xui.ClientRequest) (*xui.ClientConfig, error) {
		xuiCalled = true
		return nil, nil
	}

	ctx := context.Background()
	handler.createSubscription(ctx, 123456, "testuser", 1)

	assert.False(t, xuiCalled, "XUI should not be called when loading message fails")
}
