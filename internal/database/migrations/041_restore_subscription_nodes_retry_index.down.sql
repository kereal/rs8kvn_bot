-- Migration: 041_restore_subscription_nodes_retry_index (down)
-- Индекс не несёт данных: удаление безопасно.
DROP INDEX IF EXISTS idx_subscription_nodes_retry;
