# Техническое задание: интеграция платёжной системы Platega.io

Версия: 1.5 · Дата: 2026-08-10 · Статус: согласовано с текущей реализацией


---

## 0. Глоссарий и официальные источники

- **Platega** — внешний платёжный провайдер.
- **Исходящий endpoint** — `POST https://app.platega.io/v2/transaction/process`; способ оплаты выбирает плательщик на странице Platega.
- **Входящий callback** — `POST /payment/callback`; Platega передаёт заголовки `X-MerchantId`/`X-Secret` и JSON body.
- **Order** — запись в `orders`. В этой реализации один Order является одной платёжной попыткой/intent. До получения `provider_payment_id` повтор после явного HTTP 400/401 может использовать тот же pending-заказ. После неопределённого результата автоматический повтор запрещён. Отдельная таблица попыток не вводится.
- **Статусы Order** — только `pending | paid | expired | canceled`.
- **Product** — тариф из `products`: `id, plan_id, name, duration_days, price_cents, currency, is_active`.
- **subURL** — публичная ссылка `Config.SubURL(subscriptionID)`.

Официальные источники:

- https://docs.platega.io/
- https://docs.platega.io/llms.txt
- «Создание платёжной ссылки без заданного метода»
- «Callback об изменении статуса транзакции»

В общей схеме статусов транзакции Platega встречаются `PENDING`, `CANCELED`, `CONFIRMED`, `CHARGEBACKED`. В описании webhook явно указаны `CONFIRMED`, `CANCELED` и `CHARGEBACKED`; реализация также безопасно принимает официальный `PENDING` как no-op. Значения `SUCCESS`, `PAID` и иные aliases не поддерживаются.

---

## 1. Цели и бизнес-правила

1. Подключить оплату через Platega для Telegram-бота.
2. Показывать только платные продукты, привязанные к активному плану:
   `products.is_active=true AND products.price_cents > 0 AND plans.is_active=true`.
3. Любой пользователь может выбрать платный продукт. Если подписки нет, `RequestPayment` создаёт её до создания заказа.
4. Повторные и параллельные callback не продлевают подписку повторно и не отправляют второе success-сообщение.
5. После фактической оплаты отправлять новое сообщение с тарифом, сроком, трафиком и subURL; исходный экран не редактировать.
6. Использовать ту же presentation-логику для success-сообщения и экрана «Моя подписка».
7. Удалить старую бесплатную upgrade-логику и hardcoded-кнопку `buy_premium_230`.
8. Пока платёжная ссылка действительна, повторно возвращать ту же ссылку без создания нового Order и новой транзакции.
9. После истечения ссылки старый pending-заказ переводить в `expired`, не перезаписывая его provider ID и URL. Следующий запрос создаёт новый pending-заказ.
10. Срок ссылки брать из ответа Platega `expiresIn`; дополнительный grace-период не вводить.
11. Внешнюю VPN-синхронизацию выполнять после commit с отдельным timeout не более 20 секунд. Ошибка или timeout синхронизации не отменяют оплату; фоновые workers повторяют доставку.

---

## 2. Конфигурация

### 2.1 Environment variables

| Имя | Тип | Default | Описание |
|---|---|---|---|
| `PAYMENT_ENABLED` | bool | `false` | Включает платежи и платёжные кнопки |
| `PAYMENT_PROVIDER` | string | `platega` | Идентификатор провайдера |
| `PLATEGA_MERCHANT_ID` | string | пусто | UUID мерчанта |
| `PLATEGA_SECRET` | string | пусто | API-ключ |

Base URL `https://app.platega.io` — константа клиента, не env.

Правила валидации:

- при `PAYMENT_ENABLED=false` credentials могут быть пустыми;
- при `PAYMENT_ENABLED=true` provider обязан быть `platega`, а merchant ID и secret — непустыми;
- ошибка должна явно указывать отсутствующие credentials.

Добавить параметры в существующую систему конфигурации и `.env.example`. Удалить `MAIN_MENU_BTN_PRODUCT` и `MainMenuBtnProductID`.

---

