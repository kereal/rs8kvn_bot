-- Migration: 042_broadcast_idempotency_key (down)
-- Колонка и индекс не несут связей: удаление безопасно.
DROP INDEX IF EXISTS idx_broadcasts_idempotency_key;
ALTER TABLE broadcasts DROP COLUMN idempotency_key;
