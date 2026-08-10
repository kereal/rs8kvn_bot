DROP INDEX IF EXISTS idx_orders_pending_subscription_product_unique;
DROP INDEX IF EXISTS idx_orders_provider_payment_unique;

ALTER TABLE orders DROP COLUMN payment_creation_uncertain;
ALTER TABLE orders DROP COLUMN payment_expires_at;
ALTER TABLE orders DROP COLUMN payment_url;

CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_provider_payment_unique
    ON orders(payment_provider, provider_payment_id)
    WHERE provider_payment_id IS NOT NULL;
