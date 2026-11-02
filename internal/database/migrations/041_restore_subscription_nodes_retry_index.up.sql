-- Migration: 041_restore_subscription_nodes_retry_index
-- Description: Вернуть индекс pending-скана синка.
--
-- idx_subscription_nodes_retry(status, retry_at) был создан в 023, но потерян
-- при пересоздании таблицы в 026, 027 и 032: rebuild-миграции перечисляют
-- только четыре индекса (subscription_id, node_id, status, updated_at).
-- GetPendingSync фильтрует по status IN (...) AND (retry_at IS NULL OR
-- retry_at <= now), то есть без составного индекса скан идёт по одному status
-- и перечитывает все pending-строки каждый проход (5 минут).
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_retry ON subscription_nodes(status, retry_at);
