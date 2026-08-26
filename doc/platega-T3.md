# Техническое задание: интеграция платёжной системы Platega.io

Статус: согласовано с текущей реализацией


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

### 1.11 Поздний CONFIRMED и гонка с задержкой доставки webhook

Наблюдение (2026-08-11, прод): платёжная ссылка действует 30 минут (`expiresIn`), но доставка webhook Platega может опаздывать — например, webhook об истечении приходил на 34-й минуте (задержка ~4 минуты). Задержка применима ко всем статусам, включая `CONFIRMED`.

Как это влияет на реализацию:

- Локальный дедлайн `payment_expires_at = receivedAt + expiresIn` используется только для ленивой терминизации pending-заказа при новом `RequestPayment` (повторная покупка / новая ссылка). Фоновых воркеров, просрочивающих pending-заказы, нет, а активация по `CONFIRMED` дедлайн не проверяет — источник истины — webhook (см. §3.3).
- Поздний `CANCELED` (истечение) безвреден: заказ ещё `pending` → корректно переводится в `canceled`; заказ уже `expired` (запрошена новая ссылка) → CAS не срабатывает (no-op, Warn). Новый заказ имеет другой provider ID и не затрагивается.
- Реальная гонка — поздний `CONFIRMED` после терминизации:
  1. пользователь оплатил в валидном окне (например, на 29-й минуте), но webhook задержался и пришёл на 33-й;
  2. в окне 30–33 мин пользователь снова нажал «Купить» (посчитал ссылку умершей) — старый pending-заказ переведён в `expired`, выдана новая ссылка;
  3. `CONFIRMED` старого заказа попадает в ветку `late_confirmed_callback` (§5.4): подписка НЕ активируется автоматически, деньги взяты, админу уходит алерт «Late confirmed payment — verify payment and refund or activate manually» (покрыто тестами: `internal/service/order_test.go`, `internal/web/payment_test.go`).

Решение (согласовано, 2026-08-11): оставить как есть, без grace-периода (§1.10). Гонка редкая, «тихой» потери нет — каждое событие фиксируется в метрике `payment_issues_total{event="late_confirmed_callback"}` и админском Telegram-алерте с полным контекстом (order ID, сумма, валюта, provider ID) для ручного разбора. Повторная активация и двойное начисление исключены: поздний `CONFIRMED` никогда не переводит `expired → paid`.

Мониторинг: следить за ростом `payment_issues_total{event="late_confirmed_callback"}`. Если события станут регулярными (задержка провайдера стабильно больше ожидаемой), пересмотреть решение и ввести grace-период к локальному дедлайну (например, `receivedAt + expiresIn + 5 мин`), обновив этот документ.

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

-- The application database is guaranteed to contain no legacy payment rows when
-- this migration is applied, so the migration does not perform historical
-- deduplication. It only installs the current payment invariants.
CREATE UNIQUE INDEX idx_orders_provider_payment_unique
    ON orders(payment_provider, provider_payment_id)
    WHERE provider_payment_id IS NOT NULL AND provider_payment_id <> '';

CREATE UNIQUE INDEX idx_orders_pending_subscription_product_unique
    ON orders(subscription_id, product_id, payment_provider)
    WHERE status = 'pending' AND payment_provider = 'platega';
```

Исторические платёжные записи и дубли в базе отсутствуют, поэтому миграция не выполняет очистку данных. При конфликте partial unique index в конкурентном запросе сервис должен перечитать уже существующий pending-заказ и вернуть его, а не показывать инфраструктурную ошибку.

### 3.2 Новые поля Order

Добавить в `internal/database/models.go`:

```go
PaymentURL                string     `gorm:"column:payment_url"`
PaymentExpiresAt          *time.Time `gorm:"column:payment_expires_at"`
PaymentCreationUncertain  bool       `gorm:"not null;default:false;column:payment_creation_uncertain"`
```

`PaymentExpiresAt` хранится в UTC. `nil` означает, что ссылка ещё не создана или провайдер не вернул срок. Внутренние методы OrderService/репозитория принимают `uuid.UUID`, но DB-поле `orders.provider_payment_id` остаётся строковым как универсальный внешний идентификатор. Для Platega строка валидируется и преобразуется в UUID на границе; повреждённое сохранённое значение даёт контролируемую ошибку без panic.

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

Задержка доставки webhook (~до 4 минут) и гонка позднего `CONFIRMED` описаны в §1.11.

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
    product *Product,
    applyPlan ApplyPlanInTxFn,
) (bool, error)

func (s *Service) CancelOrderCAS(
    ctx context.Context,
    provider string,
    providerPaymentID uuid.UUID,
    fromStatuses []OrderStatus,
) (bool, error)

// CalculatePaymentExpiry — единый источник истины для расчёта срока подписки
// после оплаты. Используется внутри ConfirmOrderPaidCAS и тестами.
func CalculatePaymentExpiry(
    now time.Time,
    sub *Subscription,
    product *Product,
) time.Time
```

