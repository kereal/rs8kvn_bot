-- Migration: 033_enforce_subscription_status_check (DOWN)
-- Description: Откат — убирает CHECK constraint и NOT NULL на status,
-- возвращая колонку к исходному виду status VARCHAR(50) DEFAULT 'active'
-- (без ограничений на значения).

PRAGMA foreign_keys = OFF;

CREATE TABLE subscriptions_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    telegram_id BIGINT NOT NULL,
    username VARCHAR(255),
    client_id VARCHAR(255),
    subscription_id VARCHAR(255),
    status VARCHAR(50) DEFAULT 'active',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    invite_code VARCHAR(16),
    referred_by BIGINT,
    plan_id INTEGER REFERENCES plans(id),
    devices TEXT NOT NULL DEFAULT '[]',
    ips TEXT NOT NULL DEFAULT '[]',
    expires_at DATETIME,
    product_id INTEGER REFERENCES products(id),
    started_at DATETIME,
    price_paid_cents INTEGER NOT NULL DEFAULT 0 CHECK (price_paid_cents >= 0),
    currency CHAR(3),
    last_request DATETIME,
    reminders_sent INTEGER NOT NULL DEFAULT 0
);

INSERT INTO subscriptions_old (
    id, telegram_id, username, client_id, subscription_id, status,
    created_at, updated_at, invite_code, referred_by, plan_id,
    devices, ips, expires_at, product_id, started_at, price_paid_cents,
    currency, last_request, reminders_sent
)
SELECT
    id, telegram_id, username, client_id, subscription_id, status,
    created_at, updated_at, invite_code, referred_by, plan_id,
    devices, ips, expires_at, product_id, started_at, price_paid_cents,
    currency, last_request, reminders_sent
FROM subscriptions;

DROP TABLE subscriptions;

ALTER TABLE subscriptions_old RENAME TO subscriptions;

CREATE INDEX IF NOT EXISTS idx_subscriptions_telegram_id
    ON subscriptions(telegram_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_telegram_id_unique
    ON subscriptions(telegram_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_status
    ON subscriptions(status);
CREATE INDEX IF NOT EXISTS idx_subscriptions_subscription_id
    ON subscriptions(subscription_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_subscription_id_unique
    ON subscriptions(subscription_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_client_id_unique
    ON subscriptions(client_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_expires_at
    ON subscriptions(expires_at);
CREATE INDEX IF NOT EXISTS idx_subscriptions_invite_code
    ON subscriptions(invite_code);
CREATE INDEX IF NOT EXISTS idx_subscriptions_referred_by
    ON subscriptions(referred_by);
CREATE INDEX IF NOT EXISTS idx_subscriptions_last_request
    ON subscriptions(last_request);

PRAGMA foreign_keys = ON;

PRAGMA foreign_key_check;
