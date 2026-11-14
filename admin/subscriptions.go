package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"go.uber.org/zap"
)

// SubscriptionReadStore is the narrow persistence contract the subscriptions
// screen needs for reads. The concrete *database.Service satisfies it; the
// panel never writes into subscriptions/subscription_nodes itself (ADR-0002
// §6): every mutation goes through the service layer.
type SubscriptionReadStore interface {
	ListSubscriptionOverviews(ctx context.Context, f database.SubscriptionFilter) ([]database.SubscriptionOverview, int64, error)
	GetSubscriptionDetail(ctx context.Context, id uint) (*database.SubscriptionOverview, []database.SubscriptionNodeView, error)
	// GetByID loads the raw subscription row for detail-only fields
	// (devices, ips, payment state). It is a read: writes stay in the service.
	GetByID(ctx context.Context, id uint) (*database.Subscription, error)
	// GetProductByID resolves duration_days for the detail card.
	GetProductByID(ctx context.Context, id uint) (*database.Product, error)
	// InsertAuditLog writes the traffic_reset event after a successful reset.
	InsertAuditLog(ctx context.Context, e *database.AuditLog) error
}

// SubscriptionActions is the narrow service contract for plan changes and the
// two destructive operations. Wired via SetSubscriptionServices (see
// cmd/bot/main.go); the concrete *service.SubscriptionService satisfies it.
type SubscriptionActions interface {
	AdminSetPlan(ctx context.Context, subscriptionID, planID uint, days int) (*database.Subscription, error)
	RevokeSubscription(ctx context.Context, id uint) (*database.Subscription, error)
	DeleteByID(ctx context.Context, id uint) (*database.Subscription, error)
}

// SubscriptionSyncActions is the narrow service contract for the two
// node-facing operations: the traffic counter reset and a forced re-sync of one
// subscription. Both live on *service.SyncService, so the panel takes one
// dependency instead of two views of the same object.
type SubscriptionSyncActions interface {
	ResetSubscriptionTraffic(ctx context.Context, subscriptionID uint) (service.TrafficResetResult, error)
	// SyncSubscription re-runs the reconciler for one subscription: pending_add,
	// pending_update and pending_remove bindings are pushed to the nodes. It is
	// idempotent and safe to call for any status.
	SyncSubscription(ctx context.Context, subscriptionID uint) error
}

// SetSubscriptionServices wires the service layer used by the mutating
// subscription endpoints. Without it the screen stays read-only and mutations
// answer 503.
func (s *Server) SetSubscriptionServices(read SubscriptionReadStore, actions SubscriptionActions, sync SubscriptionSyncActions) {
	s.subs = read
	s.subActions = actions
	s.subSync = sync
}

// Screen constants: pagination boundaries and default expiry window (hours).
const (
	subscriptionsDefaultLimit = 50
	subscriptionsMaxLimit     = 200
)

var (
	// subscriptionStatuses is the closed set of subscriptions.status values
	// (CHECK constraint in the schema).
	subscriptionStatuses = map[string]bool{
		string(database.SubscriptionStatusActive):   true,
		string(database.SubscriptionStatusExpired):  true,
		string(database.SubscriptionStatusPaused):   true,
		string(database.SubscriptionStatusCanceled): true,
		string(database.SubscriptionStatusRevoked):  true,
	}

	// subscriptionSortColumns whitelists sortable columns; the values are
	// column names, so anything outside this set must be rejected, never
	// interpolated into SQL.
	subscriptionSortColumns = map[string]bool{
		"id":           true,
		"created_at":   true,
		"expires_at":   true,
		"last_request": true,
	}
)

