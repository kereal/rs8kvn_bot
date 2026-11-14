package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
)

// TrialStore — узкий read-контракт экрана «Триал-запросы». Конкретный
// *database.Service удовлетворяет ему; экран нигде не пишет.
type TrialStore interface {
	ListTrialRequests(ctx context.Context, limit, offset int) ([]database.TrialRequest, int64, error)
	GetTrialRequestStats(ctx context.Context) (database.TrialRequestStats, error)
}

const (
	trialLimitDefault = 50
	trialLimitMax     = 200
)

// trialItem — одна строка списка триал-запросов. Антифрод читает IP и время,
// больше на экран ничего не нужно.
type trialItem struct {
	ID        uint      `json:"id"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
}

// trialTopIP — одна строка топа IP за сутки.
type trialTopIP struct {
	IP    string `json:"ip"`
	Count int64  `json:"count"`
}

// trialStatsResponse — форма GET /admin/api/trials/stats.
type trialStatsResponse struct {
	Requests24h  int64        `json:"requests_24h"`
	UniqueIPs24h int64        `json:"unique_ips_24h"`
	TopIPs       []trialTopIP `json:"top_ips"`
}

// trialStore — узкий seam экрана триал-запросов. Все store-поля сервера держат
// одно и то же конкретное *database.Service (NewServer), поэтому берём его из
// поля экрана заказов через утверждение типа: отдельное поле в Server не
// заводится — admin/server.go меняет только интегратор (образец — revenue.go).
func (s *Server) trialStore() (TrialStore, bool) {
	store, ok := s.orders.(TrialStore)
	if !ok {
		logger.Error("admin: orders store does not implement TrialStore")
	}

	return store, ok
}

func (s *Server) handleListTrials(w http.ResponseWriter, r *http.Request) {
	store, ok := s.trialStore()
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "trials store unavailable")

		return
	}

	// Сортировка у списка фиксирована (новые сверху): whitelist сортировки
	// пуст, любые sort/order-параметры отвечают bad_request, а не игнорируются.
	shared, ok := parseListParams(w, r, trialLimitDefault, trialLimitMax, nil)
	if !ok {
		return
	}

	rows, total, err := store.ListTrialRequests(r.Context(), shared.Limit, shared.Offset)
	if err != nil {
		s.writeStoreError(w, "list trial requests", err)

		return
	}

	items := make([]trialItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, trialItem{
			ID:        row.ID,
			IP:        row.IP,
			CreatedAt: row.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleTrialStats(w http.ResponseWriter, r *http.Request) {
	store, ok := s.trialStore()
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "trials store unavailable")

		return
	}

	stats, err := store.GetTrialRequestStats(r.Context())
	if err != nil {
		s.writeStoreError(w, "get trial request stats", err)

		return
	}

	top := make([]trialTopIP, 0, len(stats.TopIPs))
	for _, t := range stats.TopIPs {
		top = append(top, trialTopIP{IP: t.IP, Count: t.Count})
	}

	writeJSON(w, http.StatusOK, trialStatsResponse{
		Requests24h:  stats.Requests24h,
		UniqueIPs24h: stats.UniqueIPs24h,
		TopIPs:       top,
	})
}
