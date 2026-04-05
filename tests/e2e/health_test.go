package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"rs8kvn_bot/internal/service"
	"rs8kvn_bot/internal/web"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_HealthEndpoint(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	addr := getFreePort(t)
	srv := web.NewServer(addr, env.db, env.xui, env.cfg, env.botConfig, subService, nil)

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

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/healthz", addr))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), `"status":"ok"`)
}

func TestE2E_HealthEndpoint_DBError(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	addr := getFreePort(t)
	srv := web.NewServer(addr, env.db, env.xui, env.cfg, env.botConfig, subService, nil)

	srv.RegisterChecker("database", func(ctx context.Context) web.ComponentHealth {
		return web.ComponentHealth{Status: web.StatusDown, Message: "database connection failed"}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Start(ctx)
	require.NoError(t, err)
	defer srv.Stop(context.Background())

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/readyz", addr))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Contains(t, string(body), "NOT READY")
}

func TestE2E_ReadyEndpoint(t *testing.T) {
	t.Parallel()
	env := setupE2EEnv(t)
	defer env.db.Close()

	subService := service.NewSubscriptionService(env.db, env.xui, env.cfg)
	addr := getFreePort(t)
	srv := web.NewServer(addr, env.db, env.xui, env.cfg, env.botConfig, subService, nil)

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

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/readyz", addr))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func getFreePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return fmt.Sprintf("127.0.0.1:%d", port)
}
