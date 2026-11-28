package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// SyncQueueStore is the narrow read contract of the sync queue screen. The
// concrete *database.Service satisfies it; the screen never writes anywhere.
type SyncQueueStore interface {
	ListSyncQueue(ctx context.Context, f database.SyncQueueFilter) ([]database.SyncQueueRow, int64, error)
}

const (
	syncQueueDefaultLimit = 50
	syncQueueMaxLimit     = 200
	// syncQueueLastErrorLimit ограничивает last_error в ответе: это свободный
	// текст внешнего API, а не поле для бесконечного дампа.
	syncQueueLastErrorLimit = 300
)

var (
	// syncQueueSortColumns whitelists sortable columns (P8); values are column
	// names of subscription_nodes, never interpolated from the caller.
	syncQueueSortColumns = map[string]bool{
		"updated_at":      true,
		"retry_at":        true,
		"subscription_id": true,
	}

	// syncQueueFilterStatuses — допустимые значения ?status=. active в очередь
	// не входит по определению, поэтому и фильтром не бывает.
	syncQueueFilterStatuses = map[string]bool{
		string(database.SyncStatusPendingAdd):    true,
		string(database.SyncStatusPendingUpdate): true,
		string(database.SyncStatusPendingRemove): true,
	}
)

// syncQueueItem is the DTO of one queued subscription↔node pair. Sync is
// triggered by the existing POST /admin/api/subscriptions/{id}/sync — the row
// only carries the id the UI links to.
type syncQueueItem struct {
	SubscriptionID       uint       `json:"subscription_id"`
	TelegramID           int64      `json:"telegram_id"`
	SubscriptionIDPublic string     `json:"subscription_id_public"`
	NodeID               uint       `json:"node_id"`
	NodeName             string     `json:"node_name"`
	Status               string     `json:"status"`
	RetryCount           int        `json:"retry_count"`
	RetryAt              *time.Time `json:"retry_at"`
	LastError            *string    `json:"last_error"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func (s *Server) handleListSyncQueue(w http.ResponseWriter, r *http.Request) {
	shared, ok := parseListParams(w, r, syncQueueDefaultLimit, syncQueueMaxLimit, syncQueueSortColumns)
	if !ok {
		return
	}

	f := database.SyncQueueFilter{
		Limit:  shared.Limit,
		Offset: shared.Offset,
		Sort:   shared.Sort,
		Desc:   shared.Desc,
	}

	// Очередь сортируется по возрасту записи: без явного order самые старые
	// строки идут первыми (P8 действует только на переданные параметры).
	if r.URL.Query().Get("order") == "" {
		f.Desc = false
	}

	if raw := r.URL.Query().Get("status"); raw != "" {
		if !syncQueueFilterStatuses[raw] {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown status")
			return
		}
		f.Status = raw
	}

	rows, total, err := s.syncQueue.ListSyncQueue(r.Context(), f)
	if err != nil {
		s.writeStoreError(w, "list sync queue", err)
		return
	}

	items := make([]syncQueueItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, syncQueueItem{
			SubscriptionID:       row.SubscriptionID,
			TelegramID:           row.TelegramID,
			SubscriptionIDPublic: row.SubscriptionIDPublic,
			NodeID:               row.NodeID,
			NodeName:             row.NodeName,
			Status:               row.Status,
			RetryCount:           row.RetryCount,
			RetryAt:              row.RetryAt,
			LastError:            truncateLastError(row.LastError),
			UpdatedAt:            row.UpdatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// truncateLastError ограничивает свободный текст ошибки ноды. Пустая строка
// нормализуется в null — UI показывает прочерк.
func truncateLastError(err *string) *string {
	if err == nil {
		return nil
	}

	runes := []rune(*err)
	if len(runes) > syncQueueLastErrorLimit {
		cut := string(runes[:syncQueueLastErrorLimit]) + "…"
		return &cut
	}

	if len(runes) == 0 {
		return nil
	}

	return err
}
