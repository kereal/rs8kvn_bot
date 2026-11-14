package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDeactivator records the deactivate-blocked call and returns a canned
// result, so HTTP tests verify the panel calls the service instead of
// revoking subscriptions itself (spec 06: the panel never sends messages).
type fakeDeactivator struct {
	calledWith uint
	result     *service.DeactivationResult
	err        error
}

func (f *fakeDeactivator) DeactivateBlockedBroadcastRecipients(_ context.Context, broadcastID uint) (*service.DeactivationResult, error) {
	f.calledWith = broadcastID

	return f.result, f.err
}

// seedBroadcastAudience creates count active free-plan subscriptions so the
// default broadcast filter selects a known audience. The test DSN enforces
// the plans foreign key, so PlanID is set to the seeded free plan.
func seedBroadcastAudience(t *testing.T, db *database.Service, firstID, count int64) {
	t.Helper()

	plan, err := db.GetPlanByName(context.Background(), database.FreePlanName)
	require.NoError(t, err)

	for i := range count {
		sub := &database.Subscription{
			TelegramID:     firstID + i,
			Username:       fmt.Sprintf("user%d", firstID+i),
			ClientID:       fmt.Sprintf("bc-client-%d", firstID+i),
			SubscriptionID: fmt.Sprintf("bc-sub-%d", firstID+i),
			Status:         string(database.SubscriptionStatusActive),
			PlanID:         plan.ID,
		}
		require.NoError(t, db.GetDB().Create(sub).Error)
	}
}

// seedBroadcast inserts a broadcast row with the given status.
func seedBroadcast(t *testing.T, db *database.Service, name, status string) *database.Broadcast {
	t.Helper()

	b := &database.Broadcast{
		Name:        name,
		MessageText: "Текст рассылки",
		Filters:     "{}",
		Status:      status,
	}
	require.NoError(t, db.GetDB().Create(b).Error)

	return b
}

func broadcastPayloadJSON(name, text, filters string, planned string) string {
	plannedPart := "null"
	if planned != "" {
		plannedPart = fmt.Sprintf("%q", planned)
	}

	return fmt.Sprintf(`{"name":%q,"message_text":%q,"filters":%s,"planned_at":%s}`, name, text, filters, plannedPart)
}

func auditRowsByEvent(t *testing.T, db *database.Service, event string) []database.AuditLog {
	t.Helper()

	var rows []database.AuditLog
	require.NoError(t, db.GetDB().Where("event = ?", event).Order("id ASC").Find(&rows).Error)

	return rows
}

func TestAPI_BroadcastsCreate(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	seedBroadcastAudience(t, db, 700001, 3)

	body := broadcastPayloadJSON("Чёрная пятница", "Скидки!", "{}", "")
	rec := do(t, srv, http.MethodPost, "/admin/api/broadcasts", testToken, body)
	require.Equal(t, http.StatusCreated, rec.Code)

	var detail map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))

	assert.NotZero(t, detail["id"])
	assert.Equal(t, "Чёрная пятница", detail["name"])
	assert.Equal(t, "scheduled", detail["status"])
	assert.Equal(t, "Скидки!", detail["message_text"])
	// filters отдаются распарсенным объектом, message-текст — целиком (черновик).
	assert.Equal(t, map[string]any{}, detail["filters"])
	assert.NotEmpty(t, detail["filters_text"])
	assert.Contains(t, detail, "errors_preview")
	assert.Contains(t, detail, "snapshot_taken")

	// Полный delivery_report не отдаётся нигде (§4).
	assert.NotContains(t, rec.Body.String(), "delivery_report")

	// Аудит: broadcast_created от админа, без полного текста.
	rows := auditRowsByEvent(t, db, "broadcast_created")
	require.Len(t, rows, 1)
	assert.Equal(t, string(database.EntityBroadcast), string(rows[0].EntityType))
	assert.Equal(t, string(database.ActorAdmin), string(rows[0].ActorType))
	require.NotNil(t, rows[0].NewState)
	assert.NotContains(t, *rows[0].NewState, "Скидки!")
	assert.Contains(t, *rows[0].NewState, "sha256")
}

