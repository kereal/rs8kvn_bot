CREATE INDEX IF NOT EXISTS idx_subscriptions_deleted_at ON subscriptions(deleted_at);
CREATE INDEX IF NOT EXISTS idx_subscriptions_is_trial ON subscriptions(is_trial);
CREATE INDEX IF NOT EXISTS idx_subscriptions_inbound_id ON subscriptions(inbound_id);
