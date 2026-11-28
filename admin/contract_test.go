package admin

// Контрактные тесты API панели: каждый хендлер должен отдавать ровно тот набор
// JSON-ключей, который читает соответствующая view. Панель отвечает success=true
// даже на мусор, поэтому тихое переименование поля или незаполненный снапшот
// всплывают только в браузере; здесь любое изменение ключа требует осознанной
// правки ожиданий в этом файле. Ожидания выписаны из json-тегов DTO в admin/*.go
// вручную и никогда не выводятся из рантайма.
//
// Источник багов, которые этот файл ловит: snapshot_taken не заполнялся (всегда
// false), а форма плана отправляла число там, где view зовёт .trim().

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contractFixtureID — явный id для всех фикстур одного теста. ID в разных
// таблицах независимы, а сидированные миграцией планы занимают id 1 и 2.
const contractFixtureID = 301

// contractRow декодирует тело ответа. Для списков берёт первый элемент items,
// для деталей — сам объект: набор ключей одной строки и есть контракт view.
func contractRow(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	if items, ok := payload["items"].([]any); ok {
		require.NotEmpty(t, items, "list response must contain at least one item")
		row, ok := items[0].(map[string]any)
		require.True(t, ok, "list items must be JSON objects")

		return row
	}

	return payload
}

// contractKeys возвращает отсортированный набор ключей одной строки.
func contractKeys(row map[string]any) []string {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return keys
}

// contractSeedFixtures сажает по одной строке каждой сущности через SQL:
// план -> нода -> продукт -> инвайт -> подписка -> заказ -> рассылка -> аудит.
// Тестовый DSN включает проверку внешних ключей, поэтому порядок важен.
func contractSeedFixtures(t *testing.T, db *database.Service) {
	t.Helper()

	now := time.Now().UTC()
	in30d := now.Add(30 * 24 * time.Hour)
	id := contractFixtureID

	exec := func(query string, args ...any) {
		t.Helper()
		require.NoError(t, db.GetDB().Exec(query, args...).Error)
	}

	exec(`INSERT INTO plans (id, name, is_active, devices_limit, traffic_limit)
		VALUES (?, 'contract-plan', 1, 3, 1073741824)`, id)
	exec(`INSERT INTO nodes (id, name, type, host, subscription_url, inbound_ids, is_active)
		VALUES (?, 'contract-node', '3x-ui', 'http://127.0.0.1:2053', 'https://contract.example/sub', '[1]', 1)`, id)
	exec(`INSERT INTO products (id, plan_id, name, duration_days, price_cents, currency, is_active)
		VALUES (?, ?, 'contract-product', 30, 39900, 'RUB', 1)`, id, id)
	exec(`INSERT INTO invites (code, referrer_tg_id) VALUES ('CTR301', 501000301)`)
	exec(`INSERT INTO subscriptions (id, telegram_id, username, client_id, subscription_id, status,
		plan_id, product_id, price_paid_cents, currency, expires_at, devices, ips)
		VALUES (?, 502000301, 'contract-user', 'contract-client-301', 'contract-sub-301', 'active',
		?, ?, 39900, 'RUB', ?, '[]', '[]')`, id, id, id, in30d)
	exec(`INSERT INTO orders (id, subscription_id, product_id, status, amount_cents, currency,
		payment_provider, provider_payment_id, created_at, expires_at)
		VALUES (?, ?, ?, 'paid', 39900, 'RUB', 'contract-provider', 'contract-payment-301', ?, ?)`,
		id, id, id, now, in30d)
	exec(`INSERT INTO broadcasts (id, name, filters, message_text, status, created_at, updated_at)
		VALUES (?, 'contract-broadcast', '{}', 'текст рассылки', 'completed', ?, ?)`, id, now, now)
	exec(`INSERT INTO audit_log (id, entity_type, entity_id, event, actor_type, details, new_state, created_at)
		VALUES (?, 'subscription', ?, 'subscription_created', 'admin', '{}', '{}', ?)`, id, id, now)
}

// contractSyncStub — минимальный дублёр сервисов подписок: панель реальной
// службы не касается, sync отвечает ровно так, как запрограммирован тест.
type contractSyncStub struct {
	syncErr error
}

func (s *contractSyncStub) AdminSetPlan(context.Context, uint, uint, int) (*database.Subscription, error) {
	return nil, nil
}

func (s *contractSyncStub) RevokeSubscription(context.Context, uint) (*database.Subscription, error) {
	return nil, nil
}

func (s *contractSyncStub) DeleteByID(context.Context, uint) (*database.Subscription, error) {
	return nil, nil
}

func (s *contractSyncStub) ResetSubscriptionTraffic(context.Context, uint) (service.TrafficResetResult, error) {
	return service.TrafficResetResult{}, nil
}