## 3. База данных и модель Order

### 3.1 Миграция

Добавить одну миграцию без отдельной таблицы попыток. Проект использует SQLite и partial indexes.

```sql
-- Migration 031_add_payment_intent_fields
ALTER TABLE orders ADD COLUMN payment_url TEXT;
ALTER TABLE orders ADD COLUMN payment_expires_at DATETIME;
ALTER TABLE orders ADD COLUMN payment_creation_uncertain BOOLEAN NOT NULL DEFAULT FALSE;

DROP INDEX IF EXISTS idx_orders_provider_payment_unique;

-- Before creating these indexes, normalize historical duplicates: keep the
-- oldest provider ID and oldest Platega pending intent; terminalize the rest.
-- The migration then recreates both invariants.
CREATE UNIQUE INDEX idx_orders_provider_payment_unique
    ON orders(payment_provider, provider_payment_id)
    WHERE provider_payment_id IS NOT NULL AND TRIM(provider_payment_id) <> '';

CREATE UNIQUE INDEX idx_orders_pending_subscription_product_unique
    ON orders(subscription_id, product_id, payment_provider)
    WHERE status = 'pending' AND payment_provider = 'platega';
```

Перед созданием индекса нужно детерминированно обработать исторические дубли pending-заказов: для каждой пары `(subscription_id, product_id)` сохранить заказ с минимальным `id`, а остальные перевести в `expired`. Их `payment_url` не изменяется. Если у нескольких исторических заказов одинаковый непустой `provider_payment_id`, сохранить его только у заказа с минимальным `id`, а у остальных очистить значение, иначе уникальный индекс создать невозможно; такие строки остаются доступными для ручного аудита по Order ID. При конфликте индекса в конкурентном запросе сервис должен перечитать уже существующий pending-заказ и вернуть его, а не показывать инфраструктурную ошибку.

### 3.2 Новые поля Order

Добавить в `internal/database/models.go`:

```go
PaymentURL                string     `gorm:"column:payment_url"`
PaymentExpiresAt          *time.Time `gorm:"column:payment_expires_at"`
PaymentCreationUncertain  bool       `gorm:"not null;default:false;column:payment_creation_uncertain"`
```

`PaymentExpiresAt` хранится в UTC. `nil` означает, что ссылка ещё не создана или провайдер не вернул срок. Внутренние методы OrderService/репозитория принимают `uuid.UUID`, но универсальное DB-поле `orders.provider_payment_id` остаётся строковым для совместимости с историческими ID других провайдеров. Для Platega строка валидируется и преобразуется в UUID на границе; повреждённое сохранённое значение даёт контролируемую ошибку без panic.

### 3.3 Семантика сроков

`orders.expires_at` — не срок жизни платёжной ссылки. Это snapshot срока подписки, выданного конкретной оплатой:

```text
orders.expires_at == subscriptions.expires_at
```

на момент перехода `pending → paid`.

Срок платёжной ссылки хранится отдельно:

- `payment_url` — выбранная ссылка (`url`, иначе `redirect`);
- `payment_expires_at` — локальная оценка абсолютного UTC-времени, рассчитанная как `receivedAt + expiresIn`. Таймер фактически запускается у Platega раньше, поэтому окончательным источником статуса платежа остаётся callback Platega; grace-период не добавляется;
- `payment_creation_uncertain=true` — запрос к Platega завершился неопределённо, поэтому повторное создание запрещено до ручной сверки.

Не использовать `orders.expires_at` для invoice timeout.

### 3.4 Неизменяемость Product

После появления первого Order для Product запрещено менять:

- `name`;
- `plan_id`;
- `duration_days`;
- `price_cents`;
- `currency`.

Для нового тарифа или исправления цены создавать новый Product. Старый Product можно деактивировать через `is_active=false`.

Запрет является операционным правилом. Все изменения Product должны проходить через единый guarded repository method, который перед обновлением повторно загружает Product, проверяет наличие любого Order по `product_id` и при наличии Order возвращает ошибку, если меняется хотя бы одно из пяти перечисленных полей. Изменение только `is_active` разрешено. Прямые административные SQL-изменения этим контрактом не поддерживаются. Добавить тест на этот guard. Отдельные snapshot-поля в `orders` не добавляются.

