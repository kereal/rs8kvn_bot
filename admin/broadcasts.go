package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
)

// BroadcastStore is the narrow persistence contract the broadcasts screen
// needs. The concrete *database.Service satisfies it. InsertAuditLog is here
// because every panel mutation on this screen writes its own audit event
// (doc/specs/admin/06-broadcasts.md §6).
type BroadcastStore interface {
	ListBroadcastOverviews(ctx context.Context, status string, limit, offset int) ([]database.BroadcastOverview, int64, error)
	CountBroadcastAudience(ctx context.Context, f database.BroadcastFilter) (int64, error)
	GetBroadcast(ctx context.Context, id uint) (*database.Broadcast, error)
	CreateBroadcast(ctx context.Context, b *database.Broadcast) error
	UpdateBroadcastDraft(ctx context.Context, b *database.Broadcast) error
	CancelBroadcast(ctx context.Context, id uint, now time.Time) (bool, error)
	DeleteBroadcast(ctx context.Context, id uint) error
	ResetBroadcastRetry(ctx context.Context, id uint, now time.Time) error
	InsertAuditLog(ctx context.Context, e *database.AuditLog) error
}

// BroadcastDeactivator выключает подписки пользователей, заблокировавших
// бота, по итогам рассылки. Реализуется *service.SubscriptionService.
type BroadcastDeactivator interface {
	DeactivateBlockedBroadcastRecipients(ctx context.Context, broadcastID uint) (*service.DeactivationResult, error)
}

// Screen constants: pagination and content limits per spec (§3, §4).
const (
	broadcastsDefaultLimit = 50
	broadcastsMaxLimit     = 200
	broadcastErrorsPreview = 20
	broadcastPlannedSlack  = 5 * time.Minute
)

// broadcastStatuses is the closed set of broadcasts.status values (CHECK
// constraint); the list endpoint filters by it.
var broadcastStatuses = map[string]bool{
	string(database.BroadcastStatusScheduled): true,
	string(database.BroadcastStatusRunning):   true,
	string(database.BroadcastStatusCompleted): true,
	string(database.BroadcastStatusFailed):    true,
	string(database.BroadcastStatusCanceled):  true,
}