func (s *contractSyncStub) SyncSubscription(context.Context, uint) error {
	return s.syncErr
}

func TestAPI_Contract_KeySets(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	contractSeedFixtures(t, db)

	// Сервисы подписок нужны для detail и sync: без SetSubscriptionServices
	// мутирующие эндпоинты отвечают 503, а detail не соберётся вовсе.
	// Для чтения передаётся реальная база — detail собирается из настоящих строк.
	stub := &contractSyncStub{}
	srv.SetSubscriptionServices(db, stub, stub)

	id := contractFixtureID

	cases := []struct {
		name     string
		method   string
		path     string
		body     string
		expected []string
	}{
		{
			name:     "session",
			method:   http.MethodGet,
			path:     "/admin/api/session",
			expected: []string{"ok"},
		},
		// admin/src/views/Plans.svelte
		{
			name:   "plans list",
			method: http.MethodGet,
			path:   "/admin/api/plans",
			expected: []string{
				"created_at", "devices_limit", "id", "is_active", "name",
				"products_count", "subscriptions_count", "traffic_limit", "updated_at",
			},
		},
		// admin/src/views/Nodes.svelte
		{
			name:   "nodes list",
			method: http.MethodGet,
			path:   "/admin/api/nodes",
			expected: []string{
				"created_at", "has_api_token", "host", "id", "inbound_ids",
				"is_active", "name", "plans_count", "subscription_url",
				"subscriptions_count", "type", "updated_at",
			},
		},
		// admin/src/views/Products.svelte
		{
			name:   "products list",
			method: http.MethodGet,
			path:   "/admin/api/products",
			expected: []string{
				"created_at", "currency", "duration_days", "id", "is_active", "name",
				"orders_count", "plan_id", "plan_name", "price_cents",
				"subscriptions_count", "updated_at",
			},
		},
		// admin/src/views/Invites.svelte
		{
			name:   "invites list",
			method: http.MethodGet,
			path:   "/admin/api/invites",
			expected: []string{
				"code", "created_at", "referred_count", "referrer_tg_id", "subscriptions_count",
			},
		},
		// admin/src/views/Orders.svelte
		{
			name:   "orders list",
			method: http.MethodGet,
			path:   "/admin/api/orders",
			expected: []string{
				"activated_at", "amount_cents", "callback_amount_cents", "created_at",
				"currency", "duration_days", "id", "paid_at",
				"payment_creation_uncertain", "payment_provider", "plan_name",
				"product_name", "provider_fee_cents", "provider_payment_id", "status",
				"subscription_id", "telegram_id",
			},
		},
		// admin/src/views/OrdersDetail.svelte
		{
			name:   "orders detail",
			method: http.MethodGet,
			path:   fmt.Sprintf("/admin/api/orders/%d", id),
			expected: []string{
				"activated_at", "amount_cents", "callback_amount_cents", "created_at",
				"currency", "duration_days", "expires_at", "id", "paid_at",
				"payment_creation_uncertain", "payment_expires_at", "payment_provider",
				"payment_url", "plan_name", "product_id", "product_name",
				"provider_fee_cents", "provider_payment_id", "status",
				"subscription_id", "telegram_id",
			},
		},
		// admin/src/views/Broadcasts.svelte
		{
			name:   "broadcasts list",
			method: http.MethodGet,
			path:   "/admin/api/broadcasts",
			expected: []string{
				"blocked_count", "created_at", "failed_count", "finished_at", "id",
				"last_error", "name", "planned_at", "recipients_total", "retry_at",
				"sent_count", "started_at", "status", "unreachable_count",
			},
		},
		// admin/src/views/BroadcastDetail.svelte — snapshot_taken обязан
		// присутствовать даже когда он false: его отсутствие ломает бейдж.
		{
			name:   "broadcasts detail",
			method: http.MethodGet,
			path:   fmt.Sprintf("/admin/api/broadcasts/%d", id),
			expected: []string{
				"blocked", "blocked_count", "created_at", "delivered_count",
				"errors", "errors_preview", "failed_count", "filters",
				"filters_text", "finished_at", "id", "last_error",
				"message_text", "name", "planned_at", "recipients_total", "retry_at",
				"retry_count", "sent_count", "snapshot_taken", "started_at", "status",
				"unreachable", "unreachable_count",
			},
		},
		// admin/src/views/AuditLog.svelte
		{
			name:   "audit list",
			method: http.MethodGet,
			path:   "/admin/api/audit",
			expected: []string{
				"actor_id", "actor_type", "created_at", "details", "entity_id",
				"entity_type", "event", "id",
			},
		},
		// admin/src/views/Broadcasts.svelte — предпросмотр перед отправкой.
		{
			name:   "broadcasts preview",
			method: http.MethodPost,
			path:   "/admin/api/broadcasts/preview",
			body:   `{"name":"Превью","message_text":"*текст*","filters":{},"planned_at":null}`,
			expected: []string{
				"audience_count", "filters_text", "message_runs", "planned_at",
			},
		},
		// admin/src/views/AuditLog.svelte (раскрытие строки)
		{
			name:   "audit detail",
			method: http.MethodGet,
			path:   fmt.Sprintf("/admin/api/audit/%d", id),
			expected: []string{
				"actor_id", "actor_type", "created_at", "details", "entity_id",
				"entity_type", "event", "id", "new_state", "old_state",
			},
		},
		// admin/src/views/SubscriptionDetail.svelte
		{
			name:   "subscriptions detail",
			method: http.MethodGet,
			path:   fmt.Sprintf("/admin/api/subscriptions/%d", id),
			expected: []string{
				"created_at", "currency", "devices", "duration_days", "expires_at",
				"id", "invite_code", "ips", "last_request", "nodes", "nodes_active",
				"nodes_total", "plan_id", "plan_name", "price_paid_cents",
				"product_id", "referred_by", "reminders_sent", "started_at",
				"status", "sub_url", "subscription_id", "telegram_id",
				"traffic_reminders_sent", "updated_at", "username",
			},
		},
		// admin/src/views/Overview.svelte — верхний уровень; вложенные блоки
		// проверяются отдельным сабтестом ниже.
		{
			name:   "overview",
			method: http.MethodGet,
			path:   "/admin/api/overview",
			expected: []string{
				"audit", "bindings", "broadcasts", "build", "database",
				"expiring", "last_sub_request", "nodes", "orders",
				"subscriptions",
			},
		},
		{
			name:     "revenue",
			method:   http.MethodGet,
			path:     "/admin/api/revenue?days=7",
			expected: []string{"currencies", "currency", "days", "totals"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, srv, tc.method, tc.path, testToken, tc.body)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			assert.Equal(t, tc.expected, contractKeys(contractRow(t, rec.Body.Bytes())))
		})
	}

	// admin/src/views/Broadcasts.svelte — ключи каждого фрагмента сообщения.
	t.Run("broadcast preview runs", func(t *testing.T) {
		t.Parallel()

		rec := do(t, srv, http.MethodPost, "/admin/api/broadcasts/preview", testToken,
			`{"name":"Превью","message_text":"*текст*","filters":{},"planned_at":null}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		payload := contractRow(t, rec.Body.Bytes())

		runs, ok := payload["message_runs"].([]any)
		require.True(t, ok, "message_runs must be a JSON array")
		require.NotEmpty(t, runs)

		run, ok := runs[0].(map[string]any)
		require.True(t, ok, "message run must be a JSON object")
		assert.Equal(t,
			[]string{"bold", "code", "italic", "strike", "text", "type", "url"},
			contractKeys(run),
		)
	})

	// admin/src/views/Overview.svelte — ключи каждого вложенного блока.
	t.Run("overview blocks", func(t *testing.T) {
		t.Parallel()

		rec := do(t, srv, http.MethodGet, "/admin/api/overview", testToken, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		payload := contractRow(t, rec.Body.Bytes())

		blocks := map[string][]string{
			"build":         {"build_time", "commit", "uptime_seconds", "version"},
			"expiring":      {"days", "total"},
			"database":      {"path", "size_bytes"},
			"subscriptions": {"by_status", "total"},
			"nodes":         {"total"},
			"bindings":      {"by_status", "oldest_pending_at", "pending_total"},
			"orders":        {"by_status", "total", "uncertain"},
			"broadcasts":    {"by_status", "total"},
			"audit":         {"events_24h"},
		}

		names := make([]string, 0, len(blocks))
		for name := range blocks {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			block, ok := payload[name].(map[string]any)
			require.True(t, ok, "overview block %q must be a JSON object", name)
			assert.Equal(t, blocks[name], contractKeys(block), "block %q", name)
		}
	})

	// admin/src/views/Overview.svelte — карточка выручки: вложенные блоки
	// days[i] и totals.
	t.Run("revenue blocks", func(t *testing.T) {
		t.Parallel()

		rec := do(t, srv, http.MethodGet, "/admin/api/revenue?days=7", testToken, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		payload := contractRow(t, rec.Body.Bytes())
		assert.Equal(t, []string{"date", "orders", "revenue_cents"}, contractKeys(payload["days"].([]any)[0].(map[string]any)))
		assert.Len(t, payload["days"], 7)
		assert.Equal(t,
			[]string{"avg_check_cents", "canceled", "expired", "paid_orders", "revenue_cents"},
			contractKeys(payload["totals"].(map[string]any)),
		)
	})

	// admin/src/views/SubscriptionDetail.svelte — POST sync. warning не входит
	// в постоянный набор: ключ появляется только при частичном сбое (omitempty),
	// поэтому обе ветви проверяются раздельно.
	t.Run("sync warning absent on success", func(t *testing.T) {
		t.Parallel()

		rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/subscriptions/%d/sync", id), testToken, "{}")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		row := contractRow(t, rec.Body.Bytes())
		assert.Equal(t, []string{"subscription"}, contractKeys(row))
		assert.NotContains(t, row, "warning")
	})

	t.Run("sync warning present on partial failure", func(t *testing.T) {
		srvFail, dbFail := newTestServer(t)
		contractSeedFixtures(t, dbFail)

		failing := &contractSyncStub{syncErr: errors.New("node unreachable")}
		srvFail.SetSubscriptionServices(dbFail, failing, failing)

		rec := do(t, srvFail, http.MethodPost,
			fmt.Sprintf("/admin/api/subscriptions/%d/sync", contractFixtureID), testToken, "{}")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		row := contractRow(t, rec.Body.Bytes())
		assert.Equal(t, []string{"subscription", "warning"}, contractKeys(row))
		assert.NotEmpty(t, row["warning"], "warning must carry a non-empty message")
	})
}

// TestAPI_RouteRegistry pins the full panel route list (ADR-0002, P22): a route
// removed or renamed by accident must fail here, not surface as a dead link in
// the UI. Registration is proven by the mux behaviour: an unregistered path
// falls through to Go's plain-text "404 page not found", while any registered
// route answers with a JSON body (200, 400/404 from the handler, whatever).
func TestAPI_RouteRegistry(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	// Метод + путь-представитель (динамические сегменты — конкретным id).
	routes := []string{
		"GET /admin/api/session",
		"GET /admin/api/overview",
		"GET /admin/api/plans",
		"POST /admin/api/plans",
		"GET /admin/api/plans/1",
		"PUT /admin/api/plans/1",
		"DELETE /admin/api/plans/1",
		"GET /admin/api/plans/1/nodes",
		"PUT /admin/api/plans/1/nodes",
		"GET /admin/api/nodes",
		"POST /admin/api/nodes",
		"GET /admin/api/nodes/1",
		"PUT /admin/api/nodes/1",
		"DELETE /admin/api/nodes/1",
		"POST /admin/api/nodes/1/health",
		"GET /admin/api/nodes/1/live",
		"GET /admin/api/products",
		"POST /admin/api/products",
		"GET /admin/api/products/1",
		"PUT /admin/api/products/1",
		"DELETE /admin/api/products/1",
		"GET /admin/api/orders",
		"GET /admin/api/orders/1",
		"GET /admin/api/revenue",
		"GET /admin/api/broadcasts",
		"POST /admin/api/broadcasts",
		"GET /admin/api/broadcasts/audience",
		"POST /admin/api/broadcasts/preview",
		"GET /admin/api/broadcasts/1",
		"PUT /admin/api/broadcasts/1",
		"DELETE /admin/api/broadcasts/1",
		"POST /admin/api/broadcasts/1/cancel",
		"POST /admin/api/broadcasts/1/retry",
		"POST /admin/api/broadcasts/1/deactivate-blocked",
		"GET /admin/api/invites",
		"POST /admin/api/invites",
		"DELETE /admin/api/invites/somecode",
		"GET /admin/api/audit",
		"GET /admin/api/audit/events",
		"GET /admin/api/audit/1",
		"GET /admin/api/trials",
		"GET /admin/api/trials/stats",
		"GET /admin/api/subscriptions",
		"GET /admin/api/subscriptions/1",
		"GET /admin/api/subscriptions/1/traffic",
		"POST /admin/api/subscriptions/1/plan",
		"POST /admin/api/subscriptions/1/revoke",
		"POST /admin/api/subscriptions/1/reset-traffic",
		"POST /admin/api/subscriptions/1/sync",
		"DELETE /admin/api/subscriptions/1",
	}

	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			t.Parallel()

			method, path, _ := strings.Cut(route, " ")
			rec := do(t, srv, method, path, testToken, "{}")
			assert.NotEqual(t, "404 page not found\n", rec.Body.String(),
				"route %q is not registered in Handler()", route)
		})
	}

	// SPA-фолбэк живёт на "/admin/": корень отдаёт разметку, не 404 и не 401.
	rec := do(t, srv, http.MethodGet, "/admin/", testToken, "")
	assert.NotEqual(t, "404 page not found\n", rec.Body.String())
}