`SavePaymentDetails` — единственный путь записи provider ID, ссылки и `payment_expires_at`; он применяется только к pending-заказу. Legacy-методы `UpdateOrderStatus`, `UpdateOrderPaidStatus`, `UpdateOrderActivatedAt`, `UpdateOrderProviderPaymentID` удалены как мёртвый код (пошаговые переходы заменены атомарными CAS: `ConfirmOrderPaidCAS`, `CancelOrderCAS`).

`FindOrCreatePendingPaymentOrder` обязан:

1. найти pending-заказ по конкретным `subscription_id` и `product_id`;
2. первым делом проверить `payment_creation_uncertain`;
3. вернуть действующую ссылку без нового запроса к Platega;
4. перевести истёкший pending-заказ в `expired` и создать новый;
5. обработать конфликт partial unique index перечитыванием существующего заказа.

`ConfirmOrderPaidCAS` в одной DB-транзакции выполняет:

- CAS `pending → paid` с условием `WHERE status='pending'`;
- вычисление `newExpiry` **внутри транзакции** через `CalculatePaymentExpiry(activatedAt, sub, product)` от актуального состояния подписки (продление не теряется при параллельных покупках) и запись `paid_at`, `activated_at`, `expires_at`;
- обновление подписки (`subscriptions.expires_at = newExpiry`, `reminders_sent=0`);
- DB-setup sync через `applyPlan` в той же транзакции.

`newExpiry` в параметры не передаётся: предварительный расчёт в сервисе удалён как мёртвый код, а `expires_at` пишется в Order один раз из значения, пересчитанного внутри транзакции (двойная запись устранена). Ошибка `applyPlan` откатывает всю транзакцию. Дубликат хелпера в service-пакете удалён — единая реализация живёт в `database.CalculatePaymentExpiry`.

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

Обязательны `id`, `amount`, `currency`, `status`. В текущей реализации `id` должен быть UUID, как указано в официальной схеме Platega. `payload` и `paymentMethod` принимаются при наличии. Официальные страницы Platega противоречат друг другу по обязательности этих полей: endpoint описывает `payload`, а схема `CallbackPayload` — `paymentMethod`; до письменного уточнения провайдера webhook сохраняет обратную совместимость и не отклоняет отсутствие любого из них.

Для webhook принимаются только следующие значения, официально присутствующие в схемах Platega:

- `PENDING` — официальный статус общей модели транзакции; Warn и HTTP 200 без изменения заказа;
- `CANCELED` — статус webhook; отмена pending-заказа;
- `CONFIRMED` — статус webhook; подтверждение pending-заказа;
- `CHARGEBACKED` — официальный статус общей модели транзакции; перевод pending/paid в canceled. При переводе ранее оплаченного (`paid`) заказа подписка автоматически даунгрейдится до free-плана, если у неё нет другого оплаченного заказа; иначе доступ сохраняется для ручного разбора.

Любое неизвестное будущее значение статуса не изменяет заказ, записывается в Warn/audit-log и получает HTTP 200. `SUCCESS` и `PAID` не являются допустимыми aliases.