### 3.5 Репозиторные методы

`internal/database/products.go`:

```go
// ListActiveProducts возвращает платные продукты активных планов
// в детерминированном порядке цены и ID.
func (s *Service) ListActiveProducts(ctx context.Context) ([]Product, error)
```

SQL-фильтр:

```sql
SELECT products.*
FROM products
JOIN plans ON plans.id = products.plan_id
WHERE products.is_active = true
  AND products.price_cents > 0
  AND plans.is_active = true
ORDER BY products.price_cents ASC, products.id ASC;
```

`internal/database/orders.go`:

```go
func (s *Service) GetOrderByProviderPaymentID(
    ctx context.Context,
    provider string,
    providerPaymentID uuid.UUID,
) (*Order, error)

func (s *Service) UpdateOrderProviderPaymentID(
    ctx context.Context,
    orderID uint,
    providerPaymentID uuid.UUID,
) error

func (s *Service) FindOrCreatePendingPaymentOrder(
    ctx context.Context,
    subscriptionID, productID uint,
    amountCents int64,
    currency string,
    now time.Time,
) (*Order, error)

func (s *Service) ConfirmOrderPaidCAS(
    ctx context.Context,
    orderID uint,
    paidAt time.Time,
    activatedAt time.Time,
    sub *Subscription,
    newExpiry time.Time,
    product *Product,
    applyPlan ApplyPlanInTxFn,
) (bool, error)

func (s *Service) CancelOrderCAS(
    ctx context.Context,
    provider string,
    providerPaymentID uuid.UUID,
    fromStatuses []OrderStatus,
) (bool, error)
```

`UpdateOrderProviderPaymentID` обновляет только pending-заказ.

`FindOrCreatePendingPaymentOrder` обязан:

1. найти pending-заказ по конкретным `subscription_id` и `product_id`;
2. первым делом проверить `payment_creation_uncertain`;
3. вернуть действующую ссылку без нового запроса к Platega;
4. перевести истёкший pending-заказ в `expired` и создать новый;
5. обработать конфликт partial unique index перечитыванием существующего заказа.

`ConfirmOrderPaidCAS` в одной DB-транзакции выполняет:

- CAS `pending → paid` с условием `WHERE status='pending'`;
- запись `paid_at`, `activated_at`, `expires_at`;
- обновление подписки, `reminders_sent=0`;
- DB-setup sync через `applyPlan` в той же транзакции.

Ошибка `applyPlan` откатывает всю транзакцию.

`ApplyPlanInTxFn`:

```go
type ApplyPlanInTxFn func(
    ctx context.Context,
    tx *gorm.DB,
    subscriptionID uint,
    planID uint,
) error
```

Обновить `ProductRepository`, `OrderRepository`, fakes и DatabaseService fake.

---

## 4. Клиент Platega

### 4.1 Расположение и конфигурация

- `internal/service/payment/platega/client.go` — исходящий API;
- `internal/service/payment/platega/callback.go` — callback payload и fixed-point amount parser.

```go
type Config struct {
    MerchantID string
    Secret     string
    BaseURL    string
    HTTPClient *http.Client
}
```

Defaults:

- BaseURL: `https://app.platega.io`;
- HTTP timeout: 5 секунд.

### 4.2 Создание транзакции

```text
POST {BaseURL}/v2/transaction/process
```

Заголовки:

```text
X-MerchantId: <UUID>
X-Secret: <secret>
Content-Type: application/json
```

`paymentMethod` не передавать.

Тело:

```json
{
  "paymentDetails": { "amount": 230.00, "currency": "RUB" },
  "description": "Оплата тарифа Premium на 30 дней",
  "return": "https://t.me/<bot_username>",
  "failedUrl": "https://t.me/<bot_username>",
  "payload": "<order_id>",
  "metadata": {
    "userId": "123456789",
    "userName": "@username"
  }
}
```

