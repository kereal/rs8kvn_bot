

package e2e

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/kereal/rs8kvn_bot/internal/web"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_SubscriptionDelivery_AfterRenew_InvalidatesCacheAndServesUpdatedConfig(t *testing.T) {
	t.Parallel()

	env := setupE2EEnv(t)

	ctx := context.Background()
	freePlan, err := env.db.GetPlanByName(ctx, database.FreePlanName)
	require.NoError(t, err)

	premiumPlan := &database.Plan{
		Name:         "premium-e2e-delivery",
		IsActive:     true,
		DevicesLimit: 3,
		TrafficLimit: 2 << 30,
	}
	require.NoError(t, env.db.GetDB().WithContext(ctx).Create(premiumPlan).Error)

	backendState := struct {
		sync.Mutex

		body       string
		userInfo   string
		requestCnt int
	}{
		body:     "vless://free-user@free.example.com:443?encryption=none#free",
		userInfo: "upload=100; download=200; total=1073741824; expire=111",
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendState.Lock()
		defer backendState.Unlock()
		backendState.requestCnt++
		w.Header().Set("Subscription-UserInfo", backendState.userInfo)
		_, _ = w.Write([]byte(backendState.body))
	}))
	defer backend.Close()

	node := &database.Node{
		Name:            "premium-e2e-node",
		IsActive:        true,
		Host:            backend.URL,
		APIToken:        "premium-token",
		InboundIDs:      `[1]`,
		SubscriptionURL: backend.URL + "/sub/",
	}
	require.NoError(t, env.db.GetDB().WithContext(ctx).Create(node).Error)
	require.NoError(t, env.db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: freePlan.ID, NodeID: node.ID}).Error)
	require.NoError(t, env.db.GetDB().WithContext(ctx).Create(&database.PlanNode{PlanID: premiumPlan.ID, NodeID: node.ID}).Error)

	sub, err := env.subService.Create(ctx, env.chatID, env.username, "")
	require.NoError(t, err)

	xuiClients := map[uint]interfaces.XUIClient{1: env.xui, node.ID: env.xui}
	nodes := []database.Node{
		{ID: 1, Name: "main", IsActive: true, Host: "https://panel.example.com", APIToken: "test-api-token", InboundIDs: "[1]"},
		*node,
	}
	subService := service.NewSubscriptionService(env.db, xuiClients, e2eVPNClients(xuiClients), nodes, env.cfg)
	subSrv := subserver.NewService(config.SubServerCacheTTL)
	defer subSrv.Stop()
	subService.SetInvalidateBySubIDFunc(subSrv.InvalidateCache)
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig.Username, subService, subSrv)

	ctxSrv, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, srv.Start(ctxSrv))
	defer srv.Stop(context.Background())
	waitForServerReady(t, srv.Addr(), time.Second)

	getDecodedBody := func() (string, http.Header) {
		resp, err := http.Get(fmt.Sprintf("http://%s/sub/%s", srv.Addr(), sub.Subscription.SubscriptionID))
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
		require.NoError(t, err)
		return string(decoded), resp.Header.Clone()
	}

	bodyBefore, headersBefore := getDecodedBody()
	assert.Contains(t, bodyBefore, "free.example.com", "subscription should be downloadable before renew")
	assert.Contains(t, headersBefore.Get("Subscription-Userinfo"), "total=53687091200", "free plan traffic should be exposed before renew")

	backendState.Lock()
	requestsAfterFirstFetch := backendState.requestCnt
	backendState.body = "trojan://premium-pass@premium.example.com:443?security=tls#premium"
	backendState.userInfo = "upload=300; download=400; total=2147483648; expire=222"
	backendState.Unlock()

	product := &database.Product{
		PlanID:       premiumPlan.ID,
		Name:         "Premium 30d E2E",
		DurationDays: 30,
		PriceCents:   19900,
		Currency:     "RUB",
		IsActive:     true,
	}
	require.NoError(t, env.db.GetDB().WithContext(ctx).Create(product).Error)

	_, err = subService.RenewSubscription(ctx, env.chatID, product)
	require.NoError(t, err)

	bodyAfter, headersAfter := getDecodedBody()
	assert.Contains(t, bodyAfter, "premium.example.com", "renewed subscription should serve updated backend config")
	assert.NotContains(t, bodyAfter, "free.example.com", "old cached config should be invalidated after renew")
	assert.Contains(t, headersAfter.Get("Subscription-Userinfo"), "total=2147483648", "renew should expose updated plan traffic in response headers")

	updatedSub, err := env.db.GetByTelegramID(ctx, env.chatID)
	require.NoError(t, err)
	assert.Equal(t, premiumPlan.ID, updatedSub.PlanID)
	require.NotNil(t, updatedSub.ExpiresAt)
	assert.True(t, updatedSub.ExpiresAt.After(time.Now()), "renew should extend subscription expiry")

	rows := []database.SubscriptionNode{}
	require.NoError(t, env.db.GetDB().WithContext(ctx).Where("subscription_id = ? AND node_id = ?", updatedSub.ID, node.ID).Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Contains(t, []database.SyncStatus{database.SyncStatusPendingAdd, database.SyncStatusPendingUpdate, database.SyncStatusActive}, rows[0].Status)

	backendState.Lock()
	assert.Greater(t, backendState.requestCnt, requestsAfterFirstFetch, "second download should hit backend again after cache invalidation")
	backendState.Unlock()

	var order database.Order
	require.NoError(t, env.db.GetDB().WithContext(ctx).Where("subscription_id = ? AND product_id = ?", updatedSub.ID, product.ID).First(&order).Error)
	assert.Equal(t, database.OrderStatusPaid, order.Status)
	assert.Equal(t, product.PriceCents, order.AmountCents)
}
