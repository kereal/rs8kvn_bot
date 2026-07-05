-- Migration: 028_add_last_request_to_subscriptions
-- Description: Добавляет колонку last_request для отслеживания даты/времени
-- последнего запроса подписки клиентом через субсервер.

ALTER TABLE subscriptions ADD COLUMN last_request DATETIME;

CREATE INDEX IF NOT EXISTS idx_subscriptions_last_request
    ON subscriptions(last_request);
