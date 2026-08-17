-- Migration: 033_enforce_subscription_status_check
-- Description: Вводит CHECK constraint на subscriptions.status.
--
-- Исторически колонка создана как status VARCHAR(50) DEFAULT 'active' БЕЗ
-- ограничений — в БД можно было записать любую строку. Инвариант: допустимы
-- только статусы, описанные в database/models.go (константы SubscriptionStatus*):
--
--     active | expired | paused | canceled | revoked
--
-- SQLite не поддерживает ALTER TABLE ADD CONSTRAINT (см. миграцию 032):
-- пересоздаём таблицу целиком. Дочерние FK (subscription_nodes, orders)
-- ссылаются на subscriptions по имени таблицы, поэтому при отключённых
-- foreign_keys таблицу можно удалить и пересоздать без потери ссылок.
--
-- Нормализация legacy-данных: NULL и статусы вне допустимого набора → 'revoked'
-- (терминальный статус: субсервер отдаёт 404, запись сохраняется). Это
-- безопаснее, чем 'active': неизвестная подписка не получает доступ к VPN.

PRAGMA foreign_keys = OFF;

CREATE TABLE subscriptions_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    telegram_id BIGINT NOT NULL,
    username VARCHAR(255),
    client_id VARCHAR(255),
    subscription_id VARCHAR(255),
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (
        status IN ('active', 'expired', 'paused', 'canceled', 'revoked')
    ),
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

INSERT INTO subscriptions_new (
    id, telegram_id, username, client_id, subscription_id, status,
    created_at, updated_at, invite_code, referred_by, plan_id,
    devices, ips, expires_at, product_id, started_at, price_paid_cents,
    currency, last_request, reminders_sent
)
SELECT
    id, telegram_id, username, client_id, subscription_id,
    CASE WHEN status IS NULL
              OR status NOT IN ('active', 'expired', 'paused', 'canceled', 'revoked')
         THEN 'revoked'
         ELSE status
    END,
    created_at, updated_at, invite_code, referred_by, plan_id,
    devices, ips, expires_at, product_id, started_at, price_paid_cents,
    currency, last_request, reminders_sent
FROM subscriptions;

DROP TABLE subscriptions;

ALTER TABLE subscriptions_new RENAME TO subscriptions;

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
