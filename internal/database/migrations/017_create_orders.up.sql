-- Migration: 017_create_orders
-- Description: Таблица для хранения факта покупки подписки и процесса её обработки.
-- Статусы заказа:
--   pending   — создан, ожидает оплаты
--   paid      — оплата подтверждена
--   expired   — истек срок ожидания оплаты
--   canceled  — отменён

CREATE TABLE orders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL REFERENCES subscriptions(id),
    product_id INTEGER NOT NULL REFERENCES products(id),
    status TEXT NOT NULL CHECK (status IN ('pending', 'paid', 'expired', 'canceled')),
    amount_cents INTEGER NOT NULL,
    currency CHAR(3) NOT NULL DEFAULT 'RUB',
    payment_provider TEXT,
    provider_payment_id TEXT,
    created_at DATETIME NOT NULL,
    paid_at DATETIME,
    activated_at DATETIME,
    expires_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_orders_subscription_id ON orders(subscription_id);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_created_at ON orders(created_at);
CREATE INDEX IF NOT EXISTS idx_orders_product_id ON orders(product_id);
