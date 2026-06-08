ALTER TABLE subscriptions RENAME COLUMN expires_at TO expiry_time;

ALTER TABLE subscriptions DROP COLUMN product_id;
ALTER TABLE subscriptions DROP COLUMN started_at;
ALTER TABLE subscriptions DROP COLUMN price_paid_cents;
ALTER TABLE subscriptions DROP COLUMN currency;