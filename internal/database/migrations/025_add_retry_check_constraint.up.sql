-- Migration: 025_add_retry_check_constraint
-- Description: Инвариант №5: если retry_count > 0, то retry_at не может быть NULL.
--
-- ВНИМАНИЕ: это выражение — фактический no-op. SQLite не поддерживает
-- ADD CONSTRAINT через ALTER TABLE: драйвер mattn/go-sqlite3 молча игнорирует
-- его (без ошибки и без эффекта), а свежий системный sqlite3 падает с
-- синтаксической ошибкой. Инвариант реально включается миграцией 032
-- (032_enforce_retry_invariant), которая пересоздаёт таблицу с CHECK.

ALTER TABLE subscription_nodes ADD CHECK (
    retry_count = 0 OR retry_at IS NOT NULL
);
