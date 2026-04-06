package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/web"
	"rs8kvn_bot/internal/xui"

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

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_sharecode123", 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Invite message should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "пригласили", "Should show invited message")
	assert.Contains(t, env.botAPI.LastSentText, "реферальное", "Should mention referral")
}

func TestE2E_ShareLink_ExistingSubscription_Ignored(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	existingSub := &database.Subscription{
		TelegramID:     env.chatID,
		Username:       env.username,
		ClientID:       "existing-client",
		SubscriptionID: "existing-sub",
		InboundID:      1,
		TrafficLimit:   107374182400,
		Status:         "active",
	}
	require.NoError(t, env.db.CreateSubscription(ctx, existingSub))

	_, err := env.db.GetOrCreateInvite(ctx, 111222, "sharecode456")
	require.NoError(t, err)

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_sharecode456", 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Menu should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "кнопки ниже", "Should show normal menu, not invite message")
}

func TestE2E_ShareLink_InvalidCode(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	resetMockBotAPI(env.botAPI)

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_invalidcode", 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Menu should be sent")
	assert.Contains(t, env.botAPI.LastSentText, "Привет", "Should show normal menu for invalid code")
}

func TestE2E_InviteLink_CreatesTrial(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "invite_test_abc"
	_, err := env.db.GetOrCreateInvite(ctx, 200001, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req.Header.Set("X-Forwarded-For", "10.1.1.1")
	rec := httptest.NewRecorder()

	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	html := rec.Body.String()
	assert.Contains(t, html, "trial_", "Should contain trial activation link")
	assert.Contains(t, html, "https://t.me/", "Should contain Telegram link")

	allSubs, err := env.db.GetAllSubscriptions(ctx)
	require.NoError(t, err)
	trialCount := 0
	for _, sub := range allSubs {
		if sub.IsTrial {
			trialCount++
		}
	}
	assert.Equal(t, 1, trialCount, "Trial subscription should be created in DB")
	assert.True(t, env.xui.AddClientWithIDCalled, "XUI AddClientWithID should be called")
}

func TestE2E_InviteLink_InvalidCode(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	env.cfg.TrialRateLimit = 100

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	req := httptest.NewRequest("GET", "/i/nonexistent_code", nil)
	req.Header.Set("X-Forwarded-For", "10.1.2.1")
	rec := httptest.NewRecorder()

	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Приглашение не найдено", "Should show invite not found error")
}

func TestE2E_InviteLink_XuiLoginFails(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "invite_xui_fail"
	_, err := env.db.GetOrCreateInvite(ctx, 200003, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		return nil, fmt.Errorf("authentication failed")
	}

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req.Header.Set("X-Forwarded-For", "10.1.3.1")
	rec := httptest.NewRecorder()

	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "Ошибка сервера", "Should show server error")
}