// broadcastItem is the list DTO (P24: the DB model is never serialized
// directly; the full delivery_report stays on the server).
type broadcastItem struct {
	ID               uint       `json:"id"`
	Name             string     `json:"name"`
	Status           string     `json:"status"`
	PlannedAt        *time.Time `json:"planned_at"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
	RecipientsTotal  int64      `json:"recipients_total"`
	SentCount        int64      `json:"sent_count"`
	BlockedCount     int64      `json:"blocked_count"`
	UnreachableCount int64      `json:"unreachable_count"`
	FailedCount      int64      `json:"failed_count"`
	LastError        string     `json:"last_error"`
	RetryAt          *time.Time `json:"retry_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

// broadcastDetail adds fields that only make sense on the detail page:
// parsed filters, full text (needed for editing drafts), human-readable
// filter description and the first errors_preview delivery errors.
type broadcastDetail struct {
	broadcastItem

	Filters       json.RawMessage               `json:"filters"`
	MessageText   string                        `json:"message_text"`
	FiltersText   string                        `json:"filters_text"`
	RetryCount    int                           `json:"retry_count"`
	SnapshotTaken bool                          `json:"snapshot_taken"`
	ErrorsPreview []database.BroadcastSendError `json:"errors_preview"`
}

type broadcastPayload struct {
	Name        string          `json:"name"`
	MessageText string          `json:"message_text"`
	Filters     json.RawMessage `json:"filters"`
	PlannedAt   *time.Time      `json:"planned_at"`
}

// broadcastPreview is the dry-run answer for the form's preview step: the
// exact audience size, the filter in human-readable form, the schedule and
// the message text parsed the way Telegram will render it. Nothing is
// created and nothing is sent — the panel only puts rows into the queue.
type broadcastPreview struct {
	AudienceCount int64               `json:"audience_count"`
	FiltersText   string              `json:"filters_text"`
	PlannedAt     *time.Time          `json:"planned_at"`
	MessageRuns   []utils.MarkdownRun `json:"message_runs"`
}

func (s *Server) handleListBroadcasts(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parseBroadcastPage(w, r)
	if !ok {
		return
	}

	status := r.URL.Query().Get("status")
	if status != "" && !broadcastStatuses[status] {
		writeError(w, http.StatusBadRequest, "bad_request", "unknown status")

		return
	}

	rows, total, err := s.broadcasts.ListBroadcastOverviews(r.Context(), status, limit, offset)
	if err != nil {
		s.writeBroadcastStoreError(w, "list broadcasts", err)

		return
	}

	items := make([]broadcastItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, toBroadcastItem(row.Broadcast))
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleGetBroadcast(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	row, err := s.broadcasts.GetBroadcast(r.Context(), id)
	if err != nil {
		s.writeBroadcastStoreError(w, "get broadcast", err)

		return
	}

	writeJSON(w, http.StatusOK, toBroadcastDetail(row))
}

// handleBroadcastAudience returns the live audience count for filter query
// params — the preview the form debounces on (§7).
func (s *Server) handleBroadcastAudience(w http.ResponseWriter, r *http.Request) {
	f, ok := parseBroadcastFilterQuery(w, r)
	if !ok {
		return
	}

	count, err := s.broadcasts.CountBroadcastAudience(r.Context(), f)
	if err != nil {
		s.writeBroadcastStoreError(w, "count broadcast audience", err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

// handlePreviewBroadcast is the dry-run of the "Новая рассылка" form: it
// counts the audience with the same query the real send uses and returns the
// message text parsed the way Telegram will render it. Nothing is created and
// nothing is sent — the admin confirms the preview in a second step.
func (s *Server) handlePreviewBroadcast(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeBroadcastPayload(w, r)
	if !ok {
		return
	}

	b, filter, ok := buildBroadcast(w, payload)
	if !ok {
		return
	}

	// The same guards as the create path: a preview of an invalid draft would
	// only show numbers that cannot be sent.
	err := database.ValidateBroadcastDraft(b)
	if err != nil {
		s.writeBroadcastStoreError(w, "validate broadcast", err)

		return
	}

	count, err := s.broadcasts.CountBroadcastAudience(r.Context(), filter)
	if err != nil {
		s.writeBroadcastStoreError(w, "count broadcast audience", err)

		return
	}

	writeJSON(w, http.StatusOK, broadcastPreview{
		AudienceCount: count,
		FiltersText:   filter.String(),
		PlannedAt:     b.PlannedAt,
		MessageRuns:   utils.ParseMarkdownV2(b.MessageText),
	})
}

func (s *Server) handleCreateBroadcast(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeBroadcastPayload(w, r)
	if !ok {
		return
	}

	b, _, ok := buildBroadcast(w, payload)
	if !ok {
		return
	}

	// Same field validation as the draft update path: name/text limits and
	// the filter whitelist live in one place (database.ValidateBroadcastDraft).
	err := database.ValidateBroadcastDraft(b)
	if err != nil {
		s.writeBroadcastStoreError(w, "validate broadcast", err)

		return
	}

	// Audience guard runs before the insert: an empty filter result is a
	// client mistake (§3), not a server error.
	filter, err := database.ParseBroadcastFilter(b.Filters)
	if err != nil {
		s.writeBroadcastStoreError(w, "parse broadcast filter", err)

		return
	}

	count, err := s.broadcasts.CountBroadcastAudience(r.Context(), filter)
	if err != nil {
		s.writeBroadcastStoreError(w, "count broadcast audience", err)

		return
	}
	if count == 0 {
		writeError(w, http.StatusConflict, "in_use", "audience is empty", map[string]any{"audience": 0})

		return
	}

	err = s.broadcasts.CreateBroadcast(r.Context(), b)
	if err != nil {
		s.writeBroadcastStoreError(w, "create broadcast", err)

		return
	}

	s.logBroadcastAudit(r.Context(), database.AuditBroadcastCreated(b))

	writeJSON(w, http.StatusCreated, toBroadcastDetail(b))
}

func (s *Server) handleUpdateBroadcast(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	current, err := s.broadcasts.GetBroadcast(r.Context(), id)
	if err != nil {
		s.writeBroadcastStoreError(w, "get broadcast", err)

		return
	}

	if current.Status != string(database.BroadcastStatusScheduled) {
		writeError(w, http.StatusConflict, "in_use", "only a scheduled draft can be edited", map[string]any{"status": current.Status})

		return
	}

	payload, ok := decodeBroadcastPayload(w, r)
	if !ok {
		return
	}

	b, _, ok := buildBroadcast(w, payload)
	if !ok {
		return
	}
	b.ID = id

	err = s.broadcasts.UpdateBroadcastDraft(r.Context(), b)
	if err != nil {
		s.writeBroadcastStoreError(w, "update broadcast draft", err)

		return
	}

	s.logBroadcastAudit(r.Context(), database.AuditBroadcastUpdated(id, current, b))

	updated, err := s.broadcasts.GetBroadcast(r.Context(), id)
	if err != nil {
		// The draft was updated; a refetch failure must not read as failure.
		logger.Warn("admin: refetch updated broadcast failed", zap.Uint("broadcast_id", id), zap.Error(err))
		writeJSON(w, http.StatusOK, toBroadcastDetail(b))

		return
	}

	writeJSON(w, http.StatusOK, toBroadcastDetail(updated))
}

func (s *Server) handleCancelBroadcast(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	b, ok := s.broadcastForAction(w, r, id, "stale", database.BroadcastStatusScheduled, database.BroadcastStatusRunning)
	if !ok {
		return
	}

	_, err := s.broadcasts.CancelBroadcast(r.Context(), id, time.Now().UTC())
	if err != nil {
		s.writeBroadcastStoreError(w, "cancel broadcast", err)

		return
	}

	s.logBroadcastAudit(r.Context(), database.AuditBroadcastCanceled(b, 0))

	s.writeBroadcastAfter(r.Context(), w, id)
}

func (s *Server) handleRetryBroadcast(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if _, ok := s.broadcastForAction(w, r, id, "stale", database.BroadcastStatusFailed); !ok {
		return
	}

	err := s.broadcasts.ResetBroadcastRetry(r.Context(), id, time.Now().UTC())
	if err != nil {
		s.writeBroadcastStoreError(w, "retry broadcast", err)

		return
	}

	updated, err := s.broadcasts.GetBroadcast(r.Context(), id)
	if err == nil {
		s.logBroadcastAudit(r.Context(), database.AuditBroadcastRetried(id, updated.RetryCount))
	} else {
		logger.Warn("admin: refetch retried broadcast failed", zap.Uint("broadcast_id", id), zap.Error(err))
		s.logBroadcastAudit(r.Context(), database.AuditBroadcastRetried(id, 0))
	}

	s.writeBroadcastAfter(r.Context(), w, id)
}

func (s *Server) handleDeleteBroadcast(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	b, ok := s.broadcastForAction(w, r, id, "in_use", broadcastDeletableStatuses...)
	if !ok {
		return
	}

	err := s.broadcasts.DeleteBroadcast(r.Context(), id)
	if err != nil {
		s.writeBroadcastStoreError(w, "delete broadcast", err)

		return
	}

	s.logBroadcastAudit(r.Context(), database.AuditBroadcastDeleted(b))

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// handleDeactivateBlocked revokes subscriptions of users who blocked the bot
// during a finished broadcast. The panel only reports the outcome; the
// service performs the revocation and deprovisioning exactly like the
// removed bot flow did (§3).
func (s *Server) handleDeactivateBlocked(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	b, ok := s.broadcastForAction(w, r, id, "stale", database.BroadcastStatusCompleted, database.BroadcastStatusFailed)
	if !ok {
		return
	}

	if b.BlockedCount == 0 && b.UnreachableCount == 0 {
		writeError(w, http.StatusConflict, "in_use", "no blocked or unreachable recipients", map[string]any{"blocked": 0})

		return
	}

	if s.deactivator == nil {
		logger.Error("admin: broadcast deactivator is not wired")
		writeError(w, http.StatusInternalServerError, "internal", "internal error")

		return
	}

	result, err := s.deactivator.DeactivateBlockedBroadcastRecipients(service.WithAuditAdmin(r.Context(), 0), id)
	if err != nil {
		if result == nil {
			logger.Error("admin: deactivate blocked recipients failed", zap.Uint("broadcast_id", id), zap.Error(err))
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}
		// Partial failure: counts are still meaningful; the error travels to
		// the SPA so it can warn instead of celebrate (§3).
		logger.Error("admin: deactivate blocked recipients partially failed", zap.Uint("broadcast_id", id), zap.Error(err))
		writeJSON(w, http.StatusOK, map[string]any{
			"targets": result.Targets,
			"revoked": result.Revoked,
			"failed":  result.Failed,
			"error":   err.Error(),
		})

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"targets": result.Targets,
		"revoked": result.Revoked,
		"failed":  result.Failed,
	})
}

// broadcastDeletableStatuses lists the statuses delete is allowed for: any
// except running (§3 — a running campaign belongs to the worker).
var broadcastDeletableStatuses = []database.BroadcastStatus{
	database.BroadcastStatusScheduled,
	database.BroadcastStatusCompleted,
	database.BroadcastStatusFailed,
	database.BroadcastStatusCanceled,
}

// broadcastForAction loads the broadcast and checks its status against the
// whitelist, mapping violations to the given 409 code (§3: update/delete are
// in_use, cancel/retry/deactivate are stale). Missing rows are 404.
func (s *Server) broadcastForAction(w http.ResponseWriter, r *http.Request, id uint, code string, allowed ...database.BroadcastStatus) (*database.Broadcast, bool) {
	b, err := s.broadcasts.GetBroadcast(r.Context(), id)
	if err != nil {
		s.writeBroadcastStoreError(w, "get broadcast", err)

		return nil, false
	}

	for _, status := range allowed {
		if b.Status == string(status) {
			return b, true
		}
	}

	writeError(w, http.StatusConflict, code, "broadcast status does not allow the operation", map[string]any{"status": b.Status})

	return nil, false
}

// writeBroadcastAfter re-fetches the row so mutations return the fresh state.
func (s *Server) writeBroadcastAfter(ctx context.Context, w http.ResponseWriter, id uint) {
	b, err := s.broadcasts.GetBroadcast(ctx, id)
	if err != nil {
		logger.Warn("admin: refetch broadcast after mutation failed", zap.Uint("broadcast_id", id), zap.Error(err))
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "ok": true})

		return
	}

	writeJSON(w, http.StatusOK, toBroadcastDetail(b))
}

func toBroadcastItem(b database.Broadcast) broadcastItem {
	return broadcastItem{
		ID:               b.ID,
		Name:             b.Name,
		Status:           b.Status,
		PlannedAt:        b.PlannedAt,
		StartedAt:        b.StartedAt,
		FinishedAt:       b.FinishedAt,
		RecipientsTotal:  b.RecipientsTotal,
		SentCount:        b.SentCount,
		BlockedCount:     b.BlockedCount,
		UnreachableCount: b.UnreachableCount,
		FailedCount:      b.FailedCount,
		LastError:        b.LastError,
		RetryAt:          b.RetryAt,
		CreatedAt:        b.CreatedAt,
	}
}

func toBroadcastDetail(b *database.Broadcast) broadcastDetail {
	filters := json.RawMessage(b.Filters)
	if len(filters) == 0 || !json.Valid(filters) {
		filters = json.RawMessage("{}")
	}

	filter, _ := database.ParseBroadcastFilter(b.Filters)

	preview := make([]database.BroadcastSendError, 0, broadcastErrorsPreview)
	report, reportErr := b.ParseDeliveryReport()
	if reportErr == nil {
		preview = report.Errors
		if len(preview) > broadcastErrorsPreview {
			preview = preview[:broadcastErrorsPreview]
		}
	}

	return broadcastDetail{
		broadcastItem: toBroadcastItem(*b),
		Filters:       filters,
		MessageText:   b.MessageText,
		FiltersText:   filter.String(),
		RetryCount:    b.RetryCount,
		SnapshotTaken: database.BroadcastSnapshotTaken(b),
		ErrorsPreview: preview,
	}
}

// buildBroadcast converts the payload into a model: canonical filter JSON is
// stored so the row always round-trips through ParseBroadcastFilter.
func buildBroadcast(w http.ResponseWriter, payload *broadcastPayload) (*database.Broadcast, database.BroadcastFilter, bool) {
	rawFilter := strings.TrimSpace(string(payload.Filters))
	if rawFilter == "" {
		rawFilter = "{}"
	}

	filter, err := database.ParseBroadcastFilter(rawFilter)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation", "unknown filter value", map[string]any{"filters": "неизвестное значение фильтра"})

		return nil, database.BroadcastFilter{}, false
	}

	switch filter.PlanType {
	case "", "paid", "free":
	default:
		writeError(w, http.StatusBadRequest, "validation", "unknown filter value", map[string]any{"filters": "plan_type: допускаются paid и free"})

		return nil, database.BroadcastFilter{}, false
	}

	filterJSON, err := json.Marshal(filter)
	if err != nil {
		logger.Error("admin: marshal broadcast filter failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "internal", "internal error")

		return nil, database.BroadcastFilter{}, false
	}

	// planned_at in the past (beyond the 5-minute slack) is a client mistake:
	// the worker would either miss it or start immediately, neither of which
	// is what the admin asked for.
	if payload.PlannedAt != nil && payload.PlannedAt.Before(time.Now().Add(-broadcastPlannedSlack)) {
		writeError(w, http.StatusBadRequest, "validation", "planned_at is in the past", map[string]any{"planned_at": "дата уже прошла"})

		return nil, database.BroadcastFilter{}, false
	}

	return &database.Broadcast{
		Name:        strings.TrimSpace(payload.Name),
		Filters:     string(filterJSON),
		MessageText: payload.MessageText,
		PlannedAt:   payload.PlannedAt,
	}, filter, true
}

func decodeBroadcastPayload(w http.ResponseWriter, r *http.Request) (*broadcastPayload, bool) {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()

	var payload broadcastPayload

	err := dec.Decode(&payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")

		return nil, false
	}

	// Reject trailing data so a malformed client cannot think it succeeded.
	_, err = dec.Token()
	if err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")

		return nil, false
	}

	return &payload, true
}

// parseBroadcastPage converts limit/offset with the spec's clamp rule:
// malformed values are 400, limit above the cap is clamped (P7/P8).
func parseBroadcastPage(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit := broadcastsDefaultLimit

	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be a positive integer")

			return 0, 0, false
		}
		if parsed > broadcastsMaxLimit {
			parsed = broadcastsMaxLimit
		}
		limit = parsed
	}

	offset := 0

	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "offset must be a non-negative integer")

			return 0, 0, false
		}
		offset = parsed
	}

	return limit, offset, true
}

