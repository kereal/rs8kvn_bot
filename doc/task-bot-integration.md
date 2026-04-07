# Задача: Интеграция rs8kvn_bot с Proxy Manager

**Дата:** 2026-04-07
**Приоритет:** Высокий
**Зависимость:** Proxy Manager ждёт эти изменения для работы

---

## 1. Контекст

Proxy Manager управляет Xray Core через gRPC. Ему нужно получать данные о подписках из rs8kvn_bot двумя способами:

```
rs8kvn_bot ──Webhook──▶ Proxy Manager ──gRPC──▶ Xray Core
     │
     └──GET /api/v1/subscriptions (fallback, каждые 5 мин)
```

| Механизм | Когда | Зачем |
|----------|-------|-------|
| Webhook | При каждом изменении подписки | Мгновенная синхронизация |
| Polling | Каждые 5 мин | Fallback если webhook пропущен |

---

## 2. Что нужно сделать

### 2.1 Переменные окружения

| Переменная | Описание | Обязательно |
|------------|----------|-------------|
| `API_TOKEN` | Bearer token для `GET /api/v1/subscriptions` | Да |
| `PROXY_MANAGER_WEBHOOK_SECRET` | Bearer token для webhook | Да |
| `PROXY_MANAGER_WEBHOOK_URL` | URL Proxy Manager (`https://.../webhook/subscriptions`) | Да |

```env
API_TOKEN=rs8kvn-api-token-here
PROXY_MANAGER_WEBHOOK_SECRET=webhook-secret-here
PROXY_MANAGER_WEBHOOK_URL=https://proxy-manager.example.com/webhook/subscriptions
```

### 2.2 GET /api/v1/subscriptions

**Request:**
```http
GET /api/v1/subscriptions HTTP/1.1
Authorization: Bearer <API_TOKEN>
```

**Response (200):**
```json
{
  "subscriptions": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "email": "user@example.com",
      "enabled": true,
      "subscription_token": "abc123def456"
    }
  ]
}
```

**Поля:**

| Поле | Тип | Источник | Описание |
|------|-----|----------|----------|
| `id` | UUID | `Subscription.ClientID` | UUID подписки |
| `email` | string | `Subscription.Username` | Username/email |
| `enabled` | bool | `Status == "active"` | Активна ли подписка |
| `subscription_token` | string | `Subscription.SubscriptionID` | Токен подписки |

**Фильтр:** `WHERE status = 'active' AND deleted_at IS NULL`

**Файлы:**
- `internal/web/api.go` — handler `GET /api/v1/subscriptions`
- `internal/web/web.go` — зарегистрировать route + middleware
- `internal/web/middleware.go` — Bearer token auth

### 2.3 Webhook sender

**События:**

| Event | Когда |
|-------|-------|
| `subscription.activated` | Оплата, trial, продление |
| `subscription.expired` | Истечение, деактивация |
| `subscription.updated` | Смена тарифа |

**Request:**
```http
POST /webhook/subscriptions HTTP/1.1
Authorization: Bearer <PROXY_MANAGER_WEBHOOK_SECRET>
Content-Type: application/json

{
  "event_id": "evt-550e8400-e29b-41d4-a716-446655440000",
  "event": "subscription.activated",
  "user_id": "550e8400-e29b-41d4-a716-446655440000",
  "email": "user@example.com",
  "subscription_token": "abc123def456"
}
```

**Ответы Proxy Manager:**

| Код | Тело | Значение |
|-----|------|----------|
| 200 | `ok` | Обработано |
| 200 | `duplicate` | Уже обработано (dedup по event_id) |
| 400 | `bad request` / `unknown event` | Ошибка в запросе |
| 401 | `unauthorized` | Неверный токен |
| 501 | `not implemented` | `subscription.expired` пока не реализован |

**Retry:**
- 3 попытки: **1s → 5s → 15s**
- Асинхронно (goroutine) — не блокирует основной flow
- Все 3 провалились → лог ошибки (Proxy Manager подхватит через polling)
- URL не настроен → warning при старте, отправка пропускается

**Файлы:**
- `internal/webhook/sender.go` — sender с retry
- `internal/webhook/sender_test.go` — тесты
- `internal/service/subscription.go` — вызов webhook при изменении

### 2.4 Точки вызова webhook

