package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// ProductStore is the narrow persistence contract the products screen needs.
// The concrete *database.Service satisfies it.
type ProductStore interface {
	ListProductOverviews(ctx context.Context) ([]database.ProductOverview, error)
	CreateProduct(ctx context.Context, p *database.Product) error
	UpdateProductAdmin(ctx context.Context, p *database.Product) error
	DeleteProduct(ctx context.Context, id uint) error
}

type productItem struct {
	ID                 uint      `json:"id"`
	PlanID             uint      `json:"plan_id"`
	PlanName           string    `json:"plan_name"`
	Name               string    `json:"name"`
	DurationDays       int       `json:"duration_days"`
	PriceCents         int64     `json:"price_cents"`
	Currency           string    `json:"currency"`
	IsActive           bool      `json:"is_active"`
	SubscriptionsCount int64     `json:"subscriptions_count"`
	OrdersCount        int64     `json:"orders_count"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type productPayload struct {
	PlanID       uint   `json:"plan_id"`
	Name         string `json:"name"`
	DurationDays int    `json:"duration_days"`
	PriceCents   int64  `json:"price_cents"`
	Currency     string `json:"currency"`
	IsActive     bool   `json:"is_active"`
}

func (s *Server) handleListProducts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.products.ListProductOverviews(r.Context())
	if err != nil {
		s.writeProductStoreError(w, "list products", err)
		return
	}

	items := make([]productItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, productItem{
			ID:                 row.ID,
			PlanID:             row.PlanID,
			PlanName:           row.PlanName,
			Name:               row.Name,
			DurationDays:       row.DurationDays,
			PriceCents:         row.PriceCents,
			Currency:           row.Currency,
			IsActive:           row.IsActive,
			SubscriptionsCount: row.SubscriptionsCount,
			OrdersCount:        row.OrdersCount,
			CreatedAt:          row.CreatedAt,
			UpdatedAt:          row.UpdatedAt,
		})
	}

	items = filterProducts(r, items)

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

// filterProducts applies the optional plan_id and active query filters. Name
// search (?q=) is client-side: the list is a couple of dozen rows.
func filterProducts(r *http.Request, items []productItem) []productItem {
	planID := uint(0)

	if raw := r.URL.Query().Get("plan_id"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 32)
		if err == nil {
			planID = uint(parsed)
		}
	}

	var active *bool

	if raw := r.URL.Query().Get("active"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err == nil {
			active = &value
		}
	}

	if planID == 0 && active == nil {
		return items
	}

	filtered := items[:0]
	for _, item := range items {
		if planID != 0 && item.PlanID != planID {
			continue
		}

		if active != nil && item.IsActive != *active {
			continue
		}

		filtered = append(filtered, item)
	}

	return filtered
}

func (s *Server) handleCreateProduct(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeProductPayload(w, r)
	if !ok {
		return
	}

	product := payload.product()

	err := s.products.CreateProduct(r.Context(), product)
	if err != nil {
		s.writeProductStoreError(w, "create product", err)
		return
	}

	writeJSON(w, http.StatusCreated, productItemFromProduct(*product, "", 0, 0))
}

func (s *Server) handleUpdateProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	payload, ok := decodeProductPayload(w, r)
	if !ok {
		return
	}

	product := payload.product()
	product.ID = id

	err := s.products.UpdateProductAdmin(r.Context(), product)
	if err != nil {
		s.writeProductStoreError(w, "update product", err)
		return
	}

	updated, err := s.products.ListProductOverviews(r.Context())
	if err == nil {
		for _, row := range updated {
			if row.ID == id {
				writeJSON(w, http.StatusOK, productItemFromOverview(row))
				return
			}
		}
	}

	writeJSON(w, http.StatusOK, productItemFromProduct(*product, "", 0, 0))
}

func (s *Server) handleDeleteProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	// Deleted product is loaded before removal so the response can echo it.
	overviews, _ := s.products.ListProductOverviews(r.Context())
	var deleted *productItem

	for i := range overviews {
		if overviews[i].ID == id {
			deleted = new(productItem)
			*deleted = productItemFromOverview(overviews[i])
			break
		}
	}

	err := s.products.DeleteProduct(r.Context(), id)
	if err != nil {
		s.writeProductStoreError(w, "delete product", err)
		return
	}

	if deleted != nil {
		writeJSON(w, http.StatusOK, deleted)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *productPayload) product() *database.Product {
	currency := p.Currency
	if currency == "" {
		currency = "RUB"
	}

	return &database.Product{
		PlanID:       p.PlanID,
		Name:         p.Name,
		DurationDays: p.DurationDays,
		PriceCents:   p.PriceCents,
		Currency:     currency,
		IsActive:     p.IsActive,
	}
}

func productItemFromProduct(p database.Product, planName string, subscriptions, orders int64) productItem {
	return productItem{
		ID:                 p.ID,
		PlanID:             p.PlanID,
		PlanName:           planName,
		Name:               p.Name,
		DurationDays:       p.DurationDays,
		PriceCents:         p.PriceCents,
		Currency:           p.Currency,
		IsActive:           p.IsActive,
		SubscriptionsCount: subscriptions,
		OrdersCount:        orders,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
}

func productItemFromOverview(row database.ProductOverview) productItem {
	return productItem{
		ID:                 row.ID,
		PlanID:             row.PlanID,
		PlanName:           row.PlanName,
		Name:               row.Name,
		DurationDays:       row.DurationDays,
		PriceCents:         row.PriceCents,
		Currency:           row.Currency,
		IsActive:           row.IsActive,
		SubscriptionsCount: row.SubscriptionsCount,
		OrdersCount:        row.OrdersCount,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

// writeProductStoreError maps product sentinels onto HTTP codes with the
// details object the SPA renders, and delegates the rest to the shared mapper.
func (s *Server) writeProductStoreError(w http.ResponseWriter, op string, err error) {
	var fieldErr *database.ProductFieldError

	switch {
	case errors.As(err, &fieldErr):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "validation",
			"message": err.Error(),
			"details": map[string]string{fieldErr.Field: fieldErr.Err.Error()},
		})
	case errors.Is(err, database.ErrProductNotFound):
		writeError(w, http.StatusNotFound, "not_found", "product not found")
	case errors.Is(err, database.ErrProductNameTaken):
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":    "name_taken",
			"message": err.Error(),
			"details": map[string]string{"field": "duration_days"},
		})
	case errors.Is(err, database.ErrProductProtected):
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":    "protected",
			"message": err.Error(),
			"details": map[string]string{"reason": "plan_id_immutable"},
		})
	case errors.Is(err, database.ErrProductInUse):
		var inUse *database.ProductInUseError

		details := map[string]any{}
		if errors.As(err, &inUse) {
			if inUse.Details["subscriptions"] > 0 {
				details["subscriptions"] = inUse.Details["subscriptions"]
			}

			if inUse.Details["orders"] > 0 {
				details["orders"] = inUse.Details["orders"]
			}
		}

		writeJSON(w, http.StatusConflict, map[string]any{
			"code":    "in_use",
			"message": err.Error(),
			"details": details,
		})
	case errors.Is(err, database.ErrProductImmutable):
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":    "protected",
			"message": err.Error(),
			"details": map[string]string{"reason": "immutable_after_order"},
		})
	default:
		s.writeStoreError(w, op, err)
	}
}

func decodeProductPayload(w http.ResponseWriter, r *http.Request) (*productPayload, bool) {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()

	var payload productPayload

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
