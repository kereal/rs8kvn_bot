package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// OrderStore is the narrow persistence contract the orders screen needs. The
// concrete *database.Service satisfies it.
type OrderStore interface {
	ListOrderOverviews(ctx context.Context, f database.OrderAdminFilter) ([]database.OrderOverview, int64, error)
	GetOrderOverview(ctx context.Context, id uint) (*database.OrderOverview, error)
}

// Screen constants: pagination and whitelist boundaries per spec (P7, P8).
const (
	ordersDefaultLimit = 50
	ordersMaxLimit     = 200
)

var (
	// orderStatuses is the closed set of orders.status values (CHECK constraint).
	orderStatuses = map[string]bool{
		string(database.OrderStatusPending):  true,
		string(database.OrderStatusPaid):     true,
		string(database.OrderStatusExpired):  true,
		string(database.OrderStatusCanceled): true,
	}

	// orderSortColumns whitelists sortable columns; the values are column
	// names, so anything outside this set must be rejected, never interpolated.
	orderSortColumns = map[string]bool{
		"id":           true,
		"created_at":   true,
		"paid_at":      true,
		"amount_cents": true,
	}
)

// orderItem is the list DTO (P24: the DB model is never serialized directly).
type orderItem struct {
	ID                       uint       `json:"id"`
	SubscriptionID           uint       `json:"subscription_id"`
	TelegramID               int64      `json:"telegram_id"`
	PlanName                 string     `json:"plan_name"`
	ProductName              string     `json:"product_name"`
	DurationDays             int        `json:"duration_days"`
	Status                   string     `json:"status"`
	AmountCents              int64      `json:"amount_cents"`
	Currency                 string     `json:"currency"`
	PaymentProvider          string     `json:"payment_provider"`
	ProviderPaymentID        string     `json:"provider_payment_id"`
	CreatedAt                time.Time  `json:"created_at"`
	PaidAt                   *time.Time `json:"paid_at"`
	ActivatedAt              *time.Time `json:"activated_at"`
	PaymentCreationUncertain bool       `json:"payment_creation_uncertain"`
	CallbackAmountCents      *int64     `json:"callback_amount_cents"`
	ProviderFeeCents         *int64     `json:"provider_fee_cents"`
}

// orderDetail is the detail DTO: everything above plus fields that only make
// sense on the detail page. payment_url is not a secret (P16) and an admin
// needs it for manual reconciliation, so it is detail-only.
type orderDetail struct {
	orderItem
	ProductID        uint       `json:"product_id"`
	ExpiresAt        *time.Time `json:"expires_at"`
	PaymentURL       string     `json:"payment_url"`
	PaymentExpiresAt *time.Time `json:"payment_expires_at"`
}

func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	f, ok := parseOrderFilter(w, r)
	if !ok {
		return
	}

	rows, total, err := s.orders.ListOrderOverviews(r.Context(), f)
	if err != nil {
		s.writeStoreError(w, "list orders", err)
		return
	}

	items := make([]orderItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, toOrderItem(row))
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	row, err := s.orders.GetOrderOverview(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get order", err)
		return
	}

	writeJSON(w, http.StatusOK, orderDetail{
		orderItem:        toOrderItem(*row),
		ProductID:        row.ProductID,
		ExpiresAt:        row.ExpiresAt,
		PaymentURL:       row.PaymentURL,
		PaymentExpiresAt: row.PaymentExpiresAt,
	})
}

func toOrderItem(row database.OrderOverview) orderItem {
	return orderItem{
		ID:                       row.ID,
		SubscriptionID:           row.SubscriptionID,
		TelegramID:               row.TelegramID,
		PlanName:                 row.PlanName,
		ProductName:              row.ProductName,
		DurationDays:             row.DurationDays,
		Status:                   string(row.Status),
		AmountCents:              row.AmountCents,
		Currency:                 row.Currency,
		PaymentProvider:          row.PaymentProvider,
		ProviderPaymentID:        row.ProviderPaymentID,
		CreatedAt:                row.CreatedAt,
		PaidAt:                   row.PaidAt,
		ActivatedAt:              row.ActivatedAt,
		PaymentCreationUncertain: row.PaymentCreationUncertain,
		CallbackAmountCents:      row.CallbackAmountCents,
		ProviderFeeCents:         row.ProviderFeeCents,
	}
}

// parseOrderFilter converts query parameters into the store filter. Whitelist
// violations and malformed values are bad_request, never silent (P8); limit
// above the cap is clamped, not rejected (P7).
func parseOrderFilter(w http.ResponseWriter, r *http.Request) (database.OrderAdminFilter, bool) {
	f := database.OrderAdminFilter{Limit: ordersDefaultLimit}

	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be a positive integer")
			return f, false
		}
		if limit > ordersMaxLimit {
			limit = ordersMaxLimit
		}
		f.Limit = limit
	}

	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "offset must be a non-negative integer")
			return f, false
		}
		f.Offset = offset
	}

	if raw := r.URL.Query().Get("status"); raw != "" {
		if !orderStatuses[raw] {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown status")
			return f, false
		}
		f.Status = raw
	}

	f.Query = r.URL.Query().Get("q")

	from, ok := parseOrderDate(w, r, "from")
	if !ok {
		return f, false
	}
	f.From = from

	to, ok := parseOrderDate(w, r, "to")
	if !ok {
		return f, false
	}
	if to != nil {
		// to is inclusive: the whole UTC day.
		end := to.AddDate(0, 0, 1).Add(-time.Nanosecond)
		f.To = &end
	}

	if raw := r.URL.Query().Get("sort"); raw != "" {
		if !orderSortColumns[raw] {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown sort column")
			return f, false
		}
		f.Sort = raw
	}

	switch raw := r.URL.Query().Get("order"); raw {
	case "":
		f.Desc = true
	case "desc":
		f.Desc = true
	case "asc":
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "order must be asc or desc")
		return f, false
	}

	return f, true
}

// parseOrderDate parses a YYYY-MM-DD query parameter as a UTC day start.
func parseOrderDate(w http.ResponseWriter, r *http.Request, name string) (*time.Time, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, true
	}

	day, err := time.ParseInLocation("2006-01-02", raw, time.UTC)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", name+" must be YYYY-MM-DD")
		return nil, false
	}

	return &day, true
}
