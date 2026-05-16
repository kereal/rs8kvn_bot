package e2e

import (
	"context"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/webhook"
	"rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_RealClient_FullSubscriptionLifecycle(t *testing.T) {
	env := setupRealXUIEnv(t, nil)
	defer env.Close()

	ctx := context.Background()

	result, err := env.subService.Create(ctx, 12345, "testuser")
	require.NoError(t, err, "Create subscription should succeed")
	require.NotNil(t, result)
	assert.NotEmpty(t, result.SubscriptionURL)

	sub, err := env.db.GetByTelegramID(ctx, 12345)
	require.NoError(t, err)
	assert.Equal(t, "testuser", sub.Username)
	assert.False(t, sub.IsTrial)

	_, traffic, err := env.subService.GetWithTraffic(ctx, 12345)
	require.NoError(t, err)
	assert.NotNil(t, traffic)

	err = env.subService.Delete(ctx, 12345)
	require.NoError(t, err)

	_, err = env.db.GetByTelegramID(ctx, 12345)
	assert.Error(t, err, "Subscription should be deleted")
}

func TestE2E_RealClient_DNSErrorFastFail(t *testing.T) {
	t.Skip("Skipping flaky test: DNS resolution timing varies by OS/network environment")

	if testing.Short() {
		t.Skip("Skipping in short mode")
	}

	cfg := &config.Config{
		TelegramAdminID:   123456,
		TrafficLimitGB:    100,
		XUIInboundID:      1,
		XUIHost:           "http://nonexistent.invalid.host:9999",
		XUISubPath:        "sub",
		SiteURL:           "https://example.com",
		TelegramBotToken:  "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
		XUIAPIToken:       "test-api-token",
	}

	xuiClient, err := xui.NewClient("http://nonexistent.invalid.host:9999", "test-api-token")
	require.NoError(t, err)
	defer xuiClient.Close()

	db := setupTestDB(t)
	defer db.Close()

	subService := service.NewSubscriptionService(db, xuiClient, cfg, &webhook.NoopSender{})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	_, err = subService.Create(ctx, 42345, "dns_fail_user")
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed.Seconds(), 25.0, "DNS error should fail fast, not retry (took %.1fs)", elapsed.Seconds())
}