// parseBroadcastFilterQuery reads the audience-preview query params into a
// BroadcastFilter. Unknown values are 400, never silently ignored (§3).
func parseBroadcastFilterQuery(w http.ResponseWriter, r *http.Request) (database.BroadcastFilter, bool) {
	q := r.URL.Query()

	f := database.BroadcastFilter{
		PlanType:           q.Get("plan_type"),
		SubscriptionStatus: q.Get("subscription_status"),
	}

	switch f.PlanType {
	case "", "paid", "free":
	default:
		writeError(w, http.StatusBadRequest, "validation", "unknown filter value", map[string]any{"filters": "plan_type: допускаются paid и free"})

		return f, false
	}

	switch f.SubscriptionStatus {
	case "", "active", "revoked", "all":
	default:
		writeError(w, http.StatusBadRequest, "validation", "unknown filter value", map[string]any{"filters": "subscription_status: допускаются active, revoked, all"})

		return f, false
	}

	if raw := q.Get("inactive_days"); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 0 {
			writeError(w, http.StatusBadRequest, "validation", "unknown filter value", map[string]any{"filters": "inactive_days: целое число ≥ 0"})

			return f, false
		}
		f.InactiveDays = &days
	}

	if raw := q.Get("ever_paid"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation", "unknown filter value", map[string]any{"filters": "ever_paid: true или false"})

			return f, false
		}
		f.EverPaid = &value
	}

	if raw := q.Get("registered_after"); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation", "unknown filter value", map[string]any{"filters": "registered_after: ожидается RFC3339"})

			return f, false
		}
		f.RegisteredAfter = &value
	}

	if raw := q.Get("registered_before"); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation", "unknown filter value", map[string]any{"filters": "registered_before: ожидается RFC3339"})

			return f, false
		}
		f.RegisteredBefore = &value
	}

	return f, true
}