Сумму callback разбирать fixed-point parser-ом. Отрицательные, пустые и нечисловые значения отклонять. Лишние нулевые знаки после копеек допустимы (например, `52.5000000000000000` → `5250` копеек), но ненулевые знаки после второй дробной цифры отклонять.

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
2. Проверить точное совпадение валюты и отсутствие недоплаты в копейках. Platega может включать комиссию способа оплаты в callback-сумму, поэтому `callback_amount >= order.amount` принимается, а меньшая сумма → бизнес-ошибка и HTTP 400.
3. Загрузить канонический Product и Subscription по сохранённым ID заказа. Для расчёта использовать сохранённый ProductID и зафиксированные в Order сумму/валюту; изменение текущего каталога не должно менять уже созданную покупку.
4. Не выполнять предварительный расчёт `newExpiry` в сервисе: `ConfirmOrderPaidCAS` вычисляет срок внутри транзакции через `CalculatePaymentExpiry` от актуального состояния подписки.
5. Вызвать `ConfirmOrderPaidCAS` с `applyPlan`. После успешного CAS сервис зеркалит `sub.ExpiresAt` (заполняется CAS) в snapshot `order.ExpiresAt`; nil-guard защищает от panic, если CAS активировал подписку без установки `sub.ExpiresAt` (в этом случае `order.ExpiresAt` остаётся `nil`).
6. Если CAS не изменил строку:
   - paid → no-op;
   - canceled/expired → позднее подтверждение запрещено, Warn/audit-log.
7. После commit выполнить `SyncSubscription` с отдельным timeout ≤20 секунд. Ошибка только логируется Warn и не превращает callback в 5xx.
8. Только `Activated=true` имеет право вызвать success-уведомление:
   - пользователю — `NotifyPaidUser` (веб-слой отправляет сообщение с деталями подписки);
   - администратору — `notifyAdminPaid` (Markdown-сообщение в TelegramAdminID: тариф, сумма в читаемом виде, кликабельная ссылка на покупателя через `utils.FormatUserLink` — `t.me/username` или `tg://user?id=…`, Telegram ID, provider transaction ID, ID подписки/заказа, срок действия до). Заголовок различает покупку и продление: `🆕 Покупка подтверждена` для подписки без оплаченного состояния до активации (`PricePaidCents == 0` и `ProductID == nil` до CAS), `🔄 Продление подтверждено` для уже оплаченной. Отправка best-effort: при недоставке только Warn-лог, платёжный флоу не ломается.

Допустимые переходы:

```text
pending → paid
pending → canceled
pending → expired       только при новом RequestPayment после payment_expires_at
paid → paid              no-op
paid → canceled          automatically downgrades to free plan when no other paid order exists
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
- перед отменой проверить валюту и отсутствие недоплаты относительно Order; сумма callback может включать комиссию Platega (`callback_amount >= order.amount`), а меньшая сумма → HTTP 400 без изменения заказа;
- `CANCELED` переводит только `pending → canceled`;
- `CHARGEBACKED` может перевести `pending` или `paid` в `canceled`. Перевод `paid → canceled` автоматически даунгрейдит подписку до free-плана (best-effort, после снятия per-order payment lock, с отдельным timeout ≤20 секунд), если у подписки нет другого `paid`-заказа; даунгрейд переводит premium-ноды в `pending_remove` и запускает `SyncSubscription`, чтобы клиенты физически удалялись с панелей; при наличии другого оплаченного заказа доступ сохраняется и событие остаётся на ручной разбор. Перевод `pending → canceled` (деньги не списывались) даунгрейд не выполняет;
- второй возвращаемый bool — `wasPaid`: `true` только когда `CHARGEBACKED` переводит ранее оплаченный (`paid`) заказ. Чарджбек на ещё `pending` заказе (деньги не списывались) возвращает `wasPaid=false`. Флаг вычисляется из статуса заказа **до** перехода, а не из итогового `canceled`;
- повторный callback — no-op;
- `SUCCESS`, `PAID` и неизвестные статусы не обрабатывать.

`payload` и `paymentMethod` callback при наличии записываются в структурированный audit-log webhook-слоя; менять сигнатуры сервисных методов только ради этих полей не требуется.

Все проблемы во взаимодействии с платёжным провайдером, включая ошибки загрузки/валидации ответа, неопределённый результат запроса, невозможность сохранить данные, неизвестный/повреждённый provider ID, mismatch суммы/валюты, поздний callback, ошибки отмены и ошибки DB-setup sync, должны записываться в Warn/error log и отправлять администратору Telegram-сообщение с доступными полями: событие, причина, Order ID, Telegram ID, Product ID, Subscription ID, товар, сумма, валюта, provider ID, URL, callback status, payload/paymentMethod и рекомендуемое действие. Если Telegram-сообщение админу не отправилось, ошибка также записывается в лог.

### 5.6 Обязательные тесты OrderService

- успешный RequestPayment;
- disabled/invalid product/invalid Telegram ID;
- повтор после 400/401 без второго pending-заказа;
- действующая ссылка возвращается повторно;
- истёкшая ссылка переводит заказ в expired и создаёт новый Order;
- timeout/5xx устанавливает `payment_creation_uncertain`, блокирует автоматический retry и отправляет админу Telegram-сообщение с order ID, Telegram ID, товаром, суммой, валютой, причиной и доступными данными провайдера;
- параллельный конфликт partial unique index перечитывает существующий pending-заказ;
- callback amount/currency: комиссия провайдера и лишние нулевые знаки принимаются, недоплата отклоняется;
- неизвестный provider ID;
- late CONFIRMED для expired: заказ не активируется, сохраняются Warn и админское Telegram-сообщение с инструкцией проверить возврат или ручную активацию;
- параллельный и повторный CONFIRMED активируют только один раз; срок рассчитывается от актуального состояния подписки внутри транзакции, чтобы параллельные разные покупки не теряли продление;
- snapshot `orders.expires_at` равен `subscriptions.expires_at` после активации и не меняется при повторе;
- `CANCELED`, `CHARGEBACKED` и запрещённые переходы; `wasPaid=true` только для `paid`-заказа по `CHARGEBACKED`, а чарджбек на `pending` даёт `wasPaid=false`; чарджбек на `paid`-заказе даунгрейдит подписку до free-плана, при наличии другого `paid`-заказа доступ сохраняется;
- CAS, активирующий подписку без установки `sub.ExpiresAt`, не вызывает panic и оставляет snapshot `orders.expires_at` равным `nil` (nil-guard);
- repository guard отклоняет изменение `name`, `plan_id`, `duration_days`, `price_cents`, `currency` после появления Order и разрешает изменение только `is_active`; все административные изменения Product проходят только через этот метод;
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
6. Проверить UUID `id`, обязательные amount/currency/status и формат callback. `paymentMethod` и `payload` пока не делать обязательными из-за противоречивых официальных схем Platega; принимать их при наличии и сохранять в структурированном audit-log.
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
- CHARGEBACKED на paid-заказе автоматически даунгрейдит подписку до free-плана (при отсутствии другого paid-заказа);
- unknown status и unknown provider ID → 200 + Warn;
- late expired callback → 200 без активации и админское уведомление с order/user/payment details;
- успешная оплата (Activated=true) → ровно одно админское уведомление с тарифом, суммой и ссылкой на покупателя; повторные CONFIRMED не дублируют его;
- amount/currency mismatch: недоплата и неверная валюта → 400, сумма с комиссией провайдера → успешная активация;
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

При `paymentEnabled=true` и `hasSubscription=true` добавить кнопку `💎 Купить Premium` с callback `buy_premium_list`. Для активной подписки на канонический `free`-план над клавиатурой также показывать короткий teaser: `💎 Premium — безлимитный трафик и больше серверов`. При false или без подписки кнопки нет.

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
- бесплатные продукты не отображаются;
- перед оплатой сообщение повторяет преимущества Premium: безлимитный трафик, больше серверов и вариантов подключения, дополнительные/экспериментальные функции и приоритетная поддержка;
- экран списка тарифов использует Markdown-разметку для заголовка преимуществ.

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

После подтверждённой оплаты webhook отправляет пользователю Markdown-сообщение с приветствием Premium, преимуществами, данными подписки, сроком действия и ссылкой. Повторные callback не отправляют это сообщение повторно.

### 7.5 UI tests

- при PAYMENT_ENABLED=false кнопки нет;
- при true кнопка есть у пользователя с подпиской, а у пользователя без подписки отсутствует;
- `RequestPayment` создаёт подписку при необходимости;
- старые бесплатные callbacks отсутствуют;
- invalid/fake product callback не запускает оплату;
- продукт с неактивным планом не запускает оплату и показывает `Тариф недоступен`;
- Back-navigation не создаёт дубликаты сообщений;
- ErrPaymentDisabled показывает `Платежи временно недоступны`;
- список тарифов показывает Premium benefits с `ParseMode=Markdown` и без двоеточий в заголовке/финальной строке;
- Free-подписка с включёнными платежами показывает Premium teaser в главном меню, а платный план — нет;
- успешная оплата отправляет пользователю Markdown-приветствие Premium;
- expiry reminder содержит преимущества Premium и кнопку `💎 Продлить Premium` с callback `buy_premium_list`.

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
- chargeback/manual-review поведением: автодаунгрейд до free при чарджбеке на paid-заказе (если нет другого paid-заказа), сохранение доступа при наличии другого оплаченного заказа. При чарджбеке на оплаченном заказе (`WasPaid=true`) админ получает единственное Markdown-сообщение `notifyAdminChargeback` (тариф, сумма, ссылка на покупателя, статус доступа: «понижен до бесплатного» / «сохранён»); инфраструктурные сбои по-прежнему уходят через `NotifyPaymentIssue` → `notifyAdmin`;
- state machine Order.

После cleanup поиск в исходном коде, конфигурации и тестах (документацию не учитывать) не должен находить:

```text
buy_premium_230
freeUpgradeLabel
getFreeUpgradeLabel
upgrade_premium
confirm_upgrade_premium
ActivateProduct
RenewSubscription
```

---

## 11. Acceptance criteria

1. `PAYMENT_ENABLED=false`: приложение стартует без PLATEGA credentials, кнопок нет, callback отвечает 503.
2. До инициализации реального Telegram-бота и `SyncService` callback отвечает 503; после `SetPaymentReady(true)` обработка разрешается.
3. `PAYMENT_ENABLED=true` без credentials: понятная ошибка config validation.
4. При включённых платежах кнопка оплаты отображается у пользователя с подпиской; пользователь без подписки сначала проходит обычный flow создания подписки.
5. Покупка без подписки создаёт подписку до Order.
6. Список содержит только активные платные продукты активных планов и сортируется по цене/ID.
7. Создаётся pending Order и транзакция без `paymentMethod`.
8. Поддерживаются `url` и `redirect`; `expiresIn` сохраняется как абсолютный `payment_expires_at`.
9. Действующая ссылка возвращается повторно без нового Order и вызова Platega.
10. После истечения pending-заказ становится `expired`, старые provider ID/URL не перезаписываются, новая попытка создаёт новый Order.
11. 400/401 позволяют повторить создание на том же pending-order; timeout/5xx/неполный ответ устанавливают `payment_creation_uncertain` и блокируют автоматический retry.
12. Partial unique index предотвращает два pending-заказа; конфликт перечитывает существующий Order.
13. Неизвестный provider ID и late callback для expired дают 200, Warn/audit-log и не активируют подписку.
14. Только валидный `CONFIRMED` переводит Order `pending → paid` через CAS.
15. Параллельные/повторные CONFIRMED активируют один раз и отправляют одно сообщение.
16. `orders.expires_at` — snapshot срока подписки; повторный CONFIRMED его не меняет.
17. Success-сообщение использует ту же presentation-логику, что «Моя подписка».
18. DB-setup sync выполняется в той же транзакции; ошибка откатывает Order в pending и callback отвечает 5xx.
19. Внешняя VPN-синхронизация ограничена timeout ≤20 секунд; её ошибка не отменяет оплату и отвечает 200.
20. Ошибка Telegram после commit не откатывает оплату и отвечает 200.
21. Webhook обрабатывает только официальные статусы `PENDING`, `CANCELED`, `CONFIRMED`, `CHARGEBACKED`, перечисленные в официальных схемах Platega; `SUCCESS`/`PAID` не поддерживаются.
22. Попытка изменить поля тарифа `name`, `plan_id`, `duration_days`, `price_cents`, `currency` после появления Order отклоняется repository guard; изменение `is_active` разрешено.
23. Body callback ограничен 256 KiB.
24. Legacy free upgrade flow и перечисленные старые символы полностью удалены.
25. Migration 031 выполняется на SQLite через реальный migration runner; база не содержит legacy payment rows, а `down` удаляет новые поля/индексы и восстанавливает исходный provider index миграции 017 (`provider_payment_id IS NOT NULL`).
26. Guarded Product update имеет тесты для всех immutable fields и разрешённого изменения `is_active`.
27. Успешное представление оплаты и экран «Моя подписка» используют один formatter/helper.
28. Все payment/provider/integration errors отправляют администратору Telegram-сообщение с доступным контекстом; failure самой отправки также логируется.
29. `CHARGEBACKED` на ранее оплаченном (`paid`) заказе автоматически даунгрейдит подписку до free-плана при отсутствии других `paid`-заказов; при наличии другого оплаченного заказа доступ сохраняется и событие обрабатывается вручную.
