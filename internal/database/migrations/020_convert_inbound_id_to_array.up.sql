-- Migration: 020_convert_inbound_id_to_array
-- Description: Конвертирует nodes.inbound_id (INTEGER) в inbound_ids (TEXT JSON array),
--              затем удаляет старую колонку inbound_id.

ALTER TABLE nodes ADD COLUMN inbound_ids TEXT NOT NULL DEFAULT '[]';

UPDATE nodes SET inbound_ids = printf('[%d]', inbound_id) WHERE inbound_id > 0;

ALTER TABLE nodes DROP COLUMN inbound_id;
