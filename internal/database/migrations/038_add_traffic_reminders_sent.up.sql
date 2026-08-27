-- Migration: 038_add_traffic_reminders_sent
-- Description: Добавляет битовое поле traffic_reminders_sent для отслеживания
-- отправленных уведомлений о трафике (предупреждение о лимите, превышение,
-- авто-включение после сброса).
-- Используемые биты:
--   1 << 0 = 1   израсходовано >= 90% лимита
--   1 << 1 = 2   превышен лимит / клиент отключён панелью
--   1 << 2 = 4   трафик сброшен и клиент включён обратно ("приходите пользоваться")

ALTER TABLE subscriptions ADD COLUMN traffic_reminders_sent INTEGER NOT NULL DEFAULT 0;