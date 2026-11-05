package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
)

// InviteStore is the narrow persistence contract the invites screen needs. The
// concrete *database.Service satisfies it.
type InviteStore interface {
	ListInviteOverviews(ctx context.Context) ([]database.InviteOverview, error)
	CreateInviteAdmin(ctx context.Context, invite *database.Invite, origin database.InviteOrigin) error
	DeleteInvite(ctx context.Context, code string) (database.Invite, error)
}

type inviteItem struct {
	Code               string    `json:"code"`
	ReferrerTGID       int64     `json:"referrer_tg_id"`
	ReferredCount      int64     `json:"referred_count"`
	SubscriptionsCount int64     `json:"subscriptions_count"`
	CreatedAt          time.Time `json:"created_at"`
}

type invitePayload struct {
	Code         string `json:"code"`
	ReferrerTGID int64  `json:"referrer_tg_id"`
}

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := s.invites.ListInviteOverviews(r.Context())
	if err != nil {
		s.writeStoreError(w, "list invites", err)
		return
	}

	items := make([]inviteItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, inviteItem{
			Code:               row.Code,
			ReferrerTGID:       row.ReferrerTGID,
			ReferredCount:      row.ReferredCount,
			SubscriptionsCount: row.SubscriptionsCount,
			CreatedAt:          row.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeInvitePayload(w, r)
	if !ok {
		return
	}

	invite := &database.Invite{
		Code:         payload.Code,
		ReferrerTGID: payload.ReferrerTGID,
	}

	// Empty code means "generate server-side" with the same alphabet the bot
	// uses, so the panel code works in t.me/{bot}?start=share_{code} as is.
	origin := database.InviteOriginManual

	if invite.Code == "" {
		code, err := utils.GenerateInviteCode()
		if err != nil {
			logger.Error("admin: generate invite code failed", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		invite.Code = code
		origin = database.InviteOriginGenerated
	}

	if err := s.invites.CreateInviteAdmin(r.Context(), invite, origin); err != nil {
		s.writeStoreError(w, "create invite", err)
		return
	}

	writeJSON(w, http.StatusCreated, inviteItem{
		Code:         invite.Code,
		ReferrerTGID: invite.ReferrerTGID,
		CreatedAt:    invite.CreatedAt,
	})
}

func (s *Server) handleDeleteInvite(w http.ResponseWriter, r *http.Request) {
	// The primary key of invites is a string; PathValue is used verbatim.
	code := r.PathValue("code")
	if code == "" {
		writeError(w, http.StatusNotFound, "not_found", "invite not found")
		return
	}

	invite, err := s.invites.DeleteInvite(r.Context(), code)
	if err != nil {
		s.writeStoreError(w, "delete invite", err)
		return
	}

	writeJSON(w, http.StatusOK, inviteItem{
		Code:         invite.Code,
		ReferrerTGID: invite.ReferrerTGID,
		CreatedAt:    invite.CreatedAt,
	})
}

func decodeInvitePayload(w http.ResponseWriter, r *http.Request) (*invitePayload, bool) {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()

	var payload invitePayload

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

// inviteErrorDetails extracts the per-field payload (ADR-0002 P9) from the
// detail-carrying invite sentinels, so the SPA can show which field conflicts.
// Returns nil for sentinels without details.
func inviteErrorDetails(err error) map[string]any {
	var taken *database.InviteNameTakenError
	if errors.As(err, &taken) {
		return map[string]any{"field": taken.Details["field"]}
	}

	var inUse *database.InviteInUseError
	if errors.As(err, &inUse) {
		return map[string]any{"subscriptions": inUse.Details["subscriptions"]}
	}

	return nil
}
