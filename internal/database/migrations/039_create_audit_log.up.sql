-- Migration: 039_create_audit_log
-- Description: Универсальный append-only журнал аудита системы (audit_log).
--
-- Одна строка = одно событие аудита. Таблица НЕ привязана к подпискам:
-- entity_type/entity_id адресуют любую сущность (subscription, order,
-- broadcast, plan, node, product, system), а actor_type/actor_id — того,
-- кто инициировал событие (user, admin, worker, payment_provider, system).
--
-- Принципы:
--   - append-only: строки никогда не обновляются и не удаляются. История
--     подписки переживает физическое удаление подписки (admin /del) — FK нет.
--   - entity_id — TEXT (строковое представление ID), чтобы журнал подходил
--     для сущностей с нечисловыми идентификаторами (например subscription_id
--     строка из 44 символов у trial-подписок).
--   - Нет CHECK-констрейнтов на entity_type/event: аудит — открытый список.
--     Целостность имён обеспечивает единый Go-реестр в database/audit_log.go
--     (константы AuditEvent* и конструкторы). Новое событие = одна константа,
--     миграция не нужна.
--   - old_state/new_state — JSON со снапшотом ИЗМЕНИВШИХСЯ полей (до/после),
--     а не всей строки: компактно и достаточно для расследований.
--   - details — JSON со свободным контекстом (суммы, имена тарифов,
--     статистика рассылки, счётчики агрегатов).
--
-- Запись аудита не должна ломать бизнес-флоу: ошибки записи логируются
-- (logger.Warn), кроме платёжных транзакций, где событие пишется в той же tx.

CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,
    entity_id   TEXT,
    event       TEXT NOT NULL,
    actor_type  TEXT NOT NULL,
    actor_id    TEXT,
    old_state   TEXT,
    new_state   TEXT,
    details     TEXT NOT NULL DEFAULT '{}',
    created_at  DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_log_entity ON audit_log(entity_type, entity_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_event ON audit_log(event);
