package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPI_AuditList(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	ctx := context.Background()
	entityID := "1042"
	actorID := "42"

	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.InsertAuditLog(ctx, &database.AuditLog{
		EntityType: database.EntityOrder, EntityID: &entityID, Event: database.AuditEventOrderCreated,
		ActorType: database.ActorUser, ActorID: &actorID, Details: `{"product":"30 дней"}`,
		CreatedAt: base,
	}))
	require.NoError(t, db.InsertAuditLog(ctx, &database.AuditLog{
		EntityType: database.EntityOrder, EntityID: &entityID, Event: database.AuditEventPaymentConfirmed,
		ActorType: database.ActorPaymentProvider, Details: `{}`,
		CreatedAt: base.Add(time.Minute),
	}))

	rec := do(t, srv, http.MethodGet, "/admin/api/audit", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, int64(2), body.Total)
	require.Len(t, body.Items, 2)

	// Default order is newest first, server-side.
	assert.Equal(t, database.AuditEventPaymentConfirmed, body.Items[0]["event"])

	// List rows carry no state snapshots.
	for _, item := range body.Items {
		assert.NotContains(t, item, "old_state")
		assert.NotContains(t, item, "new_state")
	}

	// details arrives as a parsed object, not a string.
	assert.Equal(t, map[string]any{"product": "30 дней"}, body.Items[1]["details"])

	// entity_id stays a string, not a number.
	assert.Equal(t, "1042", body.Items[0]["entity_id"])
}

func TestAPI_AuditListDefaultOrderIsNewestFirst(t *testing.T) {
	t.Parallel()

	// The very first request the SPA sends has no explicit sort/order; the
	// server must still answer with the newest events first.
	srv, db := newTestServer(t)
	ctx := context.Background()

	entityID := "7"
	base := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	for i := range 3 {
		require.NoError(t, db.InsertAuditLog(ctx, &database.AuditLog{
			EntityType: database.EntityPlan, EntityID: &entityID, Event: database.AuditEventPlanChanged,
			ActorType: database.ActorAdmin, Details: "{}",
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}))
	}

	rec := do(t, srv, http.MethodGet, "/admin/api/audit", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 3)
	assert.Equal(t, base.Add(2*time.Minute).Format(time.RFC3339), body.Items[0]["created_at"],
		"empty query must default to created_at desc")
}

func TestAPI_AuditListFilters(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	ctx := context.Background()

	entityID := "1042"
	trialID := "trial-abc"
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	require.NoError(t, db.InsertAuditLog(ctx, &database.AuditLog{
		EntityType: database.EntityOrder, EntityID: &entityID, Event: database.AuditEventOrderCreated,
		ActorType: database.ActorUser, Details: "{}", CreatedAt: base,
	}))
	require.NoError(t, db.InsertAuditLog(ctx, &database.AuditLog{
		EntityType: database.EntitySubscription, EntityID: &trialID, Event: database.AuditEventTrialCreated,
		ActorType: database.ActorSystem, Details: "{}", CreatedAt: base.Add(time.Minute),
	}))

	cases := []struct {
		name  string
		query string
		total int64
	}{
		{"entity_type", "/admin/api/audit?entity_type=order", 1},
		{"entity_id", "/admin/api/audit?entity_id=trial-abc", 1},
		{"event", "/admin/api/audit?event=trial_created", 1},
		{"actor_type", "/admin/api/audit?actor_type=system", 1},
		{"combined", "/admin/api/audit?entity_type=order&event=order_created&actor_type=user", 1},
		{"from_to_inclusive", "/admin/api/audit?from=2026-11-01&to=2026-11-01", 2},
		{"from_excludes", "/admin/api/audit?from=2026-11-02", 0},
		{"limit", "/admin/api/audit?limit=1", 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, srv, http.MethodGet, tc.query, testToken, "")
			require.Equal(t, http.StatusOK, rec.Code, tc.query)

			var body struct {
				Total int64 `json:"total"`
			}

			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tc.total, body.Total, tc.query)
		})
	}
}

