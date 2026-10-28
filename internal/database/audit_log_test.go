package database

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAuditTestService создаёт in-memory сервис БД с применёнными миграциями.
func newAuditTestService(t *testing.T) *Service {
	t.Helper()

	db, err := NewService(filepath.Join(t.TempDir(), "audit_test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	return db
}

// createAuditTestSub создаёт подписку с корректным FK на free-план.
func createAuditTestSub(t *testing.T, db *Service, telegramID int64, clientID, subID string) *Subscription {
	t.Helper()

	plan, err := db.GetPlanByName(context.Background(), FreePlanName)
	require.NoError(t, err)

	sub := &Subscription{TelegramID: telegramID, ClientID: clientID, SubscriptionID: subID, PlanID: plan.ID, Status: string(SubscriptionStatusActive)}
	require.NoError(t, db.db.WithContext(context.Background()).Create(sub).Error)

	return sub
}

// TestAuditLog_InsertAndQuery verifies the append-only write path and the
// entity-scoped read helpers on a real migrated SQLite database.
func TestAuditLog_InsertAndQuery(t *testing.T) {
	t.Parallel()

	db := newAuditTestService(t)
	ctx := context.Background()

	sub := createAuditTestSub(t, db, 1001, "c1", "s1")

	// Создание подписки: событие с new_state и details.
	created := AuditSubscriptionCreated(sub, "INVITE1")
	require.NoError(t, db.InsertAuditLog(ctx, created))
	assert.NotZero(t, created.ID, "GORM must backfill the audit event ID")
	assert.Equal(t, AuditEventSubscriptionCreated, created.Event)
	assert.Equal(t, EntitySubscription, created.EntityType)
	assert.Equal(t, ActorUser, created.ActorType)

	details := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(created.Details), &details))
	assert.Equal(t, float64(1001), details["telegram_id"])
	assert.Equal(t, "INVITE1", details["invite_code"])

	var newState map[string]any
	require.NotNil(t, created.NewState)
	require.NoError(t, json.Unmarshal([]byte(*created.NewState), &newState))
	assert.Equal(t, string(SubscriptionStatusActive), newState["status"])

	// Смена тарифа: old/new содержат только изменившиеся поля.
	before := map[string]any{"status": string(SubscriptionStatusActive), "plan_id": uint(1), "expires_at": nil}
	sub.PlanID = 2
	planChanged := AuditPlanChanged(sub, before, 30, 999)
	require.NoError(t, db.InsertAuditLog(ctx, planChanged))

	var oldState map[string]any
	require.NotNil(t, planChanged.OldState)
	require.NoError(t, json.Unmarshal([]byte(*planChanged.OldState), &oldState))
	assert.InDelta(t, float64(1), oldState["plan_id"], 0)
	assert.Equal(t, ActorAdmin, planChanged.ActorType)
	require.NotNil(t, planChanged.ActorID)
	assert.Equal(t, "999", *planChanged.ActorID)

	// NULL-поля: событие без old/new state.
	syncDegraded := AuditSyncRunDegraded(2, assert.AnError)
	require.NoError(t, db.InsertAuditLog(ctx, syncDegraded))
	assert.Nil(t, syncDegraded.EntityID)
	assert.Nil(t, syncDegraded.OldState)

	// Чтение по сущности: все три события одной подписки, новые первыми.
	events, err := db.GetAuditLogByEntity(ctx, EntitySubscription, formatSubID(sub), 10)
	require.NoError(t, err)
	require.Len(t, events, 2, "only subscription-entity events are returned")

	byEvent, err := db.GetAuditLogByEvent(ctx, AuditEventSubscriptionCreated, 10)
	require.NoError(t, err)
	require.Len(t, byEvent, 1)
	assert.Equal(t, created.ID, byEvent[0].ID)

	recent, err := db.GetRecentAuditLog(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, recent, 3)
}

