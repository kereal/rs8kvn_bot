-- Migration: 034_orders_subscription_cascade (DOWN)
-- Description: Откат — возвращает orders к FK subscription_id без каскада.

PRAGMA foreign_keys = OFF;

CREATE TABLE orders_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL REFERENCES subscriptions(id),
    product_id INTEGER NOT NULL REFERENCES products(id),
    status TEXT NOT NULL CHECK (status IN ('pending', 'paid', 'expired', 'canceled')),
    amount_cents INTEGER NOT NULL CHECK (amount_cents >= 0),
    currency CHAR(3) NOT NULL DEFAULT 'RUB',
    payment_provider TEXT,
    provider_payment_id TEXT,
    created_at DATETIME NOT NULL,
    paid_at DATETIME,
    activated_at DATETIME,
    expires_at DATETIME,
    payment_url TEXT,
    payment_expires_at DATETIME,
    payment_creation_uncertain BOOLEAN NOT NULL DEFAULT FALSE
);

INSERT INTO orders_old SELECT * FROM orders;

DROP TABLE orders;

ALTER TABLE orders_old RENAME TO orders;

CREATE INDEX IF NOT EXISTS idx_orders_subscription_id
    ON orders(subscription_id);
CREATE INDEX IF NOT EXISTS idx_orders_status
    ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_created_at
    ON orders(created_at);
CREATE INDEX IF NOT EXISTS idx_orders_product_id
    ON orders(product_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_provider_payment_unique
    ON orders(payment_provider, provider_payment_id)
    WHERE provider_payment_id IS NOT NULL AND provider_payment_id <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_pending_subscription_product_unique
    ON orders(subscription_id, product_id, payment_provider)
    WHERE status = 'pending' AND payment_provider = 'platega';

PRAGMA foreign_keys = ON;

PRAGMA foreign_key_check;
