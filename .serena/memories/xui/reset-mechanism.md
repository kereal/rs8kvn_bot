## Механизм авто-сброса трафика в 3x-ui

### Как работает поле `reset` в 3x-ui

**Неправильное понимание (БЫЛО):**
- `reset = 30` → "сброс 30-го числа каждого месяца" ❌

**Правильное понимание (СТАЛО):**
- `reset = 30` → "сброс каждые 30 дней с даты создания" ✅
- Работает ТОЛЬКО если `expiryTime > 0` (не пустое)
- Если `expiryTime = 0`, авто-сброс НЕ работает

### Механика авто-продления

Когда `expiryTime > 0` и `reset > 0`:
1. При наступлении `expiryTime`:
   - Трафик сбрасывается: `up=0, down=0`
   - `expiryTime` продлевается на `reset` дней
   - Клиент активируется (если был disabled)

2. Формула: `newExpiryTime = expiryTime + (reset * 86400000)` milliseconds

### Исходный код 3x-ui

**Файл:** `web/service/inbound.go`, функция `autoRenewClients()`
**Ссылка:** https://github.com/mhsanaei/3x-ui/blob/main/web/service/inbound.go#L888-L912

```go
func (s *InboundService) autoRenewClients(tx *gorm.DB) (bool, int64, error) {
    var traffics []*xray.ClientTraffic
    now := time.Now().Unix() * 1000
    
    // Ищет клиентов с reset > 0 И expiry_time > 0 И expiry_time <= now
    err = tx.Model(xray.ClientTraffic{}).Where("reset > 0 and expiry_time > 0 and expiry_time <= ?", now).Find(&traffics).Error
    
    for traffic_index, traffic := range traffics {
        newExpiryTime := traffic.ExpiryTime
        for newExpiryTime < now {
            newExpiryTime += (int64(traffic.Reset) * 86400000)  // reset дней * миллисекунды
        }
        traffics[traffic_index].ExpiryTime = newExpiryTime
        traffics[traffic_index].Down = 0  // сброс трафика!
        traffics[traffic_index].Up = 0
        traffics[traffic_index].Enable = true
    }
}
```

### Изменения в проекте (v2.1.0)

1. **Переименование:** `SubscriptionResetDay` → `SubscriptionResetIntervalDays`
2. **Исправлена логика:** `expiryTime = time.Now() + 30 дней` (вместо пустого времени)
3. **Добавлена синхронизация:** при просмотре подписки `ExpiryTime` обновляется из 3x-ui
4. **Переписана функция:** `daysUntilReset(now, expiryTime)` - возвращает -1 если авто-сброс отключен
5. **Тесты:** 8 тестов для `daysUntilReset` (100% покрытие)

### Проблема: устаревание данных в базе бота

Если пользователь не просматривает подписку, данные в базе бота устаревают после авто-продления в 3x-ui.

**Решение:** Фоновая синхронизация (cron job)