func TestAPI_BroadcastsCreate_EmptyAudience(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodPost, "/admin/api/broadcasts", testToken,
		broadcastPayloadJSON("Пусто", "Текст", "{}", ""))
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "in_use")

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	details, _ := payload["details"].(map[string]any)
	assert.Equal(t, float64(0), details["audience"])
}

func TestAPI_BroadcastsCreate_Validation(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	seedBroadcastAudience(t, db, 710001, 1)

	cases := []struct {
		name    string
		body    string
		details string
	}{
		{"empty name", broadcastPayloadJSON("", "Текст", "{}", ""), "name"},
		{"empty text", broadcastPayloadJSON("Название", "", "{}", ""), "message_text"},
		{"unknown plan_type", broadcastPayloadJSON("Название", "Текст", `{"plan_type":"vip"}`, ""), "filters"},
		{"unknown status", broadcastPayloadJSON("Название", "Текст", `{"subscription_status":"expired"}`, ""), "filters"},
		{"planned in past", broadcastPayloadJSON("Название", "Текст", "{}", "2020-01-01T00:00:00Z"), "planned_at"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, srv, http.MethodPost, "/admin/api/broadcasts", testToken, tc.body)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), "validation")
			assert.Contains(t, rec.Body.String(), tc.details)
		})
	}
}

func TestAPI_BroadcastsCreate_RequiresToken(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodPost, "/admin/api/broadcasts", "",
		broadcastPayloadJSON("Название", "Текст", "{}", ""))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAPI_BroadcastsUpdate_Draft(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	seedBroadcastAudience(t, db, 720001, 2)
	b := seedBroadcast(t, db, "Старое имя", string(database.BroadcastStatusScheduled))

	body := broadcastPayloadJSON("Новое имя", "Новый текст", `{"subscription_status":"all"}`, "")
	rec := do(t, srv, http.MethodPut, fmt.Sprintf("/admin/api/broadcasts/%d", b.ID), testToken, body)
	require.Equal(t, http.StatusOK, rec.Code)

	var detail map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	assert.Equal(t, "Новое имя", detail["name"])
	assert.Equal(t, "Новый текст", detail["message_text"])
	assert.Equal(t, map[string]any{"subscription_status": "all"}, detail["filters"])

	// Аудит: old/new содержат только изменившиеся поля, текст — хэшем.
	rows := auditRowsByEvent(t, db, "broadcast_updated")
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].OldState)
	require.NotNil(t, rows[0].NewState)
	assert.Contains(t, *rows[0].OldState, "Старое имя")
	assert.Contains(t, *rows[0].NewState, "Новое имя")
	assert.NotContains(t, *rows[0].OldState, "Текст рассылки")
	assert.NotContains(t, *rows[0].NewState, "Новый текст")
	assert.Contains(t, *rows[0].NewState, "sha256")
}

func TestAPI_BroadcastsUpdate_Running(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	b := seedBroadcast(t, db, "Идёт", string(database.BroadcastStatusRunning))

	rec := do(t, srv, http.MethodPut, fmt.Sprintf("/admin/api/broadcasts/%d", b.ID), testToken,
		broadcastPayloadJSON("Правка", "Текст", "{}", ""))
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "in_use")
	assert.Contains(t, rec.Body.String(), "running")
}

func TestAPI_BroadcastsCancel(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	b := seedBroadcast(t, db, "Отменяемая", string(database.BroadcastStatusRunning))

	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/broadcasts/%d/cancel", b.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var detail map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	assert.Equal(t, "canceled", detail["status"])

	// Аудит отмены.
	rows := auditRowsByEvent(t, db, "broadcast_canceled")
	require.Len(t, rows, 1)
	assert.Equal(t, string(database.ActorAdmin), string(rows[0].ActorType))

	// Повторная отмена завершённой — 409 stale.
	rec = do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/broadcasts/%d/cancel", b.ID), testToken, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "stale")
}

func TestAPI_BroadcastsRetry(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	b := seedBroadcast(t, db, "Провалилась", string(database.BroadcastStatusFailed))
	require.NoError(t, db.GetDB().Model(b).Update("retry_count", 2).Error)

	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/broadcasts/%d/retry", b.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var detail map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	assert.Equal(t, "scheduled", detail["status"])
	// last_error сохраняется: админ должен видеть причину провала.
	assert.NotEmpty(t, detail["retry_count"])

	rows := auditRowsByEvent(t, db, "broadcast_retried")
	require.Len(t, rows, 1)

	// Повтор не-failed — 409 stale.
	rec = do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/broadcasts/%d/retry", b.ID), testToken, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "stale")
}

