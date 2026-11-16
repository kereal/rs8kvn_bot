-- Migration: 042_broadcast_idempotency_key
-- Description: Nullable-ключ идемпотентности для создания рассылок из панели.
--
-- POST /admin/api/broadcasts требует header Idempotency-Key (после trim
-- 1..128 символов). Повтор запроса с тем же ключом и тем же payload
-- (name, message_text, filters, planned_at) возвращает существующую
-- рассылку (201), тот же ключ с другим payload — 409.
-- NULL допустим для строк без ключа (legacy/internal создания): в SQLite
-- уникальный индекс пропускает несколько NULL, ограничение на них не
-- накладывается.
ALTER TABLE broadcasts ADD COLUMN idempotency_key TEXT;

-- Существующие строки получают уникальные legacy-значения, чтобы backfill
-- не столкнулся с уникальным индексом.
UPDATE broadcasts SET idempotency_key = 'legacy:' || id WHERE idempotency_key IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_broadcasts_idempotency_key ON broadcasts(idempotency_key);