Сумма хранится в копейках. JSON amount формировать из целого числа fixed-point форматированием; `float64` запрещён.

### 4.3 API-типы

```go
type CreateTransactionRequest struct {
    AmountCents int64
    Currency    string
    Description string
    ReturnURL   string
    FailedURL   string
    Payload     string
    UserID      string
    UserName    string
}

type CreateTransactionResponse struct {
    TransactionID string `json:"transactionId"` // внешний JSON-string, UUID v4 после валидации на границе
    Status        string `json:"status"`
    URL           string `json:"url"`
    Redirect      string `json:"redirect"`
    ExpiresIn     string `json:"expiresIn"`
}
```

`transactionId` обязателен и должен быть UUID v4; выбранная ссылка и `expiresIn` также обязательны. `expiresIn` должен разбираться как `HH:MM:SS`. Если ID, обе ссылки или срок отсутствуют/некорректны — `ErrProvider`. Клиент и webhook используют один UUID-контракт.

Клиент принимает и `url`, и `redirect`; приоритет `url`, затем `redirect`.

Ошибки:

- 400 → `ErrBadRequest`;
- 401 → `ErrAuth`;
- 5xx, timeout, context cancellation, malformed body → `ErrProvider`.

Ответ ограничить через `io.LimitReader`.

### 4.4 Callback payload

```go
type CallbackPayload struct {
    ID            string      `json:"id"`
    Amount        json.Number `json:"amount"`
    Currency      string      `json:"currency"`
    Status        string      `json:"status"`
    PaymentMethod *int        `json:"paymentMethod,omitempty"`
    Payload       string      `json:"payload,omitempty"`
}
```

Обязательны `id`, `amount`, `currency`, `status`. В текущей реализации `id` должен быть UUID, как указано в официальной схеме Platega. `payload` и `paymentMethod` опциональны: документация показывает `paymentMethod` в примерах, но не включает его в `required`.

Для webhook принимаются только следующие значения, официально присутствующие в схемах Platega:

- `PENDING` — официальный статус общей модели транзакции; Warn и HTTP 200 без изменения заказа;
- `CANCELED` — статус webhook; отмена pending-заказа;
- `CONFIRMED` — статус webhook; подтверждение pending-заказа;
- `CHARGEBACKED` — официальный статус общей модели транзакции; перевод pending/paid в canceled с обязательным ручным разбором, без автоматического отзыва подписки.

Любое неизвестное будущее значение статуса не изменяет заказ, записывается в Warn/audit-log и получает HTTP 200. `SUCCESS` и `PAID` не являются допустимыми aliases.

Сумму callback разбирать fixed-point parser-ом. Отрицательные, пустые, нечисловые значения и более двух знаков после запятой отклонять.

---

## 5. OrderService

### 5.1 Удаление legacy flow

Удалить бесплатный upgrade flow:

- `handleUpgradePremium`;
- `handleConfirmUpgradePremium`;
- `buy_premium_230`;
- `upgrade_premium`;
- `confirm_upgrade_premium`;
- `freeUpgradeLabel` и `getFreeUpgradeLabel`;
- `MainMenuBtnProductID` и `MAIN_MENU_BTN_PRODUCT`;
- `ActivateProduct`;
- `RenewSubscription`, если production callers отсутствуют;
- связанные сообщения и тесты, которые больше не используются.

Платная активация выполняется только через `RequestPayment` и `ConfirmPayment`.

### 5.2 PaymentProvider

```go
type PaymentProvider interface {
    CreateTransaction(
        ctx context.Context,
        req platega.CreateTransactionRequest,
    ) (*platega.CreateTransactionResponse, error)
}
```

Обновить constructor и все callers.

### 5.3 RequestPayment

```go
func (o *OrderService) RequestPayment(
    ctx context.Context,
    telegramID int64,
    username string,
    product *database.Product,
) (*PaymentInfo, *database.Order, error)
```

Алгоритм:

