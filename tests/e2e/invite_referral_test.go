package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/web"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_ShareLink_CachesPendingInvite(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	_, err := env.db.GetOrCreateInvite(ctx, 111222, "sharecode123")
	require.NoError(t, err)

	resetBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_sharecode123", 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Invite message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "пригласили", "Should show invited message")
	assert.Contains(t, env.botAPI.LastSentText, "реферальное", "Should mention referral")
}

func setupInviteServer(t *testing.T, env *e2eTestEnv, inviteCode string, xuiSetup func(*testutil.XUIClient)) (*web.Server, *service.SubscriptionService) {
	t.Helper()

	env.cfg.TrialRateLimit = 100

	clients := e2eNodeClients(map[uint]interfaces.XUIClient{1: env.xui})
	if xuiSetup != nil {
		xuiSetup(env.xui)
	}

	nodes := []database.Node{{Name: "main", Host: "https://panel.example.com", APIToken: "test-api-token", InboundIDs: "[1]", IsActive: true, ID: 1}}
	subService := service.NewSubscriptionService(env.db, clients, nodes, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig.Username, subService, nil)

	return srv, subService
}

func TestE2E_InviteLink_Parameterized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		inviteCode   string
		referrerID   int64
		setupXUI     func(*testutil.XUIClient)
		createInvite bool
		wantStatus   int
		wantContains string
		check        func(*testing.T, *e2eTestEnv, *httptest.ResponseRecorder)
	}{
		{
			name:         "creates_trial",
			inviteCode:   "invite_test_abc",
			referrerID:   200001,
			setupXUI:     nil,
			createInvite: true,
			wantStatus:   http.StatusOK,
			wantContains: "trial_",
			check: func(t *testing.T, env *e2eTestEnv, rec *httptest.ResponseRecorder) {
				ctx := context.Background()
				trialPlan, err := env.db.GetPlanByName(ctx, database.TrialPlanName)
				require.NoError(t, err)
				allSubs, err := env.db.GetAllSubscriptions(ctx)
				require.NoError(t, err)

				trialCount := 0

				for _, sub := range allSubs {
					if sub.PlanID == trialPlan.ID {
						trialCount++
					}
				}

				assert.Equal(t, 1, trialCount, "Trial subscription should be created in DB")
				assert.True(t, env.xui.AddClientWithIDCalled, "XUI AddClientWithID should be called")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupE2EEnv(t)
			defer env.db.Close()

			ctx := context.Background()
			if tt.createInvite {
				_, err := env.db.GetOrCreateInvite(ctx, tt.referrerID, tt.inviteCode)
				require.NoError(t, err)
			}

			srv, _ := setupInviteServer(t, env, tt.inviteCode, tt.setupXUI)

			req := httptest.NewRequest("GET", "/i/"+tt.inviteCode, nil)
			req.Header.Set("X-Forwarded-For", "10.1.1.1")

			rec := httptest.NewRecorder()

			srv.HandleInvite(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.wantContains)

			if tt.check != nil {
				tt.check(t, env, rec)
			}
		})
	}
}

