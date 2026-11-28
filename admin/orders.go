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
	ListOrderRefunds(ctx context.Context, orderID uint) ([]database.OrderRefund, error)
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

	ProductID        uint          `json:"product_id"`
	ExpiresAt        *time.Time    `json:"expires_at"`
	PaymentURL       string        `json:"payment_url"`
	PaymentExpiresAt *time.Time    `json:"payment_expires_at"`
	Refunds          []orderRefund `json:"refunds"`
}

// orderRefund — финансовый возврат из журнала аудита. Экран показывает его
// историей: инициировать возврат панель не может.
type orderRefund struct {
	Event       string    `json:"event"`
	AmountCents int64     `json:"amount_cents"`
	Currency    string    `json:"currency"`
	Downgraded  bool      `json:"downgraded"`
	ActorType   string    `json:"actor_type"`
	CreatedAt   time.Time `json:"created_at"`
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

	refunds, err := s.orders.ListOrderRefunds(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get order refunds", err)
		return
	}

	items := make([]orderRefund, 0, len(refunds))
	for _, refund := range refunds {
		items = append(items, orderRefund{
			Event:       refund.Event,
			AmountCents: refund.AmountCents,
			Currency:    refund.Currency,
			Downgraded:  refund.Downgraded,
			ActorType:   refund.ActorType,
			CreatedAt:   refund.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, orderDetail{
		orderItem:        toOrderItem(*row),
		ProductID:        row.ProductID,
		ExpiresAt:        row.ExpiresAt,
		PaymentURL:       row.PaymentURL,
		PaymentExpiresAt: row.PaymentExpiresAt,
		Refunds:          items,
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
	shared, ok := parseListParams(w, r, ordersDefaultLimit, ordersMaxLimit, orderSortColumns)
	if !ok {
		return database.OrderAdminFilter{}, false
	}
	f := database.OrderAdminFilter{Limit: shared.Limit, Offset: shared.Offset, Sort: shared.Sort, Desc: shared.Desc}

	query := r.URL.Query()

	if raw := query.Get("status"); raw != "" {
		if !orderStatuses[raw] {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown status")
			return f, false
		}
		f.Status = raw
	}

	f.Query = query.Get("q")

	if raw := query.Get("id"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "id must be a positive integer")
			return f, false
		}
		exact := uint(id)
		f.ID = &exact
	}

	if raw := query.Get("uncertain"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "uncertain must be true or false")
			return f, false
		}
		f.Uncertain = &value
	}

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