func TestAPI_BroadcastsDeactivate_Completed(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	fake := &fakeDeactivator{result: &service.DeactivationResult{Targets: 5, Revoked: 4, Failed: 1}}
	srv.SetBroadcastDeactivator(fake)

	b := seedBroadcast(t, db, "Завершённая", string(database.BroadcastStatusCompleted))
	require.NoError(t, db.GetDB().Model(b).Updates(map[string]any{
		"blocked_count": 3, "unreachable_count": 2,
	}).Error)

	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/broadcasts/%d/deactivate-blocked", b.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, float64(5), payload["targets"])
	assert.Equal(t, float64(4), payload["revoked"])
	assert.Equal(t, float64(1), payload["failed"])
	assert.Equal(t, float64(b.ID), float64(fake.calledWith), "panel must call the service with the broadcast id")
}

func TestAPI_BroadcastsDeactivate_PartialFailure(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	fake := &fakeDeactivator{
		result: &service.DeactivationResult{Targets: 3, Revoked: 2, Failed: 1},
		err:    fmt.Errorf("deprovision subscription 9: panel unreachable"),
	}
	srv.SetBroadcastDeactivator(fake)

	b := seedBroadcast(t, db, "Частичный сбой", string(database.BroadcastStatusFailed))
	require.NoError(t, db.GetDB().Model(b).Update("blocked_count", 3).Error)

	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/broadcasts/%d/deactivate-blocked", b.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, float64(2), payload["revoked"])
	assert.Equal(t, float64(1), payload["failed"])
	// Частичный сбой доезжает до панели, чтобы UI показал предупреждение.
	assert.Contains(t, payload["error"], "panel unreachable")
}

func TestAPI_BroadcastsDeactivate_Guards(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	fake := &fakeDeactivator{}
	srv.SetBroadcastDeactivator(fake)

	running := seedBroadcast(t, db, "Идёт", string(database.BroadcastStatusRunning))
	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/broadcasts/%d/deactivate-blocked", running.ID), testToken, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "stale")

	// Завершённая, но без заблокировавших — 409 in_use, details.blocked=0.
	done := seedBroadcast(t, db, "Чистая", string(database.BroadcastStatusCompleted))
	rec = do(t, srv, http.MethodPost, fmt.Sprintf("/admin/api/broadcasts/%d/deactivate-blocked", done.ID), testToken, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "in_use")

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	details, _ := payload["details"].(map[string]any)
	assert.Equal(t, float64(0), details["blocked"])
	assert.Zero(t, fake.calledWith, "service must not be called for guarded statuses")
}

