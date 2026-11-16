package database

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertAuditRow inserts one audit row with an explicit timestamp so ordering
// and date windows are deterministic.
func insertAuditRow(t *testing.T, svc *Service, row *AuditLog) *AuditLog {
	t.Helper()

	if row.Details == "" {
		row.Details = "{}"
	}

	require.NoError(t, svc.db.Create(row).Error)

	return row
}

func auditTestRows(t *testing.T, svc *Service) []*AuditLog {
	t.Helper()

	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	entityID := "1042"
	actorID := "42"

	rows := []*AuditLog{
		{
			EntityType: EntityOrder, EntityID: &entityID, Event: AuditEventOrderCreated,
			ActorType: ActorUser, ActorID: &actorID, Details: `{"product":"30 дней","amount_cents":39900}`,
			CreatedAt: base,
		},
		{
			EntityType: EntityOrder, EntityID: &entityID, Event: AuditEventPaymentConfirmed,
			ActorType: ActorPaymentProvider, Details: `{}`,
			CreatedAt: base.Add(2 * time.Minute),
		},
		{
			EntityType: EntitySubscription, EntityID: &entityID, Event: AuditEventSubscriptionRenewed,
			ActorType: ActorWorker, Details: `{"days":30}`,
			CreatedAt: base.Add(4 * time.Minute),
		},
		{
			EntityType: EntitySystem, Event: AuditEventSyncRunDegraded,
			ActorType: ActorWorker, Details: `{"failed":2}`,
			CreatedAt: base.Add(6 * time.Minute),
		},
	}

	for _, row := range rows {
		insertAuditRow(t, svc, row)
	}

	return rows
}

func TestListAuditLogPage_NoFiltersOrdersNewestFirst(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	rows := auditTestRows(t, svc)

	items, total, err := svc.ListAuditLogPage(ctx, AuditLogFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(len(rows)), total)
	require.Len(t, items, len(rows))

	// created_at DESC, id DESC: the newest event (sync_run_degraded) leads.
	assert.Equal(t, AuditEventSyncRunDegraded, items[0].Event)
	assert.Equal(t, AuditEventOrderCreated, items[len(items)-1].Event)

	for i := 1; i < len(items); i++ {
		prev, cur := items[i-1], items[i]
		if prev.CreatedAt.Equal(cur.CreatedAt) {
			assert.Greater(t, prev.ID, cur.ID, "ties must break by id desc")
		} else {
			assert.True(t, prev.CreatedAt.After(cur.CreatedAt), "items must descend by created_at")
		}
	}
}

func TestListAuditLogPage_AscendingOrder(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	auditTestRows(t, svc)

	items, _, err := svc.ListAuditLogPage(ctx, AuditLogFilter{Sort: "created_at", Desc: false})
	require.NoError(t, err)
	require.NotEmpty(t, items)
	assert.Equal(t, AuditEventOrderCreated, items[0].Event)
	assert.Equal(t, AuditEventSyncRunDegraded, items[len(items)-1].Event)
}

func TestListAuditLogPage_EntityFilter(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	auditTestRows(t, svc)

	items, total, err := svc.ListAuditLogPage(ctx, AuditLogFilter{
		EntityType: EntityOrder,
		EntityID:   "1042",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 2)

	for _, item := range items {
		assert.Equal(t, EntityOrder, item.EntityType)
		require.NotNil(t, item.EntityID)
		assert.Equal(t, "1042", *item.EntityID)
	}
}

func TestListAuditLogPage_EventAndActorFilters(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	auditTestRows(t, svc)

	byEvent, total, err := svc.ListAuditLogPage(ctx, AuditLogFilter{Event: AuditEventPaymentConfirmed})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, byEvent, 1)
	assert.Equal(t, AuditEventPaymentConfirmed, byEvent[0].Event)

	byActor, total, err := svc.ListAuditLogPage(ctx, AuditLogFilter{ActorType: ActorWorker})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, byActor, 2)

	for _, item := range byActor {
		assert.Equal(t, ActorWorker, item.ActorType)
	}
}

