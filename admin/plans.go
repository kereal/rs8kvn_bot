package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// maxBodyBytes caps admin request bodies. Plan payloads are tiny; the limit
// exists so a malformed client cannot stream unbounded memory.
const maxBodyBytes = 64 << 10

type planItem struct {
	ID                 uint      `json:"id"`
	Name               string    `json:"name"`
	IsActive           bool      `json:"is_active"`
	DevicesLimit       int       `json:"devices_limit"`
	TrafficLimit       int64     `json:"traffic_limit"`
	ProductsCount      int64     `json:"products_count"`
	SubscriptionsCount int64     `json:"subscriptions_count"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type planPayload struct {
	Name         string `json:"name"`
	DevicesLimit int    `json:"devices_limit"`
	TrafficLimit int64  `json:"traffic_limit"`
	IsActive     bool   `json:"is_active"`
}

func (s *Server) handleListPlans(w http.ResponseWriter, r *http.Request) {
	rows, err := s.plans.ListPlanOverviews(r.Context())
	if err != nil {
		s.writeStoreError(w, "list plans", err)
		return
	}

	items := make([]planItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, planItem{
			ID:                 row.ID,
			Name:               row.Name,
			IsActive:           row.IsActive,
			DevicesLimit:       row.DevicesLimit,
			TrafficLimit:       row.TrafficLimit,
			ProductsCount:      row.ProductsCount,
			SubscriptionsCount: row.SubscriptionsCount,
			CreatedAt:          row.CreatedAt,
			UpdatedAt:          row.UpdatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreatePlan(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodePlanPayload(w, r)
	if !ok {
		return
	}

	plan := &database.Plan{
		Name:         payload.Name,
		DevicesLimit: payload.DevicesLimit,
		TrafficLimit: payload.TrafficLimit,
		IsActive:     payload.IsActive,
	}

	err := s.plans.CreatePlan(r.Context(), plan)
	if err != nil {
		s.writeStoreError(w, "create plan", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"id": plan.ID})
}

func (s *Server) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	payload, ok := decodePlanPayload(w, r)
	if !ok {
		return
	}

	plan := &database.Plan{
		ID:           id,
		Name:         payload.Name,
		DevicesLimit: payload.DevicesLimit,
		TrafficLimit: payload.TrafficLimit,
		IsActive:     payload.IsActive,
	}

	err := s.plans.UpdatePlan(r.Context(), plan)
	if err != nil {
		s.writeStoreError(w, "update plan", err)
		return
	}

	// 200 with a JSON body instead of 204: Chromium logs body-less 204 replies
	// to PUT/DELETE as net::ERR_ABORTED in DevTools, which reads like a failure
	// even though the mutation succeeded.
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeletePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	err := s.plans.DeletePlan(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "delete plan", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// writeStoreError maps database sentinels onto HTTP codes the SPA can explain,
// and keeps unexpected failures out of the response body.
func (s *Server) writeStoreError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, database.ErrPlanNotFound):
		writeError(w, http.StatusNotFound, "not_found", "plan not found")
	case errors.Is(err, database.ErrAuditLogNotFound):
		writeError(w, http.StatusNotFound, "not_found", "audit entry not found")
	case errors.Is(err, database.ErrPlanInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, database.ErrPlanNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "plan name already exists")
	case errors.Is(err, database.ErrPlanProtected):
		writeError(w, http.StatusConflict, "protected", err.Error())
	case errors.Is(err, database.ErrPlanInUse):
		writeError(w, http.StatusConflict, "in_use", err.Error())
	case errors.Is(err, database.ErrOrderNotFound):
		writeError(w, http.StatusNotFound, "not_found", "order not found")
	case errors.Is(err, database.ErrInviteNotFound): // screen: invites
		writeError(w, http.StatusNotFound, "not_found", "invite not found")
	case errors.Is(err, database.ErrInviteInvalid): // screen: invites
		writeError(w, http.StatusBadRequest, "validation", err.Error())
	case errors.Is(err, database.ErrInviteNameTaken): // screen: invites
		message := "invite code already exists"
		if details := inviteErrorDetails(err); details != nil && details["field"] == "referrer_tg_id" {
			message = "user already has an invite code"
		}

		writeError(w, http.StatusConflict, "name_taken", message, inviteErrorDetails(err))
	case errors.Is(err, database.ErrInviteInUse): // screen: invites
		// The count is in the message too: ApiError (src/lib/api.js) carries no
		// details, and the SPA shows this text verbatim.
		var inUse *database.InviteInUseError
		if errors.As(err, &inUse) {
			writeError(w, http.StatusConflict, "in_use",
				fmt.Sprintf("invite is used by %d subscriptions", inUse.Details["subscriptions"]),
				inviteErrorDetails(err))
		} else {
			writeError(w, http.StatusConflict, "in_use", err.Error(), inviteErrorDetails(err))
		}
	case errors.Is(err, database.ErrNodeNotFound):
		writeError(w, http.StatusNotFound, "not_found", "node not found")
	case errors.Is(err, database.ErrNodeInvalid):
		writeNodeInvalidError(w, err)
	case errors.Is(err, database.ErrNodeTypeProtected):
		writeError(w, http.StatusConflict, "protected", err.Error())
	case errors.Is(err, database.ErrNodeInUse):
		writeNodeInUseError(w, err)
	default:
		logger.Error("admin: "+op+" failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func decodePlanPayload(w http.ResponseWriter, r *http.Request) (*planPayload, bool) {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()

	var payload planPayload

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

func pathID(w http.ResponseWriter, r *http.Request) (uint, bool) {
	raw := r.PathValue("id")

	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || id == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid id")

		return 0, false
	}

	return uint(id), true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	err := json.NewEncoder(w).Encode(payload)
	if err != nil {
		logger.Warn("admin: failed to encode response", zap.Error(err))
	}
}

func writeError(w http.ResponseWriter, status int, code, message string, details ...map[string]any) {
	payload := map[string]any{"code": code, "message": message}
	if len(details) > 0 && details[0] != nil {
		payload["details"] = details[0]
	}

	writeJSON(w, status, payload)
}
