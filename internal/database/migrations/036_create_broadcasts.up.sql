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
--   blocked_count    — пользователь заблокировал бота.
--   unreachable_count — деактивированный пользователь / удалённый или недоступный чат.
--   failed_count     — прочие ошибки доставки.
--   last_error       — последняя ошибка запуска worker-а.
--   retry_at         — не запускать worker до этого времени.
--   retry_count      — число неудачных запусков подряд.
--   delivery_report  — JSON: {"delivered":[id...],"blocked":[id...],
--                      "unreachable":[id...],
--                      "errors":[{"telegram_id":id,"error":"..."}],
--                      "not_processed":[id...]}.
--   recipients_state — JSON snapshot аудитории и состояния доставки каждого
--                      получателя; хранится в этой же таблице для recovery.
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
    unreachable_count INTEGER NOT NULL DEFAULT 0,
    failed_count     INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    retry_at         DATETIME,
    retry_count      INTEGER NOT NULL DEFAULT 0,
    delivery_report  TEXT NOT NULL DEFAULT '{}',
    recipients_state TEXT NOT NULL DEFAULT '{"snapshot":false,"recipients":[]}',
    created_at       DATETIME NOT NULL,
    updated_at       DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_broadcasts_created_at ON broadcasts(created_at);
CREATE INDEX IF NOT EXISTS idx_broadcasts_status ON broadcasts(status);