1. `payment == nil` → `ErrPaymentDisabled`.
2. Некорректный Telegram ID, nil/inactive/free product → ошибка.
3. Повторно загрузить канонический Product и его Plan из БД по `product.ID`; использовать из БД `Currency`, `DurationDays` и `PlanID`. Устаревший или поддельный объект отклонить.
4. Получить или создать подписку.
5. Найти pending-заказ в транзакции:
   - `payment_creation_uncertain=true` → запретить новый вызов Platega, Warn/audit-log, ручная сверка;
   - действующие `payment_url` и `payment_expires_at` → вернуть сохранённую ссылку даже если Product или Plan уже деактивированы;
   - активность `Product/Plan`, `price_cents > 0` и пригодность для новой покупки проверять только перед созданием новой попытки или нового pending-заказа;
   - истёкший заказ → перевести в `expired`, не перезаписывать его ID/URL;
   - pending без provider ID после явного 400/401 → переиспользовать;
   - если заказа нет → создать новый pending.
6. Перед вызовом Platega атомарно установить `payment_creation_uncertain=true`.
7. Создать транзакцию с суммой продукта, валютой, `payload=order.ID` и metadata Telegram.
8. При HTTP 400/401 сбросить флаг и вернуть ошибку; повтор на том же pending разрешён.
9. При timeout, context cancellation, 5xx или неполном ответе оставить флаг `true`, записать Warn/audit-log и не создавать новую транзакцию автоматически.
10. При успехе сохранить transaction ID, ссылку и `payment_expires_at = receivedAt + expiresIn`, затем сбросить флаг.
11. Вернуть PaymentInfo с provider ID, URL и временем истечения.

### 5.4 ConfirmPayment

```go
type PaymentConfirmation struct {
    Order     *database.Order
    Activated bool
}

func (o *OrderService) ConfirmPayment(
    ctx context.Context,
    providerPaymentID uuid.UUID,
    amount json.Number,
    currency string,
) (*PaymentConfirmation, error)
```

Алгоритм:

1. Найти заказ по `(provider, provider_payment_id)`.
   - неизвестный ID → Warn/audit-log и успешный `Activated=false`, HTTP 200;
   - `expired` → late callback записать в Warn/audit-log, не активировать, HTTP 200;
   - `paid` → идемпотентный no-op без уведомления.
2. Проверить точное совпадение валюты и суммы в копейках. Несовпадение → бизнес-ошибка и HTTP 400.
3. Загрузить канонический Product и Subscription по сохранённым ID заказа. Для расчёта использовать сохранённый ProductID и зафиксированные в Order сумму/валюту; изменение текущего каталога не должно менять уже созданную покупку.
4. Вычислить `newExpiry := calculateProductExpiry(...)`.
5. Вызвать `ConfirmOrderPaidCAS` с `applyPlan`.
6. Если CAS не изменил строку:
   - paid → no-op;
   - canceled/expired → позднее подтверждение запрещено, Warn/audit-log.
7. После commit выполнить `SyncSubscription` с отдельным timeout ≤20 секунд. Ошибка только логируется Warn и не превращает callback в 5xx.
8. Только `Activated=true` имеет право вызвать success-уведомление.

Допустимые переходы:

```text
pending → paid
pending → canceled
pending → expired       только при новом RequestPayment после payment_expires_at
paid → paid              no-op
paid → canceled          только CHARGEBACKED, без автоматического отзыва подписки
canceled → canceled      no-op
canceled → paid          запрещено
expired → paid           запрещено
```

### 5.5 CancelPaymentByProvider

```go
func (o *OrderService) CancelPaymentByProvider(
    ctx context.Context,
    providerPaymentID uuid.UUID,
    status string,
    amount json.Number,
    currency string,
) (*database.Order, bool, error)
```

- неизвестный ID → Warn/audit-log, HTTP 200, изменений нет;
- перед отменой проверить точное совпадение суммы и валюты с Order; mismatch → HTTP 400 без изменения заказа;
- `CANCELED` переводит только `pending → canceled`;
- `CHARGEBACKED` может перевести `pending` или `paid` в `canceled`, подписка автоматически не отзывается; webhook только фиксирует событие для ручного разбора;
- повторный callback — no-op;
- `SUCCESS`, `PAID` и неизвестные статусы не обрабатывать.

