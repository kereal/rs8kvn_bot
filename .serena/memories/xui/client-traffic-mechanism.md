# XUI Client Traffic API (GET /panel/api/clients/traffic/:email)

## Текущий механизм (после миграции 2026-05)

- Endpoint: GET /panel/api/clients/traffic/{email}
- Response: { "success": true, "obj": { "email", "up", "down", "total", "expiryTime", ... } }
- Код: internal/xui/client.go:390 (doGetClientTraffic)
- Интерфейс: interfaces.XUIClient.GetClientTraffic(ctx, email)
- Поведение: RetryWithBackoff, парсинг через APIResponse.Obj → ClientTraffic (структура уже содержит все нужные поля)
- Использование:
  - service/subscription.go:GetWithTraffic (показ трафика пользователю)
  - service/subscription.go:ReconcileOrphanedClients (проверка существования клиента по "client not found" в ошибке)
- Тесты: internal/xui/client_test.go (TestGetClientTraffic), integration_test, e2e
- Старый endpoint (inbounds/getClientTraffics) полностью удалён из кодовой базы

## Примечания
- Новый API не требует inboundID — работает по email глобально.
- "client not found" по-прежнему приходит в Msg при !success.
- ClientTraffic struct уже поддерживает total/expiryTime из ответа.
