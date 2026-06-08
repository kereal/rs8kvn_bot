CREATE TABLE IF NOT EXISTS products (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    duration_days INTEGER NOT NULL,
    price_cents INTEGER NOT NULL,
    currency CHAR(3) NOT NULL DEFAULT 'RUB',
    is_active INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (plan_id, duration_days)
);

CREATE INDEX IF NOT EXISTS idx_products_plan ON products(plan_id);
