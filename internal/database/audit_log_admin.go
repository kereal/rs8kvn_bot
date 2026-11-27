package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ErrAuditLogNotFound is returned by GetAuditLogByID when the requested audit
// entry does not exist.
var ErrAuditLogNotFound = errors.New("audit log entry not found")

// Audit log admin reads: the screen filters and pages over the append-only
// audit_log table. No write path lives here — the panel is read-only.

const (
	// auditLogDefaultLimit is the page size the admin screen opens with.
	auditLogDefaultLimit = 50
	// auditLogMaxLimit caps a single page; larger requests are clamped, not
	// rejected (P7: an oversized page is a wish for fewer round trips).
	auditLogMaxLimit = 200
)

// AuditLogFilter — фильтры экрана «Журнал аудита». Zero value = без фильтра.
type AuditLogFilter struct {
	EntityType EntityType // "" = все
	EntityID   string
	Event      string
	ActorType  ActorType
	From       *time.Time // окно created_at, границы включительно
	To         *time.Time
	Sort       string // created_at | id
	Desc       bool   // по умолчанию true
	Limit      int
	Offset     int
}

// buildAuditLogQuery is the single WHERE builder behind both the page items and
// their total: ListAuditLogPage calls it twice so the counter can never drift
// from the page (spec 07 §5).
func buildAuditLogQuery(s *Service, ctx context.Context, f AuditLogFilter) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&AuditLog{})

	if f.EntityType != "" {
		q = q.Where("entity_type = ?", f.EntityType)
	}

	if f.EntityID != "" {
		q = q.Where("entity_id = ?", f.EntityID)
	}

	if f.Event != "" {
		q = q.Where("event = ?", f.Event)
	}

	if f.ActorType != "" {
		q = q.Where("actor_type = ?", f.ActorType)
	}

	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}

	if f.To != nil {
		q = q.Where("created_at <= ?", *f.To)
	}

	return q
}

// auditLogOrder returns the ORDER BY clause for the whitelisted sort keys. The
// id tiebreaker keeps pages stable when several events share a timestamp.
//
// Ordering defaults to newest-first: Desc is only honoured when the caller
// pinned Sort. The HTTP layer always pins it, while a zero-value filter (and
// GetRecentAuditLog) still must lead with the newest events (spec 07 §7).
func auditLogOrder(f AuditLogFilter) string {
	column := "created_at"
	if f.Sort == "id" {
		column = "id"
	}

	desc := f.Desc || f.Sort == ""

	direction := " ASC"
	if desc {
		direction = " DESC"
	}

	if column == "id" {
		return "id" + direction
	}

	return "created_at" + direction + ", id" + direction
}

// ListAuditLogPage returns one page of audit events plus the total number of
// rows matching the same filters.
func (s *Service) ListAuditLogPage(ctx context.Context, f AuditLogFilter) ([]AuditLog, int64, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = auditLogDefaultLimit
	}

	if limit > auditLogMaxLimit {
		limit = auditLogMaxLimit
	}

	offset := max(f.Offset, 0)

	var total int64

	err := buildAuditLogQuery(s, ctx, f).Count(&total).Error
	if err != nil {
		return nil, 0, fmt.Errorf("count audit log: %w", err)
	}

	var items []AuditLog

	err = buildAuditLogQuery(s, ctx, f).
		Order(auditLogOrder(f)).
		Limit(limit).
		Offset(offset).
		Find(&items).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list audit log: %w", err)
	}

	return items, total, nil
}

// GetAuditLogByID returns a single audit entry with its full state snapshots.
func (s *Service) GetAuditLogByID(ctx context.Context, id uint) (*AuditLog, error) {
	var entry AuditLog

	err := s.db.WithContext(ctx).First(&entry, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAuditLogNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("get audit log %d: %w", id, err)
	}

	return &entry, nil
}

// AuditEventNames returns the event registry in declaration order. The admin
// screen builds its event filter from this list, so a new AuditEvent* constant
// becomes selectable without touching the handler or the UI.
func AuditEventNames() []string {
	return []string{
		AuditEventSubscriptionCreated,
		AuditEventTrialCreated,
		AuditEventTrialBound,
		AuditEventSubscriptionReanimated,
		AuditEventSubscriptionRenewed,
		AuditEventPlanChanged,
		AuditEventSubscriptionExpired,
		AuditEventDowngradedToFree,
		AuditEventSubscriptionRevoked,
		AuditEventSubscriptionDeleted,
		AuditEventTrialDeleted,
		AuditEventReminderSent,
		AuditEventTrafficNotified,
		AuditEventTrafficReset,
		AuditEventOrderCreated,
		AuditEventPaymentConfirmed,
		AuditEventPaymentRefunded,
		AuditEventPaymentAutoRefunded,
		AuditEventOrderExpired,
		AuditEventOrderCanceled,
		AuditEventChargebackReceived,
		AuditEventPaymentCreationUncert,
		AuditEventBroadcastStarted,
		AuditEventBroadcastCompleted,
		AuditEventBroadcastFailed,
		AuditEventBroadcastCanceled,
		AuditEventBroadcastCreated,
		AuditEventBroadcastUpdated,
		AuditEventBroadcastRetried,
		AuditEventBroadcastDeleted,
		AuditEventNodeCreated,
		AuditEventNodeUpdated,
		AuditEventNodeDeleted,
		AuditEventPlanNodesChanged,
		AuditEventPlanCreated,
		AuditEventPlanUpdated,
		AuditEventPlanDeleted,
		AuditEventSyncRunDegraded,
		AuditEventOrphanedClientsRevoked,
		AuditEventProductCreated,
		AuditEventProductUpdated,
		AuditEventProductDeleted,
		AuditEventInviteCreated,
		AuditEventInviteDeleted,
	}
}

// EntityTypeNames returns the entity registry used by the screen's filters.
func EntityTypeNames() []string {
	return []string{
		string(EntitySubscription),
		string(EntityOrder),
		string(EntityBroadcast),
		string(EntityPlan),
		string(EntityNode),
		string(EntityProduct),
		string(EntityInvite),
		string(EntitySystem),
	}
}

// ActorTypeNames returns the actor registry used by the screen's filters.
func ActorTypeNames() []string {
	return []string{
		string(ActorUser),
		string(ActorAdmin),
		string(ActorWorker),
		string(ActorPaymentProvider),
		string(ActorSystem),
	}
}
