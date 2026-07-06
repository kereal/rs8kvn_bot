-- Migration: 029_rename_subscriptions_expiry_index
-- Description: Переименовывает индекс idx_subscriptions_expiry → idx_subscriptions_expires_at
-- для соответствия конвенции idx_<table>_<column> (колонка переименована в 015).
--
-- DROP обоих возможных legacy имён: idx_subscriptions_expiry (из миграции 000)
-- и idx_expiry (короткое имя, встречавшееся в ранних правках этой миграции).

DROP INDEX IF EXISTS idx_subscriptions_expiry;
DROP INDEX IF EXISTS idx_expiry;
CREATE INDEX IF NOT EXISTS idx_subscriptions_expires_at
    ON subscriptions(expires_at);