func TestListAuditLogPage_DateWindowIsInclusiveInUTC(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	day := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	entityID := "7"

	atStart := &AuditLog{
		EntityType: EntityPlan, EntityID: &entityID, Event: AuditEventPlanChanged,
		ActorType: ActorAdmin, Details: "{}", CreatedAt: day,
	}
	atEnd := &AuditLog{
		EntityType: EntityPlan, EntityID: &entityID, Event: AuditEventPlanChanged,
		ActorType: ActorAdmin, Details: "{}", CreatedAt: day.Add(23*time.Hour + 59*time.Minute + 59*time.Second),
	}
	nextDay := &AuditLog{
		EntityType: EntityPlan, EntityID: &entityID, Event: AuditEventPlanChanged,
		ActorType: ActorAdmin, Details: "{}", CreatedAt: day.Add(24 * time.Hour),
	}

	insertAuditRow(t, svc, atStart)
	insertAuditRow(t, svc, atEnd)
	insertAuditRow(t, svc, nextDay)

	from := day
	to := day.Add(23*time.Hour + 59*time.Minute + 59*time.Second + 999999999*time.Nanosecond)

	items, total, err := svc.ListAuditLogPage(ctx, AuditLogFilter{From: &from, To: &to})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "both boundary rows must be inside the window")
	require.Len(t, items, 2)

	for _, item := range items {
		assert.WithinDuration(t, day, item.CreatedAt, 24*time.Hour)
		assert.False(t, item.CreatedAt.Equal(nextDay.CreatedAt), "next day must stay outside")
	}
}

