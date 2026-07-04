-- Migration: 029_rename_subscriptions_expiry_index
-- Description: Переименовывает индекс idx_subscriptions_expiry → idx_subscriptions_expires_at
-- для соответствия конвенции idx_<table>_<column> (колонка переименована в 015).

DROP INDEX IF EXISTS idx_subscriptions_expiry;
CREATE INDEX IF NOT EXISTS idx_subscriptions_expires_at
    ON subscriptions(expires_at);
