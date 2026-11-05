package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Sentinel errors for the admin products screen.
var (
	ErrProductInvalid   = errors.New("invalid product")
	ErrProductNameTaken = errors.New("product with this plan and duration already exists")
	ErrProductProtected = errors.New("product plan_id cannot be changed")
	ErrProductInUse     = errors.New("product is referenced by subscriptions or orders")
)

// maxProductNameLen caps the product display name. The DB column is 255; the
// admin UI renders it in table cells, so the API keeps it shorter.
const maxProductNameLen = 100

// maxDurationDays bounds duration_days. 3650 = ten years.
const maxDurationDays = 3650

// ProductInUseError carries the blocking counters for ErrProductInUse. The
// HTTP layer turns them into details.subscriptions / details.orders.
type ProductInUseError struct {
	Details map[string]int64
}

func (e *ProductInUseError) Error() string {
	return fmt.Sprintf("product in use: %d subscriptions, %d orders",
		e.Details["subscriptions"], e.Details["orders"])
}

func (e *ProductInUseError) Unwrap() error { return ErrProductInUse }

// ProductFieldError marks the offending field on a validation failure. The
// HTTP layer turns it into details.<field>.
type ProductFieldError struct {
	Field string
	Err   error
}

func (e *ProductFieldError) Error() string { return e.Field + ": " + e.Err.Error() }

func (e *ProductFieldError) Unwrap() error { return e.Err }

func invalidProduct(field, msg string) error {
	return &ProductFieldError{Field: field, Err: fmt.Errorf("%s: %w", msg, ErrProductInvalid)}
}

// ProductOverview is a product row enriched with plan name and usage counts.
// The admin panel needs the counts to disable destructive actions before they
// hit a foreign key.
type ProductOverview struct {
	ID           uint
	PlanID       uint
	PlanName     string
	Name         string
	DurationDays int
	PriceCents   int64
	Currency     string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time

	SubscriptionsCount int64
	OrdersCount        int64
}

// ListProductOverviews returns every product with plan name and subscription/
// order counts in a single query; N+1 lookups are not acceptable here.
func (s *Service) ListProductOverviews(ctx context.Context) ([]ProductOverview, error) {
	var rows []ProductOverview

	result := s.db.WithContext(ctx).
		Model(&Product{}).
		Select(`products.id, products.plan_id, plans.name AS plan_name,
			products.name, products.duration_days, products.price_cents, products.currency,
			products.is_active, products.created_at, products.updated_at,
			(SELECT COUNT(*) FROM subscriptions WHERE subscriptions.product_id = products.id) AS subscriptions_count,
			(SELECT COUNT(*) FROM orders WHERE orders.product_id = products.id) AS orders_count`).
		Joins("JOIN plans ON plans.id = products.plan_id").
		Order("products.id ASC").
		Scan(&rows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list products: %w", result.Error)
	}

	return rows, nil
}

// CreateProduct inserts a product. Validation lives in the repository, not
// only in the HTTP handler: the admin panel is not the only possible caller.
func (s *Service) CreateProduct(ctx context.Context, p *Product) error {
	if p == nil {
		return fmt.Errorf("create product: %w", ErrProductInvalid)
	}

	err := validateProductFields(p)
	if err != nil {
		return err
	}

	_, err = s.GetPlanByID(ctx, p.PlanID)
	if err != nil {
		return invalidProduct("plan_id", "plan not found")
	}

	err = s.db.WithContext(ctx).Create(p).Error
	if err != nil {
		return classifyProductWriteError("create product", err)
	}

	_ = s.insertAuditLogBestEffort(ctx, AuditProductCreated(p))

	return nil
}

