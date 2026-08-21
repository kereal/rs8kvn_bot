-- Migration: 036_create_broadcasts
-- Description: Таблица массовых рассылок (/broadcast).
--
-- Одна строка = одна подтверждённая рассылка. Содержит карточку рассылки
-- (название, текст, фильтры аудитории) и итоговую статистику отправки.
--
-- Поля:
--   filters          — JSON, резерв под таргетинг аудитории; сейчас всегда '{}'.
--   planned_at       — когда рассылка запланирована (nullable на будущее).
--   started_at       — момент начала отправки.
--   finished_at      — момент завершения отправки.
--   recipients_total — сколько уникальных telegram_id обработано.
--   sent_count       — доставлено (API вернул OK).
--   blocked_count    — заблокировали бота / деактивированы / чат не найден.
--   failed_count     — прочие ошибки доставки.
--   delivery_report  — JSON: {"delivered":[id...],"blocked":[id...],
--                      "errors":[{"telegram_id":id,"error":"..."}]}.
--
-- Статусы зафиксированы CHECK-constraint; допустимый набор продублирован в
-- database/models.go (константы BroadcastStatus*) — единый источник.

CREATE TABLE broadcasts (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT NOT NULL,
    filters          TEXT NOT NULL DEFAULT '{}',
    message_text     TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'scheduled'
                         CHECK (status IN ('scheduled', 'running', 'completed', 'failed', 'canceled')),
    planned_at       DATETIME,
    started_at       DATETIME,
    finished_at      DATETIME,
    recipients_total INTEGER NOT NULL DEFAULT 0,
    sent_count       INTEGER NOT NULL DEFAULT 0,
    blocked_count    INTEGER NOT NULL DEFAULT 0,
    failed_count     INTEGER NOT NULL DEFAULT 0,
    delivery_report  TEXT NOT NULL DEFAULT '{}',
    created_at       DATETIME NOT NULL,
    updated_at       DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_broadcasts_created_at ON broadcasts(created_at);
CREATE INDEX IF NOT EXISTS idx_broadcasts_status ON broadcasts(status);
