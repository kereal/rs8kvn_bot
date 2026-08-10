-- Payment-link state is kept on orders; a separate attempts table is not needed.
ALTER TABLE orders ADD COLUMN payment_url TEXT;
ALTER TABLE orders ADD COLUMN payment_expires_at DATETIME;
ALTER TABLE orders ADD COLUMN payment_creation_uncertain BOOLEAN NOT NULL DEFAULT FALSE;

-- The original provider index allowed empty provider IDs to collide. Drop it
-- while historical duplicate data is normalized; recreate it after cleanup.
DROP INDEX IF EXISTS idx_orders_provider_payment_unique;

-- Keep the oldest pending intent for each subscription/product pair. Older
-- databases may predate this invariant and contain duplicate pending rows.
-- Preserve the oldest real provider ID when old data contains duplicates.
-- Do not alter the financial status of historical paid orders; clear only the
-- duplicate external ID so the new uniqueness invariant can be created.
UPDATE orders
SET provider_payment_id = NULL
WHERE id IN (
    SELECT id
    FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY payment_provider, provider_payment_id
                   ORDER BY id ASC
               ) AS duplicate_number
        FROM orders
        WHERE provider_payment_id IS NOT NULL
          AND TRIM(provider_payment_id) <> ''
    ) AS provider_duplicates
    WHERE duplicate_number > 1
);

-- Keep the oldest Platega pending intent for each subscription/product pair.
UPDATE orders
SET status = 'expired'
WHERE id IN (
    SELECT id
    FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY subscription_id, product_id
                   ORDER BY id ASC
               ) AS duplicate_number
        FROM orders
        WHERE status = 'pending'
          AND payment_provider = 'platega'
    ) AS pending_duplicates
    WHERE duplicate_number > 1
);

CREATE UNIQUE INDEX idx_orders_provider_payment_unique
    ON orders(payment_provider, provider_payment_id)
    WHERE provider_payment_id IS NOT NULL AND TRIM(provider_payment_id) <> '';

CREATE UNIQUE INDEX idx_orders_pending_subscription_product_unique
    ON orders(subscription_id, product_id, payment_provider)
    WHERE status = 'pending' AND payment_provider = 'platega';
