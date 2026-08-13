-- Migration: 032_enforce_retry_invariant (DOWN)
-- Description: Откат — убирает инвариант №5, возвращая таблицу к схеме без
-- CHECK (retry_count = 0 OR retry_at IS NOT NULL).

CREATE TABLE subscription_nodes_old (
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

INSERT INTO subscription_nodes_old (subscription_id, node_id, status, retry_count, retry_at, last_error, updated_at)
SELECT subscription_id, node_id, status, retry_count, retry_at, last_error, updated_at
FROM subscription_nodes;

DROP TABLE subscription_nodes;

ALTER TABLE subscription_nodes_old RENAME TO subscription_nodes;

CREATE INDEX IF NOT EXISTS idx_subscription_nodes_subscription_id
    ON subscription_nodes(subscription_id);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_node_id
    ON subscription_nodes(node_id);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_status
    ON subscription_nodes(status);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_updated_at
    ON subscription_nodes(updated_at);

PRAGMA foreign_key_check;