// TestAuditLog_SurvivesSubscriptionDeletion verifies the core audit guarantee:
// the event history remains queryable after the subscription row is gone
// (admin /del physically deletes; audit_log has no FK).
func TestAuditLog_SurvivesSubscriptionDeletion(t *testing.T) {
	t.Parallel()

	db := newAuditTestService(t)
	ctx := context.Background()

	sub := createAuditTestSub(t, db, 1002, "c2", "s2")

	event := AuditSubscriptionDeleted(sub, 42)
	require.NoError(t, db.InsertAuditLog(ctx, event))

	require.NoError(t, db.db.WithContext(ctx).Delete(sub).Error)

	events, err := db.GetAuditLogByEntity(ctx, EntitySubscription, formatSubID(sub), 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, AuditEventSubscriptionDeleted, events[0].Event)

	details := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(events[0].Details), &details))
	assert.Equal(t, float64(1002), details["telegram_id"], "event must stay self-sufficient after row deletion")
}

// TestAuditLog_MarshalSemantics pins the JSON contract of the state helpers.
func TestAuditLog_MarshalSemantics(t *testing.T) {
	t.Parallel()

	// nil map -> NULL (nil pointer), not '{}'.
	assert.Nil(t, marshalAuditJSON(nil))
	assert.Nil(t, marshalAuditJSON(map[string]any{}))

	// empty entity/actor id -> NULL.
	assert.Nil(t, marshalAuditJSONString(""))
	assert.NotNil(t, marshalAuditJSONString("42"))

	// empty details -> '{}'.
	assert.Equal(t, "{}", marshalAuditDetails(nil))

	// diffState: only changed keys, removed keys become nil in "after".
	expiry := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	before := map[string]any{"plan_id": uint(1), "status": "active", "expires_at": expiry}
	after := map[string]any{"plan_id": uint(2), "status": "active"}

	oldJSON, newJSON := diffState(before, after)
	require.NotNil(t, oldJSON)
	require.NotNil(t, newJSON)

	var oldState, newState map[string]any
	require.NoError(t, json.Unmarshal([]byte(*oldJSON), &oldState))
	require.NoError(t, json.Unmarshal([]byte(*newJSON), &newState))
	assert.NotContains(t, oldState, "status", "unchanged fields must be omitted")
	assert.Contains(t, oldState, "plan_id")
	assert.Contains(t, oldState, "expires_at")
	assert.Nil(t, newState["expires_at"], "removed keys surface as null in new_state")

	// diffState with no changes -> nil pair.
	unchangedOld, unchangedNew := diffState(before, before)
	assert.Nil(t, unchangedOld)
	assert.Nil(t, unchangedNew)
}

// TestAuditLog_TableSchema checks the migrated table shape: the append-only
// columns exist with their declared nullability. Named-column assertions keep
// the test meaningful when columns are added or removed (no magic counts).
func TestAuditLog_TableSchema(t *testing.T) {
	t.Parallel()

	db := newAuditTestService(t)
	ctx := context.Background()

	rows, err := db.db.WithContext(ctx).Raw(
		"SELECT name, \"notnull\" FROM pragma_table_info('audit_log')").Rows()
	require.NoError(t, err)
	defer rows.Close()

	notNull := map[string]bool{}
	for rows.Next() {
		var name string
		var notNullFlag int
		require.NoError(t, rows.Scan(&name, &notNullFlag))
		notNull[name] = notNullFlag == 1
	}
	require.NoError(t, rows.Err())

	for _, required := range []string{"id", "entity_type", "entity_id", "event", "actor_type", "actor_id", "old_state", "new_state", "details", "created_at"} {
		_, ok := notNull[required]
		assert.True(t, ok, "audit_log must contain column %q", required)
	}
	// Columns declared NOT NULL in migration 039 (id is INTEGER PRIMARY KEY,
	// which PRAGMA reports as nullable, so it is excluded here).
	for _, required := range []string{"entity_type", "event", "actor_type", "details", "created_at"} {
		assert.True(t, notNull[required], "audit_log.%s must be NOT NULL", required)
	}

	var indexCount int
	require.NoError(t, db.db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='audit_log'").Scan(&indexCount).Error)
	assert.GreaterOrEqual(t, indexCount, 3, "audit_log lookup indexes must exist")
}