func TestAPI_AuditListRejectsBadFilters(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	require.NoError(t, db.InsertAuditLog(context.Background(), &database.AuditLog{
		EntityType: database.EntitySystem, Event: database.AuditEventSyncRunDegraded,
		ActorType: database.ActorWorker, Details: "{}",
		CreatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}))

	cases := []struct {
		query  string
		reason string
	}{
		{"/admin/api/audit?entity_type=evil", "unknown entity_type"},
		{"/admin/api/audit?actor_type=evil", "unknown actor_type"},
		{"/admin/api/audit?event=evil", "unknown event"},
		{"/admin/api/audit?sort=evil", "unknown sort"},
		{"/admin/api/audit?order=descending", "order outside asc/desc"},
		{"/admin/api/audit?offset=-1", "negative offset"},
		{"/admin/api/audit?limit=-5", "negative limit"},
		{"/admin/api/audit?from=01.11.2026", "non-ISO date"},
		{"/admin/api/audit?to=2026-13-40", "impossible date"},
	}

	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			t.Parallel()

			rec := do(t, srv, http.MethodGet, tc.query, testToken, "")
			assert.Equal(t, http.StatusBadRequest, rec.Code, tc.query)
		})
	}
}

func TestAPI_AuditDetail(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	ctx := context.Background()

	entityID := "1042"
	oldState := `{"plan":"free"}`
	newState := `{"plan":"paid"}`
	entry := &database.AuditLog{
		EntityType: database.EntitySubscription, EntityID: &entityID, Event: database.AuditEventPlanChanged,
		ActorType: database.ActorAdmin, ActorID: nil, OldState: &oldState, NewState: &newState,
		Details: `{"days":30}`, CreatedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
	}
	require.NoError(t, db.InsertAuditLog(ctx, entry))

	rec := do(t, srv, http.MethodGet, "/admin/api/audit/"+itoa(entry.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.JSONEq(t, `{"plan":"free"}`, string(body["old_state"]))
	assert.JSONEq(t, `{"plan":"paid"}`, string(body["new_state"]))
	assert.JSONEq(t, `{"days":30}`, string(body["details"]))
	assert.JSONEq(t, `"1042"`, string(body["entity_id"]))

	// A NULL old_state must arrive as JSON null, not as an absent key or "".
	created := &database.AuditLog{
		EntityType: database.EntitySubscription, EntityID: &entityID,
		Event:     database.AuditEventSubscriptionCreated,
		ActorType: database.ActorSystem, Details: "{}",
		CreatedAt: time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC),
	}
	require.NoError(t, db.InsertAuditLog(ctx, created))

	rec = do(t, srv, http.MethodGet, "/admin/api/audit/"+itoa(created.ID), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var createdBody map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &createdBody))
	assert.Contains(t, createdBody, "old_state")
	assert.Nil(t, createdBody["old_state"], "NULL state must marshal to null")
	assert.Contains(t, createdBody, "new_state")
	assert.Nil(t, createdBody["new_state"])
}

func TestAPI_AuditDetailNotFound(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodGet, "/admin/api/audit/424242", testToken, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAPI_AuditEvents(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodGet, "/admin/api/audit/events", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Events      []string `json:"events"`
		EntityTypes []string `json:"entity_types"`
		ActorTypes  []string `json:"actor_types"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, database.AuditEventNames(), body.Events)
	assert.Equal(t, database.EntityTypeNames(), body.EntityTypes)
	assert.Equal(t, database.ActorTypeNames(), body.ActorTypes)
}

func TestAPI_AuditRequiresToken(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	for _, path := range []string{"/admin/api/audit", "/admin/api/audit/events", "/admin/api/audit/1"} {
		rec := do(t, srv, http.MethodGet, path, "", "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code, path)
	}
}

func TestAPI_AuditInvalidDetailsSurvive(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)

	// Legacy row with malformed details: the API must wrap it as a string
	// instead of emitting an unparsable JSON field.
	require.NoError(t, db.GetDB().Exec(
		`INSERT INTO audit_log (entity_type, entity_id, event, actor_type, details, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"system", "1", database.AuditEventSyncRunDegraded, "worker", "not-json",
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	).Error)

	rec := do(t, srv, http.MethodGet, "/admin/api/audit", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Items []struct {
			Details any `json:"details"`
		} `json:"items"`
		Total int64 `json:"total"`
	}

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	assert.Equal(t, "not-json", body.Items[0].Details, "malformed details must arrive as a string")
}

func itoa(v uint) string {
	return strconv.FormatUint(uint64(v), 10)
}
