package admin

import (
	"net/http"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"go.uber.org/zap"
)

// syncSubscriptionResponse wraps the refreshed detail so the view can replace
// the row it already shows. The warning is present only when the reconciler
// could not reach every node: the run is best-effort, so a partial sync is a
// 200 with a message, not an error.
type syncSubscriptionResponse struct {
	Subscription *subscriptionDetail `json:"subscription"`
	Warning      string              `json:"warning,omitempty"`
}

// handleSyncSubscription forces one run of the reconciler for a subscription.
// It exists because the queue drains asynchronously: a stuck pending_remove
// used to leave the operator with a "repeat later" message and no way to
// repeat it. The call is idempotent — it pushes whatever is already pending.
//
// No audit event: the reconciler is the same code the background worker runs,
// and its effects are already visible as binding status changes. Pressing the
// button is not a domain mutation.
func (s *Server) handleSyncSubscription(w http.ResponseWriter, r *http.Request) {
	if s.requireSubSync(w) {
		return
	}

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	syncErr := s.subSync.SyncSubscription(service.WithAuditAdmin(r.Context(), 0), id)

	detail, err := s.buildSubscriptionDetail(r.Context(), id)
	if err != nil {
		// The subscription vanished between the sync and the read: report the
		// read error (404/500), the sync result is moot.
		s.writeSubStoreError(w, "get subscription after sync", err)
		return
	}

	response := syncSubscriptionResponse{Subscription: detail}
	switch {
	case syncErr != nil:
		logger.Warn("admin: forced subscription sync failed",
			zap.Uint("subscription_id", id), zap.Error(syncErr))
		response.Warning = "сверка не удалась: " + syncErr.Error()
	case pendingBindings(detail.Nodes) > 0:
		// SyncSubscription swallows per-node failures by design (the background
		// worker retries them), so a nil error does not mean the queue drained.
		// The post-state is the only honest signal: bindings still queued mean
		// at least one panel did not answer.
		logger.Warn("admin: forced subscription sync left bindings queued",
			zap.Uint("subscription_id", id), zap.Int("pending", pendingBindings(detail.Nodes)))
		response.Warning = "часть узлов не ответила: привязки остались в очереди, повторите позже"
	}

	writeJSON(w, http.StatusOK, response)
}

// pendingBindings counts bindings that are not settled. active is the only
// settled status (see database.SyncStatus).
func pendingBindings(nodes []subscriptionNode) int {
	pending := 0
	for i := range nodes {
		if nodes[i].Status != string(database.SyncStatusActive) {
			pending++
		}
	}

	return pending
}
