-- Down: восстановление колонки inbound_id INTEGER из inbound_ids JSON.
-- Берётся первый элемент массива (если массив пуст — fallback 1).
-- Поддерживает любую длину массива, не ограничивается 8 элементами.

ALTER TABLE nodes ADD COLUMN _inbound_id_temp INTEGER;

UPDATE nodes
SET _inbound_id_temp = COALESCE(json_extract(inbound_ids, '$[0]'), 1);

ALTER TABLE nodes DROP COLUMN inbound_ids;

ALTER TABLE nodes RENAME COLUMN _inbound_id_temp TO inbound_id;