`payload` callback можно писать в audit-log на webhook-слое; менять сигнатуры сервисных методов только ради payload не требуется.

### 5.6 Обязательные тесты OrderService

- успешный RequestPayment;
- disabled/invalid product/invalid Telegram ID;
- повтор после 400/401 без второго pending-заказа;
- действующая ссылка возвращается повторно;
- истёкшая ссылка переводит заказ в expired и создаёт новый Order;
- timeout/5xx устанавливает `payment_creation_uncertain`, блокирует автоматический retry и отправляет админу Telegram-сообщение с order ID, Telegram ID, товаром, суммой, валютой, причиной и доступными данными провайдера;
- параллельный конфликт partial unique index перечитывает существующий pending-заказ;
- exact amount/currency;
- неизвестный provider ID;
- late CONFIRMED для expired: заказ не активируется, сохраняются Warn и админское Telegram-сообщение с инструкцией проверить возврат или ручную активацию;
- параллельный и повторный CONFIRMED активируют только один раз; срок рассчитывается от актуального состояния подписки внутри транзакции, чтобы параллельные разные покупки не теряли продление;
- snapshot `orders.expires_at` равен `subscriptions.expires_at` после активации и не меняется при повторе;
- `CANCELED`, `CHARGEBACKED` и запрещённые переходы;
- repository guard отклоняет изменение `name`, `plan_id`, `duration_days`, `price_cents`, `currency` после появления Order и разрешает изменение только `is_active`;
- rollback при ошибке DB-setup sync;
- best-effort error/timeout внешнего SyncSubscription;
- сброс `reminders_sent=0`.

---

## 6. Webhook handler

### 6.1 Server dependencies

Добавить/подключить:

```go
type PaymentConfig struct {
    Enabled    bool
    MerchantID string
    Secret     string
}

func (s *Server) SetBot(bot interfaces.BotAPI)
func (s *Server) SetOrderService(svc *service.OrderService)
func (s *Server) SetPaymentConfig(cfg *PaymentConfig)
```

Если платежи выключены, `orderSvc`/`bot` не настроены или runtime ещё не выставил `paymentReady` после wiring реального Telegram-бота и `SyncService`, endpoint отвечает 503 до проверки credentials.

### 6.2 Обработка callback

1. Только POST; остальные методы → 405 и `Allow: POST`.
2. Если платежи выключены или зависимости отсутствуют → 503.
3. Проверить `X-MerchantId`/`X-Secret` constant-time сравнением. Пустые credentials невалидны → 401.
4. Ограничить body через `http.MaxBytesReader(..., 256*1024)` — 256 KiB.
5. Декодировать JSON через `UseNumber()` и отклонять trailing JSON/второй документ → 400.
6. Проверить UUID `id`, обязательные amount/currency/status и формат callback. `paymentMethod` не требовать: поле не входит в `required` callback-схемы Platega.
7. Для `CONFIRMED` вызвать `ConfirmPayment`.
8. Для `CANCELED`/`CHARGEBACKED` вызвать `CancelPaymentByProvider` с amount и currency для проверки целостности callback.
9. Для `PENDING` и неизвестного статуса записать Warn/audit-log и вернуть 200 без изменения заказа.
10. Неизвестный provider ID и late callback для expired возвращают 200; временные DB-ошибки и ошибка DB-setup sync возвращают 5xx, чтобы Platega повторила callback.
11. Ошибка внешнего SyncSubscription и ошибка Telegram после commit не откатывают оплату и не превращают callback в 5xx.

Успешный ответ:

```json
{"ok":true}
```

### 6.3 Success notification

Отправлять новое сообщение только при `Activated=true`. Повторный callback не должен отправлять его снова.

```go
func (o *OrderService) NotifyPaidUser(
    ctx context.Context,
    order *database.Order,
) (chatID int64, text string, err error)
```

Использовать те же helpers, что и «Моя подписка»:

- название продукта;
- формат даты;
- формат трафика;
- `Config.SubURL(sub.SubscriptionID)`.

