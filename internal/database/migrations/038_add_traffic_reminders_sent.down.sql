-- Revert: удаляет колонку traffic_reminders_sent.

ALTER TABLE subscriptions DROP COLUMN traffic_reminders_sent;