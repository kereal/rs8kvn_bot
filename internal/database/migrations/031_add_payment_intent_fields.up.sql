-- Payment-link state is kept on orders; a separate attempts table is not needed.
-- The application database is guaranteed to contain no legacy payment rows when
-- this migration is applied, so no historical deduplication is required.
ALTER TABLE orders ADD COLUMN payment_url TEXT;
ALTER TABLE orders ADD COLUMN payment_expires_at DATETIME;
ALTER TABLE orders ADD COLUMN payment_creation_uncertain BOOLEAN NOT NULL DEFAULT FALSE;

-- Replace the original provider index with the payment invariants used by the
-- current OrderService. SQLite supports partial indexes in the required version.
DROP INDEX IF EXISTS idx_orders_provider_payment_unique;

CREATE UNIQUE INDEX idx_orders_provider_payment_unique
    ON orders(payment_provider, provider_payment_id)
    WHERE provider_payment_id IS NOT NULL AND provider_payment_id <> '';

CREATE UNIQUE INDEX idx_orders_pending_subscription_product_unique
    ON orders(subscription_id, product_id, payment_provider)
    WHERE status = 'pending' AND payment_provider = 'platega';