Если Telegram ID некорректен, подписку всё равно активировать, сообщение не отправлять, записать Warn.

Ошибка `bot.Send` после commit — best-effort Warn; оплата и подписка не откатываются.

### 6.4 Webhook tests

Проверить:

- 405/503/401;
- body >256 KiB → 400;
- malformed JSON, trailing JSON, invalid UUID/amount → 400;
- все четыре официальных статуса;
- unknown status и unknown provider ID → 200 + Warn;
- late expired callback → 200 без активации и админское уведомление с order/user/payment details;
- amount/currency mismatch → 400;
- один success-message при параллельных CONFIRMED;
- ошибка Telegram после commit → 200;
- timeout внешней VPN-синхронизации → 200;
- DB-setup sync failure → 5xx и pending order.

---

## 7. Telegram UI

### 7.1 Главное меню

Источником payment flag является `Config.PaymentEnabled`. В текущем UI кнопка оплаты отображается только у пользователя с существующей подпиской и при включённых платежах; пользователь без подписки сначала получает бесплатную подписку через обычный flow.

```go
func (kb *KeyboardBuilder) MainMenu(
    hasSubscription bool,
    paymentEnabled bool,
) tgbotapi.InlineKeyboardMarkup
```

При `paymentEnabled=true` и `hasSubscription=true` добавить кнопку `💎 Купить Premium` с callback `buy_premium_list`. При false или без подписки кнопки нет.

### 7.2 Product list and payment confirmation

```go
func (kb *KeyboardBuilder) BuyProductList(
    products []database.Product,
) tgbotapi.InlineKeyboardMarkup

func (kb *KeyboardBuilder) BuyProductConfirm(
    product *database.Product,
    paymentURL string,
) tgbotapi.InlineKeyboardMarkup
```

- список содержит только активные платные продукты активных планов;
- сортировка: `price_cents ASC`, затем `id ASC`;
- callback продукта: `buy_product_{id}`;
- Back списка: `back_to_start`;
- URL-кнопка содержит payment URL;
- Back confirmation: `buy_premium_list`;
- бесплатные продукты не отображаются.

### 7.3 Callback names

Использовать `switch {}`:

```go
case data == "buy_premium_list":
    handleBuyPremiumList(...)
case strings.HasPrefix(data, "buy_product_"):
    handleBuyProduct(...)
```

Текущий контракт использует `buy_premium_list`, `handleBuyPremiumList`, `handleBuyProduct`; новые aliases не добавлять.

### 7.4 Handlers

```go
func (sh *SubscriptionHandler) handleBuyPremiumList(
    ctx context.Context,
    chatID int64,
    username string,
    messageID int,
) error

func (sh *SubscriptionHandler) handleBuyProduct(
    ctx context.Context,
    chatID int64,
    username string,
    messageID int,
    productID uint,
) error
```

`handleBuyPremiumList` показывает список активных продуктов либо понятную ошибку и Back.

`handleBuyProduct` повторно загружает Product из БД, проверяет активность/цену/план, вызывает RequestPayment и показывает ошибку пользователю, а не просто возвращает её.

### 7.5 UI tests

- при PAYMENT_ENABLED=false кнопки нет;
- при true кнопка есть у пользователя с подпиской, а у пользователя без подписки отсутствует;
- `RequestPayment` создаёт подписку при необходимости;
- старые бесплатные callbacks отсутствуют;
- invalid/fake product callback не запускает оплату;
- Back-navigation не создаёт дубликаты сообщений;
- ErrPaymentDisabled показывает `Платежи временно недоступны`.

---

## 8. Wiring

Порядок:

```text
config → database → SubscriptionService → SyncService → Platega client → OrderService → Handler/Web setters → web server → Telegram bot → workers
```

`OrderService` создаётся до запуска web server, не внутри `startBackgroundWorkers`. `botUsername` сначала пустой, затем устанавливается после `initBot/getMe`. Webhook не считается готовым сразу после создания Server: `SetPaymentReady(true)` вызывается только после wiring реального Telegram-бота и `SyncService`; до этого callback отвечает 503.