func TestE2E_InviteLink_RateLimitExceeded(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "invite_ratelimit"
	_, err := env.db.GetOrCreateInvite(ctx, 200004, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 1

	clients := e2eNodeClients(map[uint]interfaces.XUIClient{1: env.xui})
	nodes := []database.Node{{Name: "main", Host: "https://panel.example.com", APIToken: "test-api-token", InboundIDs: "[1]", IsActive: true, ID: 1}}
	subService := service.NewSubscriptionService(env.db, clients, nodes, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig.Username, subService, nil)

	req1 := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req1.Header.Set("X-Forwarded-For", "10.1.4.1")

	rec1 := httptest.NewRecorder()
	srv.HandleInvite(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code)

	req2 := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req2.Header.Set("X-Forwarded-For", "10.1.4.1")

	rec2 := httptest.NewRecorder()
	srv.HandleInvite(rec2, req2)

	assert.Equal(t, http.StatusTooManyRequests, rec2.Code)
	assert.Contains(t, rec2.Body.String(), "Слишком много запросов", "Should show rate limit error")
}

func TestE2E_InviteLink_FullFlow_BindTrial(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "invite_full_flow"
	_, err := env.db.GetOrCreateInvite(ctx, 200005, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	clients := e2eNodeClients(map[uint]interfaces.XUIClient{1: env.xui})
	nodes := []database.Node{{Name: "main", Host: "https://panel.example.com", APIToken: "test-api-token", InboundIDs: "[1]", IsActive: true, ID: 1}}
	subService := service.NewSubscriptionService(env.db, clients, nodes, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig.Username, subService, nil)

	req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req.Header.Set("X-Forwarded-For", "10.1.5.1")

	rec := httptest.NewRecorder()
	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	html := rec.Body.String()
	assert.Contains(t, html, "trial_", "Should contain trial link")

	subIDStart := strings.Index(html, "trial_")
	require.Greater(t, subIDStart, -1, "Should find trial_ in HTML")
	subIDEnd := strings.Index(html[subIDStart:], "\"")
	require.Greater(t, subIDEnd, -1, "Should find closing quote")
	trialSubID := html[subIDStart+6 : subIDStart+subIDEnd]

	resetBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Activation message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err, "Subscription should be bound to Telegram ID")
	assert.Equal(t, env.chatID, sub.TelegramID)
	assert.Equal(t, env.username, sub.Username)
	trialPlan, err := env.db.GetPlanByName(ctx, database.TrialPlanName)
	require.NoError(t, err)
	assert.False(t, sub.PlanID == trialPlan.ID, "Should no longer be marked as trial")
}

func TestE2E_FullCycle_InviteToQR(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "full_cycle_qr"
	_, err := env.db.GetOrCreateInvite(ctx, 300001, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	clients := e2eNodeClients(map[uint]interfaces.XUIClient{1: env.xui})
	nodes := []database.Node{{Name: "main", Host: "https://panel.example.com", APIToken: "test-api-token", InboundIDs: "[1]", IsActive: true, ID: 1}}
	subService := service.NewSubscriptionService(env.db, clients, nodes, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig.Username, subService, nil)

	req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req.Header.Set("X-Forwarded-For", "10.2.1.1")

	rec := httptest.NewRecorder()
	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "Invite page should load")

	html := rec.Body.String()
	assert.Contains(t, html, "trial_", "Should contain trial activation link")

	subIDStart := strings.Index(html, "trial_")
	require.Greater(t, subIDStart, -1)
	subIDEnd := strings.Index(html[subIDStart:], "\"")
	require.Greater(t, subIDEnd, -1)
	trialSubID := html[subIDStart+6 : subIDStart+subIDEnd]

	resetBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false
	env.xui.UpdateClientCalled = false

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Activation message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, env.chatID, sub.TelegramID)
	assert.Equal(t, env.username, sub.Username)
	trialPlan, err := env.db.GetPlanByName(ctx, database.TrialPlanName)
	require.NoError(t, err)
	assert.False(t, sub.PlanID == trialPlan.ID, "Should be converted from trial")
	assert.NotEmpty(t, sub.Username, "Username should be stored")

	resetBotAPI(env.botAPI)

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			Message: &tgbotapi.Message{
				Chat: &tgbotapi.Chat{ID: env.chatID},
				From: &tgbotapi.User{
					ID:       env.chatID,
					UserName: env.username,
				},
			},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "qr_code",
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "QR should be sent")
	assert.True(t, env.botAPI.RequestCalledSafe(), "QR photo should be uploaded")
}

func TestE2E_FullCycle_ShareToSubscription(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "share_to_sub"
	_, err := env.db.GetOrCreateInvite(ctx, 300002, inviteCode)
	require.NoError(t, err)

	resetBotAPI(env.botAPI)
	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_"+inviteCode, 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Should respond to share link")
	assert.Contains(t, env.botAPI.LastSentText, "пригласил", "Should mention invitation")

	resetBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false

	env.handler.HandleCallback(ctx, tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			Message: &tgbotapi.Message{
				Chat: &tgbotapi.Chat{ID: env.chatID},
				From: &tgbotapi.User{
					ID:       env.chatID,
					UserName: env.username,
				},
			},
			From: &tgbotapi.User{
				ID:       env.chatID,
				UserName: env.username,
			},
			Data: "create_subscription",
		},
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Subscription confirmation should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "подписк", "Should mention subscription")

	sub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, env.chatID, sub.TelegramID)
}

