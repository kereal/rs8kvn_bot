-- Migration: 030_add_reminders_sent_to_subscriptions
-- Description: Добавляет битовое поле reminders_sent для отслеживания
-- отправленных напоминаний об окончании подписки.
-- Используемые биты:
--   1 << 0 = 1   за 3 дня
--   1 << 1 = 2   за 1 день
--   1 << 2 = 4   за 3 часа

ALTER TABLE subscriptions ADD COLUMN reminders_sent INTEGER NOT NULL DEFAULT 0;
