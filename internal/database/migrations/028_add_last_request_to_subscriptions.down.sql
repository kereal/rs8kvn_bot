-- Revert: удаляет колонку last_request из subscriptions.

DROP INDEX IF EXISTS idx_subscriptions_last_request;

ALTER TABLE subscriptions DROP COLUMN last_request;
