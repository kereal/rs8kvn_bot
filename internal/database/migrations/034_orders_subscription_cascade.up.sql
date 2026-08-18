-- Migration: 034_orders_subscription_cascade
-- Description: Пересоздаёт orders с ON DELETE CASCADE на FK subscription_id.
--
-- Приложение исторически работало с выключенными внешними ключами (DSN без
-- _foreign_keys=on, а PRAGMA foreign_keys были no-op внутри транзакции
-- golang-migrate), поэтому административное /del оставляло осиротевшие строки
-- orders после физического удаления подписки.
--
-- С включением FK (см. sqliteBusyTimeoutDSN) удаление подписки с заказами
-- стало бы падать с FOREIGN KEY constraint failed, поэтому FK на
-- orders.subscription_id переводится на ON DELETE CASCADE — тот же приём уже
-- применён к subscription_nodes в миграции 022. product_id оставлен без
-- каскада: продукты иммутабельны (ErrProductImmutable) и не удаляются.

PRAGMA foreign_keys = OFF;

CREATE TABLE orders_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    product_id INTEGER NOT NULL REFERENCES products(id),
    status TEXT NOT NULL CHECK (status IN ('pending', 'paid', 'expired', 'canceled')),
    amount_cents INTEGER NOT NULL CHECK (amount_cents >= 0),
    currency CHAR(3) NOT NULL DEFAULT 'RUB',
    payment_provider TEXT,
    provider_payment_id TEXT,
    created_at DATETIME NOT NULL,
    paid_at DATETIME,
    activated_at DATETIME,
    expires_at DATETIME,
    payment_url TEXT,
    payment_expires_at DATETIME,
    payment_creation_uncertain BOOLEAN NOT NULL DEFAULT FALSE
);

INSERT INTO orders_new SELECT * FROM orders;

DROP TABLE orders;

ALTER TABLE orders_new RENAME TO orders;

CREATE INDEX IF NOT EXISTS idx_orders_subscription_id
    ON orders(subscription_id);
CREATE INDEX IF NOT EXISTS idx_orders_status
    ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_created_at
    ON orders(created_at);
CREATE INDEX IF NOT EXISTS idx_orders_product_id
    ON orders(product_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_provider_payment_unique
    ON orders(payment_provider, provider_payment_id)
    WHERE provider_payment_id IS NOT NULL AND provider_payment_id <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_pending_subscription_product_unique
    ON orders(subscription_id, product_id, payment_provider)
    WHERE status = 'pending' AND payment_provider = 'platega';

PRAGMA foreign_keys = ON;

-- golang-migrate executes this script through Exec and discards PRAGMA rows.
-- Convert every returned violation into a CHECK error before the version is stored.
-- The check is scoped to the orders table this migration rebuilds: legacy
-- foreign-key violations elsewhere (e.g. orphaned subscription_nodes from the
-- FK-off /del era) must not block the migration — the same policy the dirty
-- recovery verifier (foreignKeysMigrationSchemaComplete) already applies.
DROP TABLE IF EXISTS migration_034_foreign_key_check;
CREATE TEMP TABLE migration_034_foreign_key_check (
    violation_count INTEGER NOT NULL CHECK (violation_count = 0)
);
INSERT INTO migration_034_foreign_key_check
SELECT COUNT(*) FROM pragma_foreign_key_check('orders');
DROP TABLE migration_034_foreign_key_check;