При `PAYMENT_ENABLED=false` provider равен nil, UI скрывает кнопку, callback отвечает 503. До полной инициализации реального Telegram-бота и `SyncService` callback также отвечает 503.

---

## 9. Webhook deployment and retry

Production callback URL:

```text
https://<public-domain>/payment/callback
```

Нужны HTTPS, публичный домен/IP и сертификат доверенного CA. localhost, private IP и self-signed certificate не использовать.

Для development использовать ngrok/cloudflared.

По документации Platega запрос callback отменяется после 60 секунд без успешного ответа; затем возможны повторы. Поэтому DB-обработка выполняется синхронно, а временные DB-ошибки отвечают 5xx. Detached goroutine после HTTP 200 запрещена.

---

## 10. Документация и cleanup

Обновить README и Serena memory с:

- env-переменными;
- callback URL;
- статусами и кодами ответа;
- сроком ссылки и повторным использованием;
- chargeback/manual-review поведением;
- state machine Order.

После cleanup поиск в исходном коде, конфигурации и тестах (документацию не учитывать) не должен находить:

```text
buy_premium_230
freeUpgradeLabel
getFreeUpgradeLabel
upgrade_premium
confirm_upgrade_premium
MainMenuBtnProductID
MAIN_MENU_BTN_PRODUCT
ActivateProduct
RenewSubscription
```

---

## 11. Acceptance criteria

1. `PAYMENT_ENABLED=false`: приложение стартует без PLATEGA credentials, кнопок нет, callback отвечает 503.
2. До инициализации реального Telegram-бота и `SyncService` callback отвечает 503; после `SetPaymentReady(true)` обработка разрешается.
2. `PAYMENT_ENABLED=true` без credentials: понятная ошибка config validation.
3. При включённых платежах кнопка оплаты отображается у пользователя с подпиской; пользователь без подписки сначала проходит обычный flow создания подписки.
4. Покупка без подписки создаёт подписку до Order.
5. Список содержит только активные платные продукты активных планов и сортируется по цене/ID.
6. Создаётся pending Order и транзакция без `paymentMethod`.
7. Поддерживаются `url` и `redirect`; `expiresIn` сохраняется как абсолютный `payment_expires_at`.
8. Действующая ссылка возвращается повторно без нового Order и вызова Platega.
9. После истечения pending-заказ становится `expired`, старые provider ID/URL не перезаписываются, новая попытка создаёт новый Order.
10. 400/401 позволяют повторить создание на том же pending-order; timeout/5xx/неполный ответ устанавливают `payment_creation_uncertain` и блокируют автоматический retry.
11. Partial unique index предотвращает два pending-заказа; конфликт перечитывает существующий Order.
12. Неизвестный provider ID и late callback для expired дают 200, Warn/audit-log и не активируют подписку.
13. Только валидный `CONFIRMED` переводит Order `pending → paid` через CAS.
14. Параллельные/повторные CONFIRMED активируют один раз и отправляют одно сообщение.
15. `orders.expires_at` — snapshot срока подписки; повторный CONFIRMED его не меняет.
16. Success-сообщение использует ту же presentation-логику, что «Моя подписка».
17. DB-setup sync выполняется в той же транзакции; ошибка откатывает Order в pending и callback отвечает 5xx.
18. Внешняя VPN-синхронизация ограничена timeout ≤20 секунд; её ошибка не отменяет оплату и отвечает 200.
19. Ошибка Telegram после commit не откатывает оплату и отвечает 200.
20. Webhook обрабатывает только официальные статусы `PENDING`, `CANCELED`, `CONFIRMED`, `CHARGEBACKED`, перечисленные в официальных схемах Platega; `SUCCESS`/`PAID` не поддерживаются.
21. Попытка изменить поля тарифа `name`, `plan_id`, `duration_days`, `price_cents`, `currency` после появления Order отклоняется repository guard; изменение `is_active` разрешено.
22. Body callback ограничен 256 KiB.
23. Legacy free upgrade flow и перечисленные старые символы полностью удалены.
