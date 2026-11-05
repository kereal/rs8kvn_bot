package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// AuditStore is the narrow persistence contract the audit screen needs. The
// concrete *database.Service satisfies it.
type AuditStore interface {
	ListAuditLogPage(ctx context.Context, f database.AuditLogFilter) ([]database.AuditLog, int64, error)
	GetAuditLogByID(ctx context.Context, id uint) (*database.AuditLog, error)
}

const (
	auditLimitDefault = 50
	auditLimitMax     = 200
	dateLayout        = "2006-01-02"
)

// auditItem is one list row. old_state/new_state are deliberately absent: the
// list must stay small, and the snapshots are only shown after a row expands.
type auditItem struct {
	ID         uint            `json:"id"`
	EntityType string          `json:"entity_type"`
	EntityID   *string         `json:"entity_id"`
	Event      string          `json:"event"`
	ActorType  string          `json:"actor_type"`
	ActorID    *string         `json:"actor_id"`
	CreatedAt  time.Time       `json:"created_at"`
	Details    json.RawMessage `json:"details"`
}

// auditDetail adds the before/after snapshots for the expanded row.
type auditDetail struct {
	auditItem
	OldState json.RawMessage `json:"old_state"`
	NewState json.RawMessage `json:"new_state"`
}

func (s *Server) handleListAuditLog(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseAuditFilter(w, r)
	if !ok {
		return
	}

	rows, total, err := s.audit.ListAuditLogPage(r.Context(), filter)
	if err != nil {
		s.writeStoreError(w, "list audit log", err)
		return
	}

	items := make([]auditItem, 0, len(rows))
	for i := range rows {
		items = append(items, newAuditItem(&rows[i]))
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleAuditEvents(w http.ResponseWriter, _ *http.Request) {
	// The registry lives in Go constants, so a new event becomes filterable
	// without touching this handler or the UI.
	writeJSON(w, http.StatusOK, map[string]any{
		"events":       database.AuditEventNames(),
		"entity_types": database.EntityTypeNames(),
		"actor_types":  database.ActorTypeNames(),
	})
}

func (s *Server) handleAuditDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	entry, err := s.audit.GetAuditLogByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get audit log", err)
		return
	}

	writeJSON(w, http.StatusOK, auditDetail{
		auditItem: newAuditItem(entry),
		OldState:  auditStateJSON(entry.OldState),
		NewState:  auditStateJSON(entry.NewState),
	})
}

func newAuditItem(entry *database.AuditLog) auditItem {
	return auditItem{
		ID:         entry.ID,
		EntityType: string(entry.EntityType),
		EntityID:   entry.EntityID,
		Event:      entry.Event,
		ActorType:  string(entry.ActorType),
		ActorID:    entry.ActorID,
		CreatedAt:  entry.CreatedAt,
		Details:    auditDetailsJSON(entry.Details),
	}
}

// auditDetailsJSON parses the stored details once for every client. Rows
// written before details were validated (legacy junk) are wrapped as a JSON
// string so the SPA never receives an unparsable body field.
func auditDetailsJSON(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage("{}")
	}

	return auditJSON(trimmed)
}

// auditStateJSON renders old_state/new_state as JSON. NULL stays null: it means
// "no fields changed" / "entity created or deleted" (migration 039).
func auditStateJSON(raw *string) json.RawMessage {
	if raw == nil {
		return nil
	}

	return auditJSON(*raw)
}

// auditJSON passes valid JSON through verbatim and wraps anything else as a
// JSON string.
func auditJSON(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}

	wrapped, err := json.Marshal(trimmed)
	if err != nil {
		return json.RawMessage("null")
	}

	return wrapped
}

// parseAuditFilter validates the list query against the registries. Unknown
// values are rejected instead of silently ignored: a typo in a filter must not
// look like "no events matched".
func parseAuditFilter(w http.ResponseWriter, r *http.Request) (database.AuditLogFilter, bool) {
	query := r.URL.Query()

	filter := database.AuditLogFilter{
		Sort:  "created_at",
		Desc:  true,
		Limit: auditLimitDefault,
	}

	if raw := query.Get("entity_type"); raw != "" {
		if !contains(database.EntityTypeNames(), raw) {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown entity_type")

			return filter, false
		}

		filter.EntityType = database.EntityType(raw)
	}

	if raw := query.Get("actor_type"); raw != "" {
		if !contains(database.ActorTypeNames(), raw) {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown actor_type")

			return filter, false
		}

		filter.ActorType = database.ActorType(raw)
	}

	if raw := query.Get("event"); raw != "" {
		if !contains(database.AuditEventNames(), raw) {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown event")

			return filter, false
		}

		filter.Event = raw
	}

	filter.EntityID = query.Get("entity_id")

	if raw := query.Get("sort"); raw != "" {
		if raw != "created_at" && raw != "id" {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown sort")

			return filter, false
		}

		filter.Sort = raw
	}

	if raw := query.Get("order"); raw != "" {
		switch raw {
		case "desc":
			filter.Desc = true
		case "asc":
			filter.Desc = false
		default:
			writeError(w, http.StatusBadRequest, "bad_request", "order must be asc or desc")

			return filter, false
		}
	}

	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid limit")

			return filter, false
		}

		// Oversized pages are clamped, not rejected (P7).
		filter.Limit = min(n, auditLimitMax)
	}

	if raw := query.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid offset")

			return filter, false
		}

		filter.Offset = n
	}

	from, ok := parseAuditDate(w, query.Get("from"), false)
	if !ok {
		return filter, false
	}

	filter.From = from

	to, ok := parseAuditDate(w, query.Get("to"), true)
	if !ok {
		return filter, false
	}

	filter.To = to

	return filter, true
}

// parseAuditDate parses YYYY-MM-DD into a UTC day boundary: the first instant
// for "from", the last for "to" — both ends inclusive.
func parseAuditDate(w http.ResponseWriter, raw string, endOfDay bool) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}

	day, err := time.Parse(dateLayout, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "dates must be YYYY-MM-DD")

		return nil, false
	}

	day = day.UTC()
	if endOfDay {
		day = day.Add(24*time.Hour - time.Nanosecond)
	}

	return &day, true
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}

	return false
}
