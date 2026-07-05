-- Revert: возвращает индексу прежнее имя.

DROP INDEX IF EXISTS idx_subscriptions_expires_at;
CREATE INDEX IF NOT EXISTS idx_subscriptions_expiry
    ON subscriptions(expires_at);