func TestAPI_BroadcastsDelete(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	running := seedBroadcast(t, db, "Идёт", string(database.BroadcastStatusRunning))
	done := seedBroadcast(t, db, "Завершённая", string(database.BroadcastStatusCompleted))

	// running удалять нельзя.
	rec := do(t, srv, http.MethodDelete, fmt.Sprintf("/admin/api/broadcasts/%d", running.ID), testToken, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "in_use")

	// Завершённая удаляется.
	rec = do(t, srv, http.MethodDelete, fmt.Sprintf("/admin/api/broadcasts/%d", done.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, true, payload["deleted"])

	// Аудит удаления.
	rows := auditRowsByEvent(t, db, "broadcast_deleted")
	require.Len(t, rows, 1)

	var count int64
	require.NoError(t, db.GetDB().Model(&database.Broadcast{}).Where("id = ?", done.ID).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAPI_BroadcastsList_NoDeliveryReport(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	seedBroadcast(t, db, "Первая", string(database.BroadcastStatusCompleted))
	seedBroadcast(t, db, "Вторая", string(database.BroadcastStatusScheduled))

	rec := do(t, srv, http.MethodGet, "/admin/api/broadcasts", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, int64(2), payload.Total)
	require.Len(t, payload.Items, 2)
	// Сортировка id DESC — новее сверху.
	assert.Equal(t, "Вторая", payload.Items[0]["name"])

	// Полный отчёт и текст сообщения в список не попадают.
	assert.NotContains(t, rec.Body.String(), "delivery_report")
	assert.NotContains(t, rec.Body.String(), "message_text")

	// Фильтр по статусу.
	rec = do(t, srv, http.MethodGet, "/admin/api/broadcasts?status=scheduled", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, int64(1), payload.Total)
}

func TestAPI_BroadcastsDetail_ErrorsPreview(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	b := seedBroadcast(t, db, "С ошибками", string(database.BroadcastStatusFailed))

	report := map[string]any{
		"delivered": []int64{1, 2},
		"errors":    []map[string]any{},
	}
	for i := range 25 {
		report["errors"] = append(report["errors"].([]map[string]any), map[string]any{
			"telegram_id": 800000 + i,
			"error":       fmt.Sprintf("ошибка %d", i),
		})
	}
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	require.NoError(t, db.GetDB().Model(b).Update("delivery_report", string(raw)).Error)

	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/admin/api/broadcasts/%d", b.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var detail map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))

	preview, ok := detail["errors_preview"].([]any)
	require.True(t, ok, "errors_preview must be an array")
	assert.Len(t, preview, 20, "preview is capped at 20 entries")
	first, _ := preview[0].(map[string]any)
	assert.Equal(t, float64(800000), first["telegram_id"])
}

// Разобранный delivery_report на карточке: blocked/unreachable — списки id,
// errors — с лимитом 20, delivered_count — из списка доставленных. Полный
// delivery_report по-прежнему наружу не отдаётся (§4).
func TestAPI_BroadcastsDetail_DeliveryReport(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	b := seedBroadcast(t, db, "С отчётом", string(database.BroadcastStatusCompleted))

	report := map[string]any{
		"delivered":   []int64{700001, 700002},
		"blocked":     []int64{710001, 710002, 710003},
		"unreachable": []int64{720001},
		"errors":      []map[string]any{},
	}
	for i := range 25 {
		report["errors"] = append(report["errors"].([]map[string]any), map[string]any{
			"telegram_id": 800000 + i,
			"error":       fmt.Sprintf("ошибка %d", i),
		})
	}
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	require.NoError(t, db.GetDB().Model(b).Update("delivery_report", string(raw)).Error)

	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/admin/api/broadcasts/%d", b.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var detail map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	assert.NotContains(t, rec.Body.String(), "delivery_report")

	assert.Equal(t, float64(2), detail["delivered_count"])
	blocked, ok := detail["blocked"].([]any)
	require.True(t, ok, "blocked must be an array")
	assert.Equal(t, []any{float64(710001), float64(710002), float64(710003)}, blocked)
	unreachable, ok := detail["unreachable"].([]any)
	require.True(t, ok, "unreachable must be an array")
	assert.Equal(t, []any{float64(720001)}, unreachable)

	errs, ok := detail["errors"].([]any)
	require.True(t, ok, "errors must be an array")
	assert.Len(t, errs, 20, "errors are capped at 20 entries")
	first, _ := errs[0].(map[string]any)
	assert.Equal(t, float64(800000), first["telegram_id"])
	assert.Equal(t, "ошибка 0", first["error"])
}

// Без отчёта (пустой "{}") разобранные списки — пустые массивы, а не null.
func TestAPI_BroadcastsDetail_DeliveryReportEmpty(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	b := seedBroadcast(t, db, "Без отчёта", string(database.BroadcastStatusCompleted))
	require.NoError(t, db.GetDB().Model(b).Update("delivery_report", "{}").Error)

	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/admin/api/broadcasts/%d", b.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var detail map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	assert.Equal(t, float64(0), detail["delivered_count"])
	for _, key := range []string{"blocked", "unreachable", "errors"} {
		arr, ok := detail[key].([]any)
		require.True(t, ok, "%s must be an array", key)
		assert.Empty(t, arr)
	}
}

// Деталь и список читают флаг «снимок аудитории» из одного источника:
// раньше деталь всегда отдавала false, потому что флаг просто не заполнялся.
func TestAPI_BroadcastsDetail_SnapshotTaken(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	b := seedBroadcast(t, db, "Снятый снимок", string(database.BroadcastStatusCompleted))
	state := `{"snapshot":true,"recipients":[{"telegram_id":900001,"status":"sent","attempts":1}]}`
	require.NoError(t, db.GetDB().Model(b).Update("recipients_state", state).Error)

	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/admin/api/broadcasts/%d", b.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var detail map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	assert.Equal(t, true, detail["snapshot_taken"])
}

func TestAPI_BroadcastsAudience(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	seedBroadcastAudience(t, db, 810001, 2)

	rec := do(t, srv, http.MethodGet, "/admin/api/broadcasts/audience", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, float64(2), payload["count"])

	// Некорректное значение фильтра — 400, а не молчаливое игнорирование.
	rec = do(t, srv, http.MethodGet, "/admin/api/broadcasts/audience?plan_type=vip", testToken, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Даты только в RFC3339.
	rec = do(t, srv, http.MethodGet, "/admin/api/broadcasts/audience?registered_after=01.11.2026", testToken, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAPI_BroadcastsPreview(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	seedBroadcastAudience(t, db, 820001, 3)

	body := broadcastPayloadJSON("Чёрная пятница", "*Скидка* 50%! [сайт](https://example.com)", "{}", "")
	rec := do(t, srv, http.MethodPost, "/admin/api/broadcasts/preview", testToken, body)
	require.Equal(t, http.StatusOK, rec.Code)

	var preview struct {
		AudienceCount int64   `json:"audience_count"`
		FiltersText   string  `json:"filters_text"`
		PlannedAt     *string `json:"planned_at"`
		MessageRuns   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			URL  string `json:"url"`
			Bold bool   `json:"bold"`
			Code bool   `json:"code"`
		} `json:"message_runs"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &preview))

	assert.Equal(t, int64(3), preview.AudienceCount)
	assert.Equal(t, "Все активные пользователи", preview.FiltersText)
	assert.Nil(t, preview.PlannedAt)
	require.Len(t, preview.MessageRuns, 3)
	assert.Equal(t, "Скидка", preview.MessageRuns[0].Text)
	assert.True(t, preview.MessageRuns[0].Bold)
	assert.Equal(t, " 50%! ", preview.MessageRuns[1].Text)
	assert.Equal(t, "сайт", preview.MessageRuns[2].Text)
	assert.Equal(t, "https://example.com", preview.MessageRuns[2].URL)

	// Предпросмотр ничего не создаёт: в таблице по-прежнему пусто.
	var count int64
	require.NoError(t, db.GetDB().Model(&database.Broadcast{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestAPI_BroadcastsPreview_Guards(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	seedBroadcastAudience(t, db, 830001, 1)

	// Пустая аудитория — не ошибка предпросмотра: число показывается, отправка
	// блокируется в интерфейсе.
	body := broadcastPayloadJSON("Пусто", "Текст", `{"plan_type":"paid"}`, "")
	rec := do(t, srv, http.MethodPost, "/admin/api/broadcasts/preview", testToken, body)
	require.Equal(t, http.StatusOK, rec.Code)

	var preview struct {
		AudienceCount int64 `json:"audience_count"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &preview))
	assert.Equal(t, int64(0), preview.AudienceCount)

	// Невалидный черновик отклоняется тем же кодом, что и при создании.
	rec = do(t, srv, http.MethodPost, "/admin/api/broadcasts/preview", testToken,
		broadcastPayloadJSON("", "Текст", "{}", ""))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Неизвестное значение фильтра — 400, а не молчаливое игнорирование.
	rec = do(t, srv, http.MethodPost, "/admin/api/broadcasts/preview", testToken,
		broadcastPayloadJSON("Название", "Текст", `{"plan_type":"vip"}`, ""))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Без токена — 401.
	rec = do(t, srv, http.MethodPost, "/admin/api/broadcasts/preview", "",
		broadcastPayloadJSON("Название", "Текст", "{}", ""))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAPI_Broadcasts_NotFound(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/api/broadcasts/9999"},
		{http.MethodPut, "/admin/api/broadcasts/9999"},
		{http.MethodDelete, "/admin/api/broadcasts/9999"},
		{http.MethodPost, "/admin/api/broadcasts/9999/cancel"},
		{http.MethodPost, "/admin/api/broadcasts/9999/retry"},
		{http.MethodPost, "/admin/api/broadcasts/9999/deactivate-blocked"},
	}

	for _, tc := range cases {
		rec := do(t, srv, tc.method, tc.path, testToken, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, tc.path)
	}
}
