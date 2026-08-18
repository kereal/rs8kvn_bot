-- Фактические суммы, известные только в момент обработки callback:
--   callback_amount_cents — что реально списано с клиента (включая клиентскую
--     часть комиссии провайдера);
--   provider_fee_cents / provider_fee_type — комиссия провайдера, полученная
--     best-effort из API транзакций (GET /transaction/{id}). Оба поля NULL,
--     пока провайдер не ответил (или ответ недоступен).
ALTER TABLE orders ADD COLUMN callback_amount_cents INTEGER;
ALTER TABLE orders ADD COLUMN provider_fee_cents INTEGER;
ALTER TABLE orders ADD COLUMN provider_fee_type INTEGER;