// subscriptionItem is the list DTO (the DB model is never serialized directly).
type subscriptionItem struct {
	ID             uint       `json:"id"`
	TelegramID     int64      `json:"telegram_id"`
	Username       string     `json:"username"`
	SubscriptionID string     `json:"subscription_id"`
	Status         string     `json:"status"`
	PlanID         *uint      `json:"plan_id"`
	PlanName       string     `json:"plan_name"`
	ExpiresAt      *time.Time `json:"expires_at"`
	LastRequest    *time.Time `json:"last_request"`
	NodesActive    int64      `json:"nodes_active"`
	NodesTotal     int64      `json:"nodes_total"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// subscriptionNode is one row of the detail's node table.
type subscriptionNode struct {
	NodeID    uint    `json:"node_id"`
	NodeName  string  `json:"node_name"`
	Status    string  `json:"status"`
	LastError *string `json:"last_error"`
}

// subscriptionDetail is the detail DTO: the list fields plus heavy/per-row
// fields and the node bindings. devices/ips pass through as pre-validated
// JSON produced by the web server.
type subscriptionDetail struct {
	subscriptionItem

	Devices              json.RawMessage    `json:"devices"`
	Ips                  json.RawMessage    `json:"ips"`
	ProductID            *uint              `json:"product_id"`
	DurationDays         *int               `json:"duration_days"`
	PricePaidCents       int64              `json:"price_paid_cents"`
	Currency             string             `json:"currency"`
	StartedAt            *time.Time         `json:"started_at"`
	RemindersSent        int                `json:"reminders_sent"`
	TrafficRemindersSent int                `json:"traffic_reminders_sent"`
	ReferredBy           *int64             `json:"referred_by"`
	InviteCode           string             `json:"invite_code"`
	SubURL               string             `json:"sub_url"`
	Nodes                []subscriptionNode `json:"nodes"`
}

// SET_PLAN_CONSTRAINTS is documented in the payload comment below.
type setPlanPayload struct {
	PlanID *uint `json:"plan_id"`
	Days   *int  `json:"days"`
}

func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	f, ok := parseSubscriptionFilter(w, r)
	if !ok {
		return
	}

	rows, total, err := s.subs.ListSubscriptionOverviews(r.Context(), f)
	if err != nil {
		s.writeStoreError(w, "list subscriptions", err)
		return
	}

	items := make([]subscriptionItem, 0, len(rows))
	for i := range rows {
		items = append(items, toSubscriptionItem(&rows[i]))
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleGetSubscription(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	detail, err := s.buildSubscriptionDetail(r.Context(), id)
	if err != nil {
		s.writeSubStoreError(w, "get subscription", err)
		return
	}

	writeJSON(w, http.StatusOK, detail)
}

// buildSubscriptionDetail assembles the detail DTO from three reads: the
// overview row, the raw subscription, and the product that sets duration. The
// sync endpoint reuses it, so a forced re-sync answers in exactly the shape GET
// returns and the view can swap the row without a second request.
func (s *Server) buildSubscriptionDetail(ctx context.Context, id uint) (*subscriptionDetail, error) {
	overview, nodes, err := s.subs.GetSubscriptionDetail(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load subscription detail: %w", err)
	}

	row, err := s.subs.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load subscription row: %w", err)
	}

	detail := subscriptionDetail{
		subscriptionItem:     toSubscriptionItem(overview),
		ProductID:            row.ProductID,
		PricePaidCents:       row.PricePaidCents,
		StartedAt:            row.StartedAt,
		RemindersSent:        row.RemindersSent,
		TrafficRemindersSent: row.TrafficRemindersSent,
		ReferredBy:           row.ReferredBy,
		InviteCode:           derefInviteCode(row.InviteCode),
		SubURL:               "/sub/" + row.SubscriptionID,
		Nodes:                make([]subscriptionNode, 0, len(nodes)),
	}

	if detail.Devices = devicesJSON(row.Devices); detail.Devices == nil {
		detail.Devices = json.RawMessage("[]")
	}
	if detail.Ips = devicesJSON(row.Ips); detail.Ips == nil {
		detail.Ips = json.RawMessage("[]")
	}

	if row.Currency != nil {
		detail.Currency = *row.Currency
	}

	if row.ProductID != nil {
		product, prodErr := s.subs.GetProductByID(ctx, *row.ProductID)
		switch {
		case prodErr == nil:
			detail.DurationDays = &product.DurationDays
		case errors.Is(prodErr, database.ErrProductNotFound):
			// Product deleted while the subscription lives on: keep the id,
			// duration unknown.
		default:
			return nil, fmt.Errorf("load product: %w", prodErr)
		}
	}

	for i := range nodes {
		detail.Nodes = append(detail.Nodes, subscriptionNode{
			NodeID:    nodes[i].NodeID,
			NodeName:  nodes[i].NodeName,
			Status:    nodes[i].Status,
			LastError: nodes[i].LastError,
		})
	}

	return &detail, nil
}

// handleSetSubscriptionPlan renews the subscription or switches its plan
// through AdminSetPlan (which also audits plan_changed and syncs the panels).
func (s *Server) handleSetSubscriptionPlan(w http.ResponseWriter, r *http.Request) {
	if s.requireSubActions(w) {
		return
	}

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()

	var payload setPlanPayload
	err := dec.Decode(&payload)
	if err != nil || payload.PlanID == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "plan_id and days are required")
		return
	}

	days := 0
	if payload.Days != nil {
		days = *payload.Days
	}

	// Guard'ы ТЗ §3: дней не может быть отрицательным или больше 3650 —
	// проверяем в хендлере, чтобы ответить 400 validation с details.days
	// (сервис возвращает на эти случаи несентинельную ошибку).
	if days < 0 {
		writeError(w, http.StatusBadRequest, "validation", "days must be non-negative", map[string]any{"days": days})
		return
	}
	if days > service.AdminSetPlanMaxDays {
		writeError(w, http.StatusBadRequest, "validation",
			"days exceeds maximum", map[string]any{"days": days, "max": service.AdminSetPlanMaxDays})
		return
	}

	ctx := service.WithAuditAdmin(r.Context(), 0)
	sub, err := s.subActions.AdminSetPlan(ctx, id, *payload.PlanID, days)
	if err != nil {
		s.writeSubActionError(w, "set plan", err)
		return
	}

	writeJSON(w, http.StatusOK, subDTOFromModel(sub, s.subURLBase(sub)))
}

// handleRevokeSubscription revokes access without deleting the row.
func (s *Server) handleRevokeSubscription(w http.ResponseWriter, r *http.Request) {
	if s.requireSubActions(w) {
		return
	}

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	sub, err := s.subActions.RevokeSubscription(service.WithAuditAdmin(r.Context(), 0), id)
	if err != nil {
		s.writeSubActionError(w, "revoke subscription", err)
		return
	}

	writeJSON(w, http.StatusOK, subDTOFromModel(sub, s.subURLBase(sub)))
}

// handleResetSubscriptionTraffic zeroes the traffic counter on every active
// node. Reset is best-effort per node: a partial failure is a 200 with a
// warning, zero reachable nodes is 409 in_use, and the audit row always lands.
func (s *Server) handleResetSubscriptionTraffic(w http.ResponseWriter, r *http.Request) {
	if s.requireSubSync(w) {
		return
	}

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	// Guard'ы ТЗ §3: revoked/canceled не имеют клиентов на узлах, сброс
	// бессмыслен — 409 stale. Загрузка строки — чтение, мутаций нет.
	sub, err := s.subs.GetByID(r.Context(), id)
	if err != nil {
		s.writeSubStoreError(w, "load subscription", err)
		return
	}

	if sub.Status == string(database.SubscriptionStatusRevoked) ||
		sub.Status == string(database.SubscriptionStatusCanceled) {
		writeError(w, http.StatusConflict, "stale", "subscription is "+sub.Status,
			map[string]any{"status": sub.Status})
		return
	}

	reset, err := s.subSync.ResetSubscriptionTraffic(service.WithAuditAdmin(r.Context(), 0), id)
	if reset.Reset == 0 {
		// Ни один узел не ответил — счётчик не изменился нигде.
		writeError(w, http.StatusConflict, "in_use", "no active nodes answered the reset",
			map[string]any{"nodes": 0})
		return
	}

	// Аудит пишется всегда, даже при частичном сбое (ТЗ §6). Число упавших
	// узлов приходит типизированным результатом, а не парсингом текста ошибки.
	failed := reset.Failed

	auditErr := s.subs.InsertAuditLog(r.Context(), database.AuditTrafficReset(sub, reset.Reset, failed))
	if auditErr != nil {
		// Журнал не влияет на бизнес-операцию: сброс уже выполнен.
		logger.Warn("admin: traffic_reset audit failed", zap.Error(auditErr))
	}

	payload := map[string]any{"reset_nodes": reset.Reset}
	if err != nil {
		payload["failed_nodes"] = failed
		payload["warning"] = "счётчик сброшен не на всех узлах: часть панелей недоступна, повторите позже"
	}

	writeJSON(w, http.StatusOK, payload)
}

// handleDeleteSubscription hard-deletes the subscription row through
// DeleteByID (revoke + deprovision + delete, audited by the service).
func (s *Server) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	if s.requireSubActions(w) {
		return
	}

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	sub, err := s.subActions.DeleteByID(service.WithAuditAdmin(r.Context(), 0), id)
	if err != nil {
		s.writeSubActionError(w, "delete subscription", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": sub.ID, "deleted": true})
}

// subDTOFromModel renders a mutated subscription for the mutation responses.
// The DB model is never serialized directly: only the fields the SPA shows.
func subDTOFromModel(sub *database.Subscription, subURL string) subscriptionDetail {
	detail := subscriptionDetail{
		subscriptionItem: subscriptionItem{
			ID:             sub.ID,
			TelegramID:     sub.TelegramID,
			Username:       sub.Username,
			SubscriptionID: sub.SubscriptionID,
			Status:         sub.Status,
			PlanID:         nonZeroPlanID(sub.PlanID),
			ExpiresAt:      sub.ExpiresAt,
			LastRequest:    sub.LastRequest,
			CreatedAt:      sub.CreatedAt,
			UpdatedAt:      sub.UpdatedAt,
		},
		ProductID:            sub.ProductID,
		PricePaidCents:       sub.PricePaidCents,
		StartedAt:            sub.StartedAt,
		RemindersSent:        sub.RemindersSent,
		TrafficRemindersSent: sub.TrafficRemindersSent,
		ReferredBy:           sub.ReferredBy,
		InviteCode:           derefInviteCode(sub.InviteCode),
		SubURL:               subURL,
		Nodes:                []subscriptionNode{},
	}

	return detail
}

// subURLBase builds the panel-facing share path for a subscription.
func (s *Server) subURLBase(sub *database.Subscription) string {
	if sub.SubscriptionID == "" {
		return ""
	}
	return "/sub/" + sub.SubscriptionID
}

// writeSubStoreError maps subscription read sentinels before falling back to
// the shared mapping.
func (s *Server) writeSubStoreError(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, database.ErrSubscriptionNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	}

	s.writeStoreError(w, op, err)
}

// writeSubActionError maps service-layer sentinels onto the codes of ТЗ §3.
func (s *Server) writeSubActionError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, database.ErrSubscriptionNotFound):
		writeError(w, http.StatusNotFound, "not_found", "subscription not found")
	case errors.Is(err, service.ErrAlreadyRevoked):
		writeError(w, http.StatusConflict, "stale", "subscription is already revoked",
			map[string]any{"status": string(database.SubscriptionStatusRevoked)})
	case errors.Is(err, service.ErrDeprovisionIncomplete):
		// Строка не удалена, но подписка уже отозвана: доступ закрыт, а снятие
		// профилей с узлов доводит фоновый воркер.
		writeError(w, http.StatusConflict, "in_use",
			"VPN bindings are still queued for removal; the subscription stays revoked",
			map[string]any{"reason": "deprovision_incomplete"})
	case errors.Is(err, database.ErrPlanNotFound):
		writeError(w, http.StatusBadRequest, "validation", "plan does not exist",
			map[string]any{"plan_id": nil})
	default:
		logger.Error("admin: "+op+" failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

// requireSubActions guards the mutating endpoints: without a wired service the
// panel answers a clear 503 instead of panicking.
func (s *Server) requireSubActions(w http.ResponseWriter) bool {
	if s.subActions == nil {
		writeError(w, http.StatusServiceUnavailable, "internal",
			"subscription service is not wired into the panel")
		return true
	}
	return false
}

func (s *Server) requireSubSync(w http.ResponseWriter) bool {
	if s.subSync == nil {
		writeError(w, http.StatusServiceUnavailable, "internal",
			"sync service is not wired into the panel")
		return true
	}
	return false
}

func toSubscriptionItem(row *database.SubscriptionOverview) subscriptionItem {
	return subscriptionItem{
		ID:             row.ID,
		TelegramID:     row.TelegramID,
		Username:       row.Username,
		SubscriptionID: row.SubscriptionID,
		Status:         row.Status,
		PlanID:         row.PlanID,
		PlanName:       row.PlanName,
		ExpiresAt:      row.ExpiresAt,
		LastRequest:    row.LastRequest,
		NodesActive:    row.NodesActive,
		NodesTotal:     row.NodesTotal,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
}

// parseSubscriptionFilter converts query parameters into the store filter.
// Whitelist violations and malformed values are bad_request, never silent;
// limit above the cap is clamped, not rejected.
func parseSubscriptionFilter(w http.ResponseWriter, r *http.Request) (database.SubscriptionFilter, bool) {
	shared, ok := parseListParams(w, r, subscriptionsDefaultLimit, subscriptionsMaxLimit, subscriptionSortColumns)
	if !ok {
		return database.SubscriptionFilter{}, false
	}
	f := database.SubscriptionFilter{Limit: shared.Limit, Offset: shared.Offset, Sort: shared.Sort, Desc: shared.Desc}

	query := r.URL.Query()

	if raw := query.Get("status"); raw != "" {
		if !subscriptionStatuses[raw] {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown status")
			return f, false
		}
		f.Status = raw
	}

	if raw := query.Get("plan_id"); raw != "" {
		planID, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || planID == 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "plan_id must be a positive integer")
			return f, false
		}
		id := uint(planID)
		f.PlanID = &id
	}

	f.Query = query.Get("q")

	if raw := query.Get("expiring_in"); raw != "" {
		hours, err := strconv.Atoi(raw)
		if err != nil || hours <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "expiring_in must be a positive number of hours")
			return f, false
		}
		f.ExpiringIn = time.Duration(hours) * time.Hour
	}

	return f, true
}

// devicesJSON validates a stored devices/ips TEXT column as JSON. Anything
// that is not a valid document (legacy junk) becomes null, so the SPA never
// receives an unparsable body field.
func devicesJSON(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	return nil
}

func derefInviteCode(code *string) string {
	if code == nil {
		return ""
	}
	return *code
}

func nonZeroPlanID(planID uint) *uint {
	if planID == 0 {
		return nil
	}
	return &planID
}
