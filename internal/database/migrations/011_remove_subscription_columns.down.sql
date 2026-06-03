ALTER TABLE subscriptions ADD COLUMN inbound_id INTEGER;
ALTER TABLE subscriptions ADD COLUMN traffic_limit BIGINT DEFAULT 107374182400;
ALTER TABLE subscriptions ADD COLUMN subscription_url VARCHAR(512);
ALTER TABLE subscriptions ADD COLUMN deleted_at DATETIME;
ALTER TABLE subscriptions ADD COLUMN is_trial INTEGER DEFAULT 0;