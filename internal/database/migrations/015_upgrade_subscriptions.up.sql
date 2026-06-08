ALTER TABLE subscriptions RENAME COLUMN expiry_time TO expires_at;

ALTER TABLE subscriptions ADD COLUMN product_id INTEGER REFERENCES products(id);
ALTER TABLE subscriptions ADD COLUMN started_at DATETIME;
ALTER TABLE subscriptions ADD COLUMN price_paid_cents INTEGER NOT NULL DEFAULT 0;
ALTER TABLE subscriptions ADD COLUMN currency CHAR(3);