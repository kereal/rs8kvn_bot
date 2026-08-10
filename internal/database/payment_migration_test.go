package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration_PaymentIntentSchema(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	sqlDB, err := svc.db.DB()
	require.NoError(t, err)

	rows, err := sqlDB.Query("PRAGMA table_info(orders)")
	require.NoError(t, err)
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, typeName string
		var defaultValue any
		require.NoError(t, rows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk))
		columns[name] = true
	}
	require.NoError(t, rows.Err())
	assert.True(t, columns["payment_url"])
	assert.True(t, columns["payment_expires_at"])
	assert.True(t, columns["payment_creation_uncertain"])

	rows, err = sqlDB.Query("PRAGMA index_list(orders)")
	require.NoError(t, err)
	defer rows.Close()

	indexes := make(map[string]bool)
	for rows.Next() {
		var seq int
		var name string
		var unique, partial int
		var origin string
		require.NoError(t, rows.Scan(&seq, &name, &unique, &origin, &partial))
		indexes[name] = unique == 1 && partial == 1
	}
	require.NoError(t, rows.Err())
	assert.True(t, indexes["idx_orders_provider_payment_unique"])
	assert.True(t, indexes["idx_orders_pending_subscription_product_unique"])
}

func TestMigration_PaymentIntentDuplicateNormalization(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	plan := &Plan{Name: "payment-migration-plan", IsActive: true}
	require.NoError(t, svc.db.Create(plan).Error)
	product := &Product{PlanID: plan.ID, Name: "payment-migration-product", DurationDays: 30, PriceCents: 2300, Currency: "RUB", IsActive: true}
	require.NoError(t, svc.db.Create(product).Error)
	sub := &Subscription{TelegramID: 991001, Username: "migration-user", ClientID: "migration-client", SubscriptionID: "migration-sub", Status: "active", PlanID: plan.ID}
	require.NoError(t, svc.db.Create(sub).Error)

	sqlDB, err := svc.db.DB()
	require.NoError(t, err)
	_, err = sqlDB.Exec("DROP INDEX IF EXISTS idx_orders_pending_subscription_product_unique")
	require.NoError(t, err)
	_, err = sqlDB.Exec("DROP INDEX IF EXISTS idx_orders_provider_payment_unique")
	require.NoError(t, err)

	const insertOrder = `INSERT INTO orders
		(subscription_id, product_id, status, amount_cents, currency, payment_provider,
		 provider_payment_id, created_at, payment_url, payment_expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = sqlDB.Exec(insertOrder, sub.ID, product.ID, "paid", 2300, "RUB", "platega", "550e8400-e29b-41d4-a716-446655440201", "2026-01-01 00:00:00", "https://pay/old", "2026-01-01 00:15:00")
	require.NoError(t, err)
	_, err = sqlDB.Exec(insertOrder, sub.ID, product.ID, "canceled", 2300, "RUB", "platega", "550e8400-e29b-41d4-a716-446655440201", "2026-01-02 00:00:00", "https://pay/duplicate", "2026-01-02 00:15:00")
	require.NoError(t, err)
	_, err = sqlDB.Exec(insertOrder, sub.ID, product.ID, "pending", 2300, "RUB", "platega", "550e8400-e29b-41d4-a716-446655440202", "2026-01-03 00:00:00", "https://pay/pending-old", "2027-01-03 00:15:00")
	require.NoError(t, err)
	_, err = sqlDB.Exec(insertOrder, sub.ID, product.ID, "pending", 2300, "RUB", "platega", "550e8400-e29b-41d4-a716-446655440203", "2026-01-04 00:00:00", "https://pay/pending-new", "2027-01-04 00:15:00")
	require.NoError(t, err)

	_, err = sqlDB.Exec(`UPDATE orders SET provider_payment_id = NULL
		WHERE id IN (SELECT id FROM (SELECT id, ROW_NUMBER() OVER
		(PARTITION BY payment_provider, provider_payment_id ORDER BY id ASC) AS duplicate_number
		FROM orders WHERE provider_payment_id IS NOT NULL AND TRIM(provider_payment_id) <> '') AS provider_duplicates
		WHERE duplicate_number > 1)`)
	require.NoError(t, err)
	_, err = sqlDB.Exec(`UPDATE orders SET status = 'expired'
		WHERE id IN (SELECT id FROM (SELECT id, ROW_NUMBER() OVER
		(PARTITION BY subscription_id, product_id ORDER BY id ASC) AS duplicate_number
		FROM orders WHERE status = 'pending' AND payment_provider = 'platega') AS pending_duplicates
		WHERE duplicate_number > 1)`)
	require.NoError(t, err)
	_, err = sqlDB.Exec(`CREATE UNIQUE INDEX idx_orders_provider_payment_unique
		ON orders(payment_provider, provider_payment_id)
		WHERE provider_payment_id IS NOT NULL AND TRIM(provider_payment_id) <> ''`)
	require.NoError(t, err)
	_, err = sqlDB.Exec(`CREATE UNIQUE INDEX idx_orders_pending_subscription_product_unique
		ON orders(subscription_id, product_id, payment_provider)
		WHERE status = 'pending' AND payment_provider = 'platega'`)
	require.NoError(t, err)

	var providerID, paymentURL string
	require.NoError(t, sqlDB.QueryRow("SELECT provider_payment_id, payment_url FROM orders WHERE id = 1").Scan(&providerID, &paymentURL))
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440201", providerID)
	assert.Equal(t, "https://pay/old", paymentURL)
	var duplicateID *string
	require.NoError(t, sqlDB.QueryRow("SELECT provider_payment_id FROM orders WHERE id = 2").Scan(&duplicateID))
	assert.Nil(t, duplicateID)

	var status, pendingURL string
	require.NoError(t, sqlDB.QueryRow("SELECT status, payment_url FROM orders WHERE id = 3").Scan(&status, &pendingURL))
	assert.Equal(t, "pending", status)
	assert.Equal(t, "https://pay/pending-old", pendingURL)
	require.NoError(t, sqlDB.QueryRow("SELECT status FROM orders WHERE id = 4").Scan(&status))
	assert.Equal(t, "expired", status)
}
