# 3x-ui auto-reset mechanism

## Поле `reset` (auto-renewal)
- `reset = 30` НЕ означает "30-го числа каждого месяца".
- `reset = 30` означает "сброс каждые 30 дней **с даты создания**".
- Работает ТОЛЬКО если `expiryTime > 0` (не пустое).
- Если `expiryTime = 0` — авто-сброс НЕ работает.

## Механика
Когда `expiryTime > 0` и `reset > 0`:
1. При наступлении `expiryTime`:
   - Трафик сбрасывается: `up=0, down=0`
   - `expiryTime` продлевается на `reset` дней
   - Клиент активируется (если был disabled)
2. Формула: `newExpiryTime = expiryTime + (reset * 86400000)` ms.

## Исходный код 3x-ui
Файл: `web/service/inbound.go`, функция `autoRenewClients()`.
[Ссылка на исходник](https://github.com/mhsanaei/3x-ui/blob/main/web/service/inbound.go#L888-L912)

```go
err = tx.Model(xray.ClientTraffic{}).
    Where("reset > 0 and expiry_time > 0 and expiry_time <= ?", now).
    Find(&traffics).Error

for traffic_index, traffic := range traffics {
    newExpiryTime := traffic.ExpiryTime
    for newExpiryTime < now {
        newExpiryTime += (int64(traffic.Reset) * 86400000)
    }
    traffics[traffic_index].ExpiryTime = newExpiryTime
    traffics[traffic_index].Down = 0
    traffics[traffic_index].Up = 0
    traffics[traffic_index].Enable = true
}
```

## Конфигурация в проекте
- **Константа: `config.SubscriptionResetDay = 30`** (НЕ `SubscriptionResetIntervalDays` — старое имя, исправлено).
- В `subscription.go` при `Create`: `expiryTime = time.Now() + SubscriptionResetDay дней`.
- При просмотре подписки: `ExpiryTime` обновляется из x-ui (sync).

## Проблема: устаревание данных в БД бота
Если юзер не заходит — данные в БД бота устаревают после авто-продления в x-ui.
**Решение (на будущее):** фоновая синхронизация (cron).

## Тесты
- 8 тестов для `daysUntilReset` (100% покрытие).
