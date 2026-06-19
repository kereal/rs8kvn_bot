-- UNIQUE: каждый VPN-клиент привязан ровно к одной подписке
CREATE UNIQUE INDEX idx_subscriptions_client_id_unique ON subscriptions(client_id);

-- UNIQUE: каждый пользователь Telegram имеет ровно одну подписку
CREATE UNIQUE INDEX idx_subscriptions_telegram_id_unique ON subscriptions(telegram_id);

-- Индекс для sync worker: WHERE status IN (...) AND (retry_at IS NULL OR retry_at <= ?)
CREATE INDEX idx_subscription_nodes_retry ON subscription_nodes(status, retry_at);