// logBroadcastAudit writes a panel broadcast event best-effort: an audit
// failure must not fail the business operation.
func (s *Server) logBroadcastAudit(ctx context.Context, e *database.AuditLog) {
	if e == nil {
		return
	}

	err := s.broadcasts.InsertAuditLog(ctx, e)
	if err != nil {
		logger.Warn("audit log write failed", zap.String("event", e.Event), zap.Error(err))
	}
}

// writeBroadcastStoreError maps broadcast sentinels onto HTTP codes with the
// details object the SPA renders (§3), and logs unexpected failures.
func (s *Server) writeBroadcastStoreError(w http.ResponseWriter, op string, err error) {
	var fieldErr *database.BroadcastFieldError

	switch {
	case errors.As(err, &fieldErr):
		writeError(w, http.StatusBadRequest, "validation", fieldErr.Reason, map[string]any{fieldErr.Field: fieldErr.Reason})
	case errors.Is(err, database.ErrBroadcastInvalid):
		writeError(w, http.StatusBadRequest, "validation", err.Error())
	case errors.Is(err, database.ErrBroadcastNotFound):
		writeError(w, http.StatusNotFound, "not_found", "broadcast not found")
	case errors.Is(err, database.ErrBroadcastLocked):
		writeError(w, http.StatusConflict, "in_use", err.Error())
	case errors.Is(err, database.ErrBroadcastAudienceEmpty):
		writeError(w, http.StatusConflict, "in_use", "audience is empty", map[string]any{"audience": 0})
	default:
		logger.Error("admin: "+op+" failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}