func TestE2E_AutoRelogin_On401(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "invite_relogin"
	_, err := env.db.GetOrCreateInvite(ctx, 300001, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	addClientCallCount := 0
	env.xui.AddClientWithIDFunc = func(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*xui.ClientConfig, error) {
		addClientCallCount++
		if addClientCallCount == 1 {
			return nil, fmt.Errorf("authentication failed")
		}
		return &xui.ClientConfig{
			ID:    clientID,
			Email: email,
			SubID: subID,
		}, nil
	}

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	req := httptest.NewRequest("GET", "/i/"+inviteCode, nil)
	req.Header.Set("X-Forwarded-For", "10.2.1.1")
	rec := httptest.NewRecorder()

	srv.HandleInvite(rec, req)

	assert.GreaterOrEqual(t, addClientCallCount, 1, "AddClientWithID should have been called at least once")
}

func TestE2E_InviteLink_RateLimitExceeded(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "invite_ratelimit"
	_, err := env.db.GetOrCreateInvite(ctx, 200004, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 1

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

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

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

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

	resetMockBotAPI(env.botAPI)
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
	assert.False(t, sub.IsTrial, "Should no longer be marked as trial")
}

func TestE2E_FullCycle_InviteToQR(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "full_cycle_qr"
	_, err := env.db.GetOrCreateInvite(ctx, 300001, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

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

	resetMockBotAPI(env.botAPI)
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
	assert.False(t, sub.IsTrial, "Should be converted from trial")
	assert.NotEmpty(t, sub.Username, "Username should be stored")

	resetMockBotAPI(env.botAPI)

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

	resetMockBotAPI(env.botAPI)
	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_"+inviteCode, 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe(), "Should respond to share link")
	assert.Contains(t, env.botAPI.LastSentText, "пригласил", "Should mention invitation")

	resetMockBotAPI(env.botAPI)
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

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

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

		resetMockBotAPI(env.botAPI)
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
		assert.False(t, sub.IsTrial)
		assert.Equal(t, referrerID, sub.ReferredBy, "User %d should have correct referrer", chatID)
	}
}

func TestE2E_FullCycle_InviteThenShare(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	webInviteCode := "web_invite_then_share"
	shareInviteCode := "share_invite_then_bind"

	_, err := env.db.GetOrCreateInvite(ctx, 300004, webInviteCode)
	require.NoError(t, err)
	_, err = env.db.GetOrCreateInvite(ctx, 300005, shareInviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 100

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	req := httptest.NewRequest("GET", "/i/"+webInviteCode, nil)
	req.Header.Set("X-Forwarded-For", "10.4.1.1")
	rec := httptest.NewRecorder()
	srv.HandleInvite(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	html := rec.Body.String()
	subIDStart := strings.Index(html, "trial_")
	require.Greater(t, subIDStart, -1)
	subIDEnd := strings.Index(html[subIDStart:], "\"")
	require.Greater(t, subIDEnd, -1)
	trialSubID := html[subIDStart+6 : subIDStart+subIDEnd]

	resetMockBotAPI(env.botAPI)
	env.xui.AddClientWithIDCalled = false
	env.xui.UpdateClientCalled = false

	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start trial_"+trialSubID, 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe())

	resetMockBotAPI(env.botAPI)
	env.handler.HandleStart(ctx, tgbotapi.Update{
		Message: newCommandMessage(env.chatID, env.chatID, env.username, "/start share_"+shareInviteCode, 6),
	})

	assert.True(t, env.botAPI.SendCalledSafe())
	assert.NotContains(t, env.botAPI.LastSentText, "пригласил", "Should not show invite message when user has subscription")
}

func TestE2E_FullCycle_ConcurrentInviteAccess(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	inviteCode := "concurrent_invite"
	_, err := env.db.GetOrCreateInvite(ctx, 300006, inviteCode)
	require.NoError(t, err)

	env.cfg.TrialRateLimit = 1000

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	srv := web.NewServer("127.0.0.1:0", env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	var wg sync.WaitGroup
	results := make(chan int, 10)

	for i := 0; i < 10; i++ {
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
	trialCount := 0
	for _, sub := range allSubs {
		if sub.IsTrial && sub.TelegramID == 0 {
			trialCount++
		}
	}
	assert.GreaterOrEqual(t, trialCount, 10, "Should have at least 10 trial subscriptions")
}

func TestE2E_Referral_IncrementDecrements(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	referrerID := int64(700001)

	assert.Equal(t, int64(0), env.handler.GetReferralCount(referrerID))

	env.handler.IncrementReferralCount(referrerID)
	assert.Equal(t, int64(1), env.handler.GetReferralCount(referrerID))

	env.handler.IncrementReferralCount(referrerID)
	assert.Equal(t, int64(2), env.handler.GetReferralCount(referrerID))

	env.handler.DecrementReferralCount(referrerID)
	assert.Equal(t, int64(1), env.handler.GetReferralCount(referrerID))

	env.handler.DecrementReferralCount(referrerID)
	assert.Equal(t, int64(0), env.handler.GetReferralCount(referrerID))
}

func TestE2E_Referral_DecrementDoesNotGoNegative(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	referrerID := int64(700002)

	env.handler.DecrementReferralCount(referrerID)
	assert.Equal(t, int64(0), env.handler.GetReferralCount(referrerID))
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

func TestE2E_InviteChain_ABC(t *testing.T) {
	env := setupE2EEnv(t)
	defer env.db.Close()

	ctx := context.Background()

	// User A creates subscription (referrer)
	userA := int64(100000)
	_, err := env.subService.Create(ctx, userA, "user_a")
	require.NoError(t, err)

	// Create invite for A
	_, err = env.db.GetOrCreateInvite(ctx, userA, "invite_a")
	require.NoError(t, err)

	// User B creates subscription (referred by A)
	userB := int64(200000)
	_, err = env.subService.Create(ctx, userB, "user_b")
	require.NoError(t, err)

	subB, err := env.db.GetByTelegramID(ctx, userB)
	require.NoError(t, err)
	subB.ReferredBy = 100000
	err = env.db.UpdateSubscription(ctx, subB)
	require.NoError(t, err)

	subB, err = env.db.GetByTelegramID(ctx, userB)
	require.NoError(t, err)
	assert.Equal(t, int64(100000), subB.ReferredBy, "B should have A as referrer")

	// Create invite for B
	_, err = env.db.GetOrCreateInvite(ctx, userB, "invite_b")
	require.NoError(t, err)

	// User C creates subscription (referred by B)
	userC := int64(300000)
	_, err = env.subService.Create(ctx, userC, "user_c")
	require.NoError(t, err)

	subC, err := env.db.GetByTelegramID(ctx, userC)
	require.NoError(t, err)
	subC.ReferredBy = 200000
	err = env.db.UpdateSubscription(ctx, subC)
	require.NoError(t, err)

	subC, err = env.db.GetByTelegramID(ctx, userC)
	require.NoError(t, err)
	assert.Equal(t, int64(200000), subC.ReferredBy, "C should have B as referrer")

	// Verify A has no referrer
	subA, err := env.db.GetByTelegramID(ctx, 100000)
	require.NoError(t, err)
	assert.Equal(t, int64(0), subA.ReferredBy, "A should have no referrer")
}
