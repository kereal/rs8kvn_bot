-- Revert: удаляет колонку reminders_sent.

ALTER TABLE subscriptions DROP COLUMN reminders_sent;
