-- Migration: 018_create_subscription_nodes
-- Description: Таблица фактического состояния синхронизации каждой подписки
-- с каждым VPN-нодом (не план, а конкретная пара подписка×нода).
--
-- Статусы:
--   active        — нода активирована для подписки, последняя синхронизация прошла успешно
--   pending_add   — запрошено добавление ноды, ожидаем выполнения операции на панели
--   pending_remove— запрошено удаление ноды, ожидаем выполнения операции на панели
--
-- Поля:
--   retry_count  — число попыток повторного применения последней операции синхронизации
--   retry_at     — время следующей попытки (NULL при отсутствии запланированной попытки)
--   last_error   — текст последней ошибки синхронизации для диагностики
--   updated_at   — время последнего изменения строки

CREATE TABLE subscription_nodes (
    subscription_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN (
            'active',
            'pending_add',
            'pending_remove'
        )
    ),
    retry_count INTEGER NOT NULL DEFAULT 0,
    retry_at DATETIME,
    last_error TEXT,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (subscription_id, node_id),
    FOREIGN KEY (subscription_id) REFERENCES subscriptions(id),
    FOREIGN KEY (node_id) REFERENCES nodes(id)
);

CREATE INDEX IF NOT EXISTS idx_subscription_nodes_subscription_id
    ON subscription_nodes(subscription_id);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_node_id
    ON subscription_nodes(node_id);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_status
    ON subscription_nodes(status);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_updated_at
    ON subscription_nodes(updated_at);
