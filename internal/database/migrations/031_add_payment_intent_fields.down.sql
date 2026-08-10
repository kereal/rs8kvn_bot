DROP INDEX IF EXISTS idx_orders_pending_subscription_product_unique;
DROP INDEX IF EXISTS idx_orders_provider_payment_unique;

-- The current payment-intent schema excludes empty provider IDs from the
-- unique index. Normalize those placeholders before restoring the legacy
-- IS NOT NULL predicate, otherwise rollback can fail on multiple pending
-- orders that have not received a provider transaction ID yet.
UPDATE orders SET provider_payment_id = NULL WHERE provider_payment_id = '';

ALTER TABLE orders DROP COLUMN payment_creation_uncertain;
ALTER TABLE orders DROP COLUMN payment_expires_at;
ALTER TABLE orders DROP COLUMN payment_url;

CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_provider_payment_unique
    ON orders(payment_provider, provider_payment_id)
    WHERE provider_payment_id IS NOT NULL;
