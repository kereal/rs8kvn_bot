-- Migration: 022_fix_subscription_nodes_cascade (DOWN)
-- Description: Откат — убирает ON DELETE CASCADE с FK.

CREATE TABLE subscription_nodes_old (
    subscription_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'pending_add', 'pending_remove')),
    retry_count INTEGER NOT NULL DEFAULT 0,
    retry_at DATETIME,
    last_error TEXT,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (subscription_id, node_id),
    FOREIGN KEY (subscription_id) REFERENCES subscriptions(id),
    FOREIGN KEY (node_id) REFERENCES nodes(id)
);

INSERT INTO subscription_nodes_old SELECT * FROM subscription_nodes;

DROP TABLE subscription_nodes;

ALTER TABLE subscription_nodes_old RENAME TO subscription_nodes;

CREATE INDEX IF NOT EXISTS idx_subscription_nodes_subscription_id ON subscription_nodes(subscription_id);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_node_id ON subscription_nodes(node_id);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_status ON subscription_nodes(status);
CREATE INDEX IF NOT EXISTS idx_subscription_nodes_updated_at ON subscription_nodes(updated_at);