// UpdateProductAdmin rewrites mutable product fields. Changing plan_id is
// forbidden outright (a subscription references both the product and the plan,
// moving the product would create a contradictory pair), and changing
// duration_days is blocked while active subscriptions reference the product.
func (s *Service) UpdateProductAdmin(ctx context.Context, p *Product) error {
	if p == nil || p.ID == 0 {
		return fmt.Errorf("update product: %w", ErrProductNotFound)
	}

	current, err := s.GetProductByID(ctx, p.ID)
	if err != nil {
		return err
	}

	if p.PlanID != current.PlanID {
		return fmt.Errorf("plan_id %d differs from current %d: %w", p.PlanID, current.PlanID, ErrProductProtected)
	}

	err = validateProductFields(p)
	if err != nil {
		return err
	}

	if p.DurationDays != current.DurationDays {
		var active int64

		err = s.db.WithContext(ctx).Model(&Subscription{}).
			Where("product_id = ? AND status = ?", p.ID, SubscriptionStatusActive).
			Count(&active).Error
		if err != nil {
			return fmt.Errorf("count product subscriptions: %w", err)
		}

		if active > 0 {
			return &ProductInUseError{Details: map[string]int64{"subscriptions": active}}
		}
	}

	before := productAuditState(*current)

	err = s.UpdateProductGuarded(ctx, p)
	if err != nil {
		return err
	}

	after, err := s.GetProductByID(ctx, p.ID)
	if err != nil {
		return err
	}

	_ = s.insertAuditLogBestEffort(ctx, AuditProductUpdated(p.ID, before, productAuditState(*after)))

	return nil
}

// DeleteProduct removes a product nothing depends on. orders.product_id and
// subscriptions.product_id reference it without ON DELETE, so a delete with
// dependencies would die on the foreign key — counted and rejected instead.
func (s *Service) DeleteProduct(ctx context.Context, id uint) error {
	if id == 0 {
		return fmt.Errorf("delete product: %w", ErrProductNotFound)
	}

	var deleted *Product

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Product

		err := tx.Where("id = ?", id).First(&current).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("product %d: %w", id, ErrProductNotFound)
			}

			return fmt.Errorf("load product for delete: %w", err)
		}

		deleted = &current

		var subscriptions, orders int64

		err = tx.Model(&Subscription{}).Where("product_id = ?", id).Count(&subscriptions).Error
		if err != nil {
			return fmt.Errorf("count product subscriptions: %w", err)
		}

		err = tx.Model(&Order{}).Where("product_id = ?", id).Count(&orders).Error
		if err != nil {
			return fmt.Errorf("count product orders: %w", err)
		}

		if subscriptions > 0 || orders > 0 {
			return &ProductInUseError{Details: map[string]int64{"subscriptions": subscriptions, "orders": orders}}
		}

		result := tx.Delete(&Product{}, id)
		if result.Error != nil {
			return classifyProductWriteError("delete product", result.Error)
		}

		if result.RowsAffected == 0 {
			return fmt.Errorf("product %d: %w", id, ErrProductNotFound)
		}

		return nil
	})
	if err != nil {
		return err
	}

	_ = s.insertAuditLogBestEffort(ctx, AuditProductDeleted(deleted))

	return nil
}

// validateProductFields enforces the catalogue invariants from the spec.
func validateProductFields(p *Product) error {
	if p.PlanID == 0 {
		return invalidProduct("plan_id", "required")
	}

	if p.DurationDays < 1 || p.DurationDays > maxDurationDays {
		return invalidProduct("duration_days", fmt.Sprintf("must be between 1 and %d", maxDurationDays))
	}

	if p.PriceCents < 0 {
		return invalidProduct("price_cents", "must be non-negative")
	}

	if len(p.Name) > maxProductNameLen {
		return invalidProduct("name", fmt.Sprintf("must be at most %d characters", maxProductNameLen))
	}

	p.Name = strings.TrimSpace(p.Name)

	if p.Currency == "" {
		p.Currency = "RUB"
	} else if !isValidCurrency(p.Currency) {
		return invalidProduct("currency", "must be three uppercase latin letters")
	}

	return nil
}

func isValidCurrency(code string) bool {
	if len(code) != 3 {
		return false
	}

	for i := range len(code) {
		if code[i] < 'A' || code[i] > 'Z' {
			return false
		}
	}

	return true
}

// classifyProductWriteError maps SQLite's unique-index violation (and a lost
// FK race on delete) onto sentinels the HTTP layer can translate into 409
// instead of 500.
func classifyProductWriteError(op string, err error) error {
	if err == nil {
		return nil
	}

	switch {
	case strings.Contains(err.Error(), "UNIQUE constraint failed: products.plan_id, products.duration_days"):
		return fmt.Errorf("%s: %w", op, ErrProductNameTaken)
	case strings.Contains(err.Error(), "FOREIGN KEY constraint failed"):
		return fmt.Errorf("%s: %w", op, ErrProductInUse)
	}

	return fmt.Errorf("%s: %w", op, err)
}