| Ситуация | Event |
|----------|-------|
| Оплата / активация | `subscription.activated` |
| Истечение / деактивация | `subscription.expired` |
| Продление | `subscription.activated` |
| Смена тарифа | `subscription.updated` |
| Ручная деактивация (admin) | `subscription.expired` |

---

## 3. Структура изменений

```
rs8kvn_bot/
├── internal/
│   ├── config/config.go         # +3 env vars
│   ├── web/
│   │   ├── web.go               # +route +middleware
│   │   ├── api.go               # NEW: GET /api/v1/subscriptions
│   │   └── middleware.go        # NEW: Bearer token auth
│   ├── webhook/
│   │   ├── sender.go            # NEW: webhook sender
│   │   └── sender_test.go       # NEW: тесты
│   └── service/subscription.go  # +вызов webhook
└── tests/e2e/api_test.go        # NEW: e2e тест API
```

---

## 4. Вопросы и ответы

| Вопрос | Ответ |
|--------|-------|
| Auth middleware | Переиспользуем существующий или создаём `BearerAuthMiddleware` |
| Webhook delivery | Асинхронно (goroutine + retry), не блокируем flow |
| Event ID | UUID v4, формат: `evt-{uuid}` |
| Тестирование | `httptest.Server` для unit + интеграционный тест |
| Логирование | Event type, subscription_id, URL (без secret), status code, duration |
| URL не настроен | Warning при старте, отправка пропускается |

---

## 5. Примеры кода

### Webhook sender

```go
package webhook

type Sender struct {
    client *http.Client
    url    string
    secret string
}

func (s *Sender) SendAsync(event Event) {
    if s.url == "" { return }
    go func() {
        delays := []time.Duration{1 * time.Second, 5 * time.Second, 15 * time.Second}
        for i, delay := range delays {
            if i > 0 { time.Sleep(delay) }
            if s.send(event) == nil { return }
        }
        log.Error("webhook delivery failed", "event_id", event.EventID)
    }()
}

func (s *Sender) send(event Event) error {
    body, _ := json.Marshal(event)
    req, _ := http.NewRequest("POST", s.url, bytes.NewReader(body))
    req.Header.Set("Authorization", "Bearer "+s.secret)
    req.Header.Set("Content-Type", "application/json")
    resp, err := s.client.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode >= 400 {
        return fmt.Errorf("status %d", resp.StatusCode)
    }
    return nil
}
```

### API handler

```go
func (h *Handler) GetSubscriptions(w http.ResponseWriter, r *http.Request) {
    var subs []Subscription
    h.db.Where("status = ? AND deleted_at IS NULL", "active").Find(&subs)

    result := make([]SubscriptionResponse, len(subs))
    for i, s := range subs {
        result[i] = SubscriptionResponse{
            ID:                s.ClientID,
            Email:             s.Username,
            Enabled:           s.Status == "active",
            SubscriptionToken: s.SubscriptionID,
        }
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]any{"subscriptions": result})
}
```

---

## 6. Чек-лист

- [ ] Config: 3 env vars + валидация
- [ ] Middleware: Bearer token auth
- [ ] API: `GET /api/v1/subscriptions`
- [ ] Webhook: sender с retry
- [ ] Webhook: тесты
- [ ] Service: вызов webhook при изменении подписки
- [ ] E2E: тест API endpoint
- [ ] `.env.sample`: новые переменные
- [ ] README: документация

---

## 7. Миграция с текущего прокси

**Проблема:** Сейчас бот отдаёт ссылку на один прокси (текущий сервер). После перехода — ссылку на Proxy Manager (`/sub/:token`), который отдаёт все прокси.

**Решение:** Добавить текущий прокси как `manual_proxies` в конфиге Proxy Manager:

```yaml
sources:
  provider:
    sub_url: "https://provider.example.com/sub/xxxxx"

manual_proxies:
  - tag: "main-server"
    address: "1.2.3.4"
    port: 443
    reality_public_key: "..."
    server_name: "www.microsoft.com"
    short_id: ""
```

**Результат:**
- Юзеры получают N+M прокси (N из Provider + M ручных)
- Текущий сервер не теряется
- Provider subscription НЕ модифицируется

---

## 8. Зависимости

**Нужно до начала:**
- URL Proxy Manager (для `PROXY_MANAGER_WEBHOOK_URL`)
- Сгенерировать `API_TOKEN` и `PROXY_MANAGER_WEBHOOK_SECRET`

**Не нужно:**
- Миграции БД (используем существующую модель Subscription)
- Новые таблицы
