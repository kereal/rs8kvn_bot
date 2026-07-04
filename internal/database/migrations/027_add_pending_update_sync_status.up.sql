-- Migration: 027_add_pending_update_sync_status
-- Description: Добавляет статус pending_update для обновления конфигурации ноды
-- (например, при смене тарифного плана).
--
-- SQLite-compatible: пересоздаём таблицу вместо ALTER CHECK.

PRAGMA foreign_keys = OFF;

ALTER TABLE subscription_nodes RENAME TO subscription_nodes_old;

CREATE TABLE subscription_nodes (
    subscription_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN (
            'active',
            'pending_add',
            'pending_remove',
            'pending_update'
        )
    ),
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    retry_at DATETIME,
    last_error TEXT,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (subscription_id, node_id),
    FOREIGN KEY (subscription_id) REFERENCES subscriptions(id),
    FOREIGN KEY (node_id) REFERENCES nodes(id)
);

INSERT INTO subscription_nodes SELECT * FROM subscription_nodes_old;

DROP TABLE subscription_nodes_old;

CREATE INDEX IF NOT EXISTS idx_subscription_nodes_subscription_id
    ON subscription_nodes(subscription_id);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_node_id
    ON subscription_nodes(node_id);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_status
    ON subscription_nodes(status);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_updated_at
    ON subscription_nodes(updated_at);

PRAGMA foreign_keys = ON;
