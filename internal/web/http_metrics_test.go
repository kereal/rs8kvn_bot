package web

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/subserver"
)

// Смоук по реальному TCP: поднимает сервер, дёргает /sub/<удалённая подписка>
// и проверяет, что /metrics отдаёт нормированный path, счётчик мёртвых
// подписок и заранее опубликованные нулевые серии. Регрессия любой из этих
// частей ломает либо кардинальность, либо алерты, которые читают нули.
func TestHTTPMetrics_RealEndpoint(t *testing.T) {
	srv := NewServer(":0", notFoundFake(), &config.Config{}, "testbot", nil, subserver.NewService(config.SubServerCacheTTL))
	require.NoError(t, srv.Start(context.Background()))

	defer func() { _ = srv.Stop(context.Background()) }()

	base := "http://" + srv.Addr()

	for range 3 {
		resp, err := http.Get(base + "/sub/0123456789")
		require.NoError(t, err)

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.NoError(t, readErr)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.Equal(t, "Subscription not found", string(body))
	}

	// Сканерский путь не должен становиться меткой: сырой path в лейбле — это
	// новая временная серия на каждый запрос, и она живёт в TSDB вечно.
	// Проверяем обе стороны нормировки: самого пути в выдаче нет, а его серия
	// схлопнута в общий бакет other.
	scannerResp, err := http.Get(base + "/wp-login.php")
	require.NoError(t, err)
	_ = scannerResp.Body.Close()

	resp, err := http.Get(base + "/metrics")
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	text := string(raw)

	for _, want := range []string{
		`http_requests_total{code="404",method="GET",path="/sub/:id"}`,
		"subserver_not_found_total",
		`subserver_requests_total{format="xray"}`,
		`subscription_sync_total{result="ok"} 0`,
		"db_pool_waits_total",
	} {
		require.True(t, strings.Contains(text, want), "в /metrics нет %q", want)
	}

	require.False(t, strings.Contains(text, "wp-login.php"),
		"сырой scanner-путь не должен становиться меткой")
	require.True(t, strings.Contains(text, `path="other"`),
		"scanner-путь должен схлопываться в бакет other")
}