func TestListAuditLogPage_PaginationKeepsTotalStable(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	auditTestRows(t, svc)

	first, total, err := svc.ListAuditLogPage(ctx, AuditLogFilter{Limit: 2, Offset: 0})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	require.Len(t, first, 2)

	second, secondTotal, err := svc.ListAuditLogPage(ctx, AuditLogFilter{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Equal(t, total, secondTotal)
	require.Len(t, second, 2)

	assertNoPageOverlap(t, func(row AuditLog) uint { return row.ID }, first, second)
}

func TestListAuditLogPage_ClampsLimit(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	entityID := "1"

	for i := range 205 {
		insertAuditRow(t, svc, &AuditLog{
			EntityType: EntitySystem, EntityID: &entityID, Event: AuditEventSyncRunDegraded,
			ActorType: ActorWorker, Details: "{}", CreatedAt: base.Add(time.Duration(i) * time.Second),
		})
	}

	items, total, err := svc.ListAuditLogPage(ctx, AuditLogFilter{Limit: 1000})
	require.NoError(t, err)
	assert.Equal(t, int64(205), total)
	assert.Len(t, items, auditLogMaxLimit, "limit above the cap must be clamped to 200")
}

func TestListAuditLogPage_DefaultsAreNewestFirstWithFiftyRows(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	entityID := "9"

	for i := range 60 {
		insertAuditRow(t, svc, &AuditLog{
			EntityType: EntitySystem, EntityID: &entityID, Event: AuditEventSyncRunDegraded,
			ActorType: ActorWorker, Details: "{}", CreatedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	items, total, err := svc.ListAuditLogPage(ctx, AuditLogFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(60), total)
	assert.Len(t, items, auditLogDefaultLimit)
	assert.Equal(t, base.Add(59*time.Minute), items[0].CreatedAt, "zero-value filter must lead with the newest row")
}

func TestListAuditLogPage_DetailsRoundTrip(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	entityID := "5"
	insertAuditRow(t, svc, &AuditLog{
		EntityType: EntityOrder, EntityID: &entityID, Event: AuditEventOrderCreated,
		ActorType: ActorUser,
		Details:   `{"product":"30 дней","amount_cents":39900,"nested":{"paid":true},"ids":[1,2]}`,
		CreatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	})

	items, _, err := svc.ListAuditLogPage(ctx, AuditLogFilter{})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.JSONEq(t, `{"product":"30 дней","amount_cents":39900,"nested":{"paid":true},"ids":[1,2]}`, items[0].Details)
}

func TestListAuditLogPage_InvalidLegacyDetailsDoNotBreakReads(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	err := svc.db.Exec(
		`INSERT INTO audit_log (entity_type, entity_id, event, actor_type, details, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		string(EntitySystem), "1", AuditEventSyncRunDegraded, string(ActorWorker), "not-json", time.Now().UTC(),
	).Error
	require.NoError(t, err)

	items, total, err := svc.ListAuditLogPage(ctx, AuditLogFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, "not-json", items[0].Details, "malformed details must survive as a plain string")
}

func TestGetAuditLogByID_ReturnsEntryWithStates(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	entityID := "1042"
	oldState := `{"plan":"free"}`
	newState := `{"plan":"paid"}`
	row := insertAuditRow(t, svc, &AuditLog{
		EntityType: EntitySubscription, EntityID: &entityID, Event: AuditEventPlanChanged,
		ActorType: ActorAdmin, OldState: &oldState, NewState: &newState,
		Details: `{"days":30}`, CreatedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
	})

	got, err := svc.GetAuditLogByID(ctx, row.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, row.ID, got.ID)
	require.NotNil(t, got.OldState)
	require.NotNil(t, got.NewState)
	assert.JSONEq(t, oldState, *got.OldState)
	assert.JSONEq(t, newState, *got.NewState)
}

func TestGetAuditLogByID_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)

	got, err := svc.GetAuditLogByID(context.Background(), 999999)
	require.ErrorIs(t, err, ErrAuditLogNotFound)
	assert.Nil(t, got)
}

func TestGetRecentAuditLog_DelegatesToPage(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	auditTestRows(t, svc)

	recent, err := svc.GetRecentAuditLog(ctx, 3)
	require.NoError(t, err)

	page, total, err := svc.ListAuditLogPage(ctx, AuditLogFilter{Limit: 3, Desc: true})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	require.Len(t, recent, 3)
	require.Len(t, page, 3)

	for i := range page {
		assert.Equal(t, page[i].ID, recent[i].ID, "recent log must match the first page")
	}
}

// declaredAuditEventConstants parses audit_log.go and returns the value of every
// AuditEvent* constant. The panel's event filter is built from AuditEventNames(),
// and Go has no reflection over constants, so parsing the file is the only way
// to prove the registry and the declarations agree.
func declaredAuditEventConstants(t *testing.T) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "audit_log.go", nil, 0)
	require.NoError(t, err)

	var values []string

	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}

		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, "AuditEvent") || i >= len(spec.Values) {
				continue
			}

			lit, ok := spec.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}

			value, unquoteErr := strconv.Unquote(lit.Value)
			require.NoError(t, unquoteErr)
			values = append(values, value)
		}

		return true
	})

	require.NotEmpty(t, values, "audit_log.go must declare AuditEvent* constants")

	return values
}

func TestAuditRegistriesMatchConstants(t *testing.T) {
	t.Parallel()

	events := AuditEventNames()
	// The registry is hand-written, so it is checked against the constants that
	// audit_log.go actually declares: an AuditEvent* constant missing from the
	// registry silently never reaches the panel's event filter
	// (doc/specs/admin/07-audit-log.md, criterion 5).
	assert.ElementsMatch(t, declaredAuditEventConstants(t), events, "registry must list every declared AuditEvent* constant")

	assert.Equal(t, []string{
		"subscription", "order", "broadcast", "plan", "node", "product", "system",
	}, EntityTypeNames())

	assert.Equal(t, []string{
		"user", "admin", "worker", "payment_provider", "system",
	}, ActorTypeNames())

	// Every registry entry must be usable as a filter without an error.
	svc := newTestService(t)
	ctx := context.Background()

	for _, event := range events {
		_, _, err := svc.ListAuditLogPage(ctx, AuditLogFilter{Event: event})
		require.NoError(t, err, "event %q must be filterable", event)
	}
}
