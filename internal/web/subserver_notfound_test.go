package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
)

// notFoundFake returns a repository that never finds the subscription, which is
// what a client polling a deleted link sees.
func notFoundFake() *testutil.SubscriptionRepositoryFake {
	db := testutil.NewSubscriptionRepositoryFake()
	db.GetWithPlanAndNodesFunc = func(context.Context, string) (*database.SubscriptionFull, error) {
		return nil, database.ErrSubscriptionNotFound
	}

	return db
}

func TestLogSubscriptionNotFound_CountsEveryRequest(t *testing.T) {
	// Не параллельный: счётчик общий для процесса, другие тесты пакета тоже
	// отвечают 404 и сдвигали бы дельту.
	srv := testServer(t, notFoundFake(), &config.Config{})

	before := promtestutil.ToFloat64(metrics.SubserverNotFoundTotal)

	const requests = 5

	for range requests {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/sub/0123456789", nil)
		srv.handleSubscription(w, r)

		require.Equal(t, http.StatusNotFound, w.Code)
	}

	after := promtestutil.ToFloat64(metrics.SubserverNotFoundTotal)

	assert.InDelta(t, requests, after-before, 0.0001,
		"каждый запрос по мёртвой подписке должен попадать в счётчик")
}

func TestLogSubscriptionNotFound_ThrottlesWarn(t *testing.T) {
	t.Parallel()

	srv := testServer(t, notFoundFake(), &config.Config{})
	require.NotNil(t, srv.notFoundLog, "троттлинг логов должен быть инициализирован")

	key := "0123456789|203.0.113.10"
	now := time.Now()

	assert.True(t, srv.notFoundLog.Allow(key, now), "первое обращение логируется уровнем WARN")
	assert.False(t, srv.notFoundLog.Allow(key, now), "повтор в пределах интервала не логируется")
	assert.True(t, srv.notFoundLog.Allow("abcdef0123|203.0.113.10", now),
		"другая подписка с того же IP — отдельный ключ")
	assert.True(t, srv.notFoundLog.Allow(key, now.Add(subserverNotFoundLogInterval)),
		"после интервала запись снова попадает в лог")
}
