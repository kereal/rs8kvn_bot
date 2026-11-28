package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// OverviewStore is the narrow read contract of the state screen. The concrete
// *database.Service satisfies it; the screen never writes anywhere.
type OverviewStore interface {
	GetOverviewSnapshot(ctx context.Context) (*database.OverviewSnapshot, error)
}

// overviewResponse is the wire shape of GET /admin/api/overview. Every block is
// its own object so the view can render one card per concern and the contract
// stays readable in tests.
type overviewResponse struct {
	Build          overviewBuild    `json:"build"`
	Database       overviewDatabase `json:"database"`
	Subscriptions  overviewCounts   `json:"subscriptions"`
	Nodes          overviewNodes    `json:"nodes"`
	Bindings       overviewBindings `json:"bindings"`
	Orders         overviewOrders   `json:"orders"`
	Broadcasts     overviewCounts   `json:"broadcasts"`
	Expiring       overviewExpiring `json:"expiring"`
	Audit          overviewAudit    `json:"audit"`
	LastSubRequest *time.Time       `json:"last_sub_request"`
}

type overviewBuild struct {
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	BuildTime     string `json:"build_time"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

type overviewDatabase struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

// overviewOrders — заказы «Состояния»: счётчик ручной сверки рядом с общим
// total, чтобы неопределённые платежи были видны не только в списке.
type overviewOrders struct {
	Total     int64            `json:"total"`
	ByStatus  map[string]int64 `json:"by_status"`
	Uncertain int64            `json:"uncertain"`
}

type overviewCounts struct {
	Total    int64            `json:"total"`
	ByStatus map[string]int64 `json:"by_status"`
}

type overviewNodes struct {
	Total int64 `json:"total"`
}

type overviewBindings struct {
	ByStatus     map[string]int64 `json:"by_status"`
	PendingTotal int64            `json:"pending_total"`
	// OldestPendingAt is null when the queue is empty.
	OldestPendingAt *time.Time `json:"oldest_pending_at"`
}

type overviewAudit struct {
	Events24h int64 `json:"events_24h"`
}

// overviewExpiring — активные подписки, истекающие в ближайшие 30 дней:
// total за окно и по бакету на день (нулевые дни включены).
type overviewExpiring struct {
	Total int64                 `json:"total"`
	Days  []overviewExpiringDay `json:"days"`
}

type overviewExpiringDay struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.overview.GetOverviewSnapshot(r.Context())
	if err != nil {
		s.writeStoreError(w, "overview", err)
		return
	}

	expiringDays := make([]overviewExpiringDay, len(snapshot.Expiring.Days))
	for i, d := range snapshot.Expiring.Days {
		expiringDays[i] = overviewExpiringDay{Date: d.Date, Count: d.Count}
	}

	writeJSON(w, http.StatusOK, overviewResponse{
		Build: overviewBuild{
			Version:       s.build.Version,
			Commit:        s.build.Commit,
			BuildTime:     s.build.BuildTime,
			UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		},
		Database: overviewDatabase{
			Path:      snapshot.DatabasePath,
			SizeBytes: snapshot.DatabaseSizeBytes,
		},
		Subscriptions: overviewCounts{
			Total:    snapshot.SubscriptionsTotal(),
			ByStatus: snapshot.SubscriptionsByStatus,
		},
		Nodes: overviewNodes{Total: snapshot.NodesTotal},
		Bindings: overviewBindings{
			ByStatus:        snapshot.BindingsByStatus,
			PendingTotal:    snapshot.PendingBindingsTotal(),
			OldestPendingAt: snapshot.OldestPendingBinding,
		},
		Orders: overviewOrders{
			Total:     sumCounts(snapshot.OrdersByStatus),
			ByStatus:  snapshot.OrdersByStatus,
			Uncertain: snapshot.UncertainOrders,
		},
		Broadcasts: overviewCounts{
			Total:    sumCounts(snapshot.BroadcastsByStatus),
			ByStatus: snapshot.BroadcastsByStatus,
		},
		Expiring:       overviewExpiring{Total: snapshot.Expiring.Total, Days: expiringDays},
		Audit:          overviewAudit{Events24h: snapshot.AuditEventsLast24h},
		LastSubRequest: snapshot.LastSubRequest,
	})
}

func sumCounts(counts map[string]int64) int64 {
	var total int64
	for _, count := range counts {
		total += count
	}

	return total
}
