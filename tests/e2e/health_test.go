package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/interfaces"
	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/web"
	"rs8kvn_bot/internal/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_HealthEndpoint(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	xuiClients := map[uint]interfaces.XUIClient{1: env.xui}
	sources := []database.Source{{Name: "main", XUIHost: "https://panel.example.com", XUIAPIToken: "test-api-token", XUIInboundID: 1, Active: true}}
	subService := service.NewSubscriptionService(env.db, xuiClients, sources, env.cfg, env.cfg.GlobalSubURL, &webhook.NoopSender{})
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig, subService, nil)

	srv.RegisterChecker("database", func(ctx context.Context) web.ComponentHealth {
		if err := env.db.Ping(ctx); err != nil {
			return web.ComponentHealth{Status: web.StatusDown, Message: err.Error()}
		}
		return web.ComponentHealth{Status: web.StatusOK}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	addr := srv.Addr()

	resp, err := http.Get(fmt.Sprintf("http://%s/healthz", addr))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"status":"ok"`)
}

func TestE2E_HealthEndpoint_DBError(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	xuiClients := map[uint]interfaces.XUIClient{1: env.xui}
	sources := []database.Source{{Name: "main", XUIHost: "https://panel.example.com", XUIAPIToken: "test-api-token", XUIInboundID: 1, Active: true}}
	subService := service.NewSubscriptionService(env.db, xuiClients, sources, env.cfg, env.cfg.GlobalSubURL, &webhook.NoopSender{})
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig, subService, nil)

	srv.RegisterChecker("database", func(ctx context.Context) web.ComponentHealth {
		return web.ComponentHealth{Status: web.StatusDown, Message: "database connection failed"}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	addr := srv.Addr()

	resp, err := http.Get(fmt.Sprintf("http://%s/readyz", addr))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Contains(t, string(body), "NOT READY")
}

func TestE2E_ReadyEndpoint(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	xuiClients := map[uint]interfaces.XUIClient{1: env.xui}
	sources := []database.Source{{Name: "main", XUIHost: "https://panel.example.com", XUIAPIToken: "test-api-token", XUIInboundID: 1, Active: true}}
	subService := service.NewSubscriptionService(env.db, xuiClients, sources, env.cfg, env.cfg.GlobalSubURL, &webhook.NoopSender{})
	srv := web.NewServer("127.0.0.1:0", env.db, env.cfg, env.botConfig, subService, nil)

	srv.RegisterChecker("database", func(ctx context.Context) web.ComponentHealth {
		if err := env.db.Ping(ctx); err != nil {
			return web.ComponentHealth{Status: web.StatusDown, Message: err.Error()}
		}
		return web.ComponentHealth{Status: web.StatusOK}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	addr := srv.Addr()

	resp, err := http.Get(fmt.Sprintf("http://%s/readyz", addr))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