func TestE2E_FullCycle_MultipleUsersViaInvite(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "multi_user_invite"
	referrerID := int64(300003)
	_, err := env.db.GetOrCreateInvite(ctx, referrerID, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	clients := e2eNodeClients(map[uint]interfaces.XUIClient{1: env.xui})
	nodes := []database.Node{{Name: "main", Host: "https://panel.example.com", APIToken: "test-api-token", InboundIDs: "[1]", IsActive: true, ID: 1}}
	subService := service.NewSubscriptionService(env.db, clients, nodes, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig.Username, subService, nil)

	user1ChatID := int64(400001)
	user2ChatID := int64(400002)

	for _, chatID := range []int64{user1ChatID, user2ChatID} {
		req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.3.%d.1", chatID%256))

		rec := httptest.NewRecorder()
		srv.HandleInvite(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, "User %d should get trial page", chatID)

		html := rec.Body.String()
		subIDStart := strings.Index(html, "trial_")
		require.Greater(t, subIDStart, -1)
		subIDEnd := strings.Index(html[subIDStart:], "\"")
		require.Greater(t, subIDEnd, -1)
		trialSubID := html[subIDStart+6 : subIDStart+subIDEnd]

		resetBotAPI(env.botAPI)
		env.xui.AddClientWithIDCalled = false
		env.xui.UpdateClientCalled = false

		username := fmt.Sprintf("user_%d", chatID)
		env.handler.HandleStart(ctx, tgbotapi.Update{
			Message: newCommandMessage(chatID, chatID, username, "/start trial_"+trialSubID, 6),
		})

		assert.True(t, env.botAPI.SendCalledSafe(), "User %d should get activation message", chatID)

		sub, err := env.db.GetByTelegramID(ctx, chatID)
		require.NoError(t, err, "User %d should have subscription", chatID)
		assert.Equal(t, chatID, sub.TelegramID)

		trialPlan, err := env.db.GetPlanByName(ctx, database.TrialPlanName)
		require.NoError(t, err)
		assert.False(t, sub.PlanID == trialPlan.ID)
		assert.Equal(t, referrerID, *sub.ReferredBy, "User %d should have correct referrer", chatID)
	}
}

func TestE2E_FullCycle_ConcurrentInviteAccess(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "concurrent_invite"
	_, err := env.db.GetOrCreateInvite(ctx, 300006, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 1000

	clients := e2eNodeClients(map[uint]interfaces.XUIClient{1: env.xui})
	nodes := []database.Node{{Name: "main", Host: "https://panel.example.com", APIToken: "test-api-token", InboundIDs: "[1]", IsActive: true, ID: 1}}
	subService := service.NewSubscriptionService(env.db, clients, nodes, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig.Username, subService, nil)

	var wg sync.WaitGroup

	results := make(chan int, 10)

	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.5.%d.1", idx))

			rec := httptest.NewRecorder()
			srv.HandleInvite(rec, req)

			results <- rec.Code
		}(i)
	}

	wg.Wait()
	close(results)

	successCount := 0

	for code := range results {
		if code == http.StatusOK {
			successCount++
		}
	}

	assert.Equal(t, 10, successCount, "All concurrent requests should succeed")

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	trialPlan, err := env.db.GetPlanByName(ctx, database.TrialPlanName)
	require.NoError(t, err)

	trialCount := 0

	for _, sub := range allSubs {
		if sub.PlanID == trialPlan.ID && sub.TelegramID < 0 {
			trialCount++
		}
	}

	assert.GreaterOrEqual(t, trialCount, 10, "Should have at least 10 trial subscriptions")
}

func TestE2E_Referral_RefstatsShowsData(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()
	adminID := env.cfg.TelegramAdminID

	env.handler.IncrementReferralCount(int64(800001))
	env.handler.IncrementReferralCount(int64(800001))
	env.handler.IncrementReferralCount(int64(800002))

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat:     &tgbotapi.Chat{ID: adminID},
			From:     &tgbotapi.User{ID: adminID, UserName: "admin"},
			Text:     "/refstats",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 9}},
		},
	}
	env.handler.HandleRefstats(ctx, update)

	assert.True(t, env.botAPI.SendCalledSafe(), "Refstats should send message")
	assert.Contains(t, env.botAPI.LastSentText, "Статистика рефералов", "Should show referral stats")
	assert.Contains(t, env.botAPI.LastSentText, "3", "Should show total referrals")
}
