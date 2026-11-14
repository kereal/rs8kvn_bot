package admin

import (
	"context"
	"net/http"
	"strconv"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
)

// RevenueStore is the narrow read contract of the revenue aggregate. The
// concrete *database.Service satisfies it; the screen never writes anywhere.
type RevenueStore interface {
	GetRevenueStats(ctx context.Context, days int) (database.RevenueStats, error)
}

// Screen constants for the days window (P7-style bounds: clamp, never reject
// an out-of-range window; malformed values still answer bad_request).
const (
	revenueDefaultDays = 30
	revenueMinDays     = 1
	revenueMaxDays     = 365
)

// revenueResponse is the wire shape of GET /admin/api/revenue. Days are
// consecutive UTC dates ending today, zero-filled for days without paid
// orders — the view renders the chart without gap handling.
type revenueResponse struct {
	Days     []revenueDay  `json:"days"`
	Totals   revenueTotals `json:"totals"`
	Currency string        `json:"currency"`
}

type revenueDay struct {
	Date         string `json:"date"`
	Orders       int64  `json:"orders"`
	RevenueCents int64  `json:"revenue_cents"`
}

type revenueTotals struct {
	PaidOrders    int64 `json:"paid_orders"`
	RevenueCents  int64 `json:"revenue_cents"`
	AvgCheckCents int64 `json:"avg_check_cents"`
	Canceled      int64 `json:"canceled"`
	Expired       int64 `json:"expired"`
}

func (s *Server) handleRevenue(w http.ResponseWriter, r *http.Request) {
	// Узкий seam экрана выручки. Все store-поля сервера держат одно и то же
	// конкретное *database.Service (NewServer), поэтому GetRevenueStats берём
	// из поля экрана заказов через утверждение типа: отдельное поле в Server
	// не заводится — admin/server.go меняет только интегратор.
	store, ok := s.orders.(RevenueStore)
	if !ok {
		logger.Error("admin: orders store does not implement RevenueStore")
		writeError(w, http.StatusInternalServerError, "internal", "revenue store unavailable")
		return
	}

	days := revenueDefaultDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "days must be an integer")
			return
		}
		days = min(max(parsed, revenueMinDays), revenueMaxDays)
	}

	stats, err := store.GetRevenueStats(r.Context(), days)
	if err != nil {
		s.writeStoreError(w, "get revenue stats", err)
		return
	}

	daysPayload := make([]revenueDay, len(stats.Days))
	for i, day := range stats.Days {
		daysPayload[i] = revenueDay{
			Date:         day.Date,
			Orders:       day.Orders,
			RevenueCents: day.RevenueCents,
		}
	}

	writeJSON(w, http.StatusOK, revenueResponse{
		Days: daysPayload,
		Totals: revenueTotals{
			PaidOrders:    stats.Totals.PaidOrders,
			RevenueCents:  stats.Totals.RevenueCents,
			AvgCheckCents: stats.Totals.AvgCheckCents,
			Canceled:      stats.Totals.Canceled,
			Expired:       stats.Totals.Expired,
		},
		Currency: stats.Currency,
	})
}
