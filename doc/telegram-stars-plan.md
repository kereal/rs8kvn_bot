# Telegram Stars: план интеграции

Дата: 2026-11-18. Ветка: `feature/telegram-stars` от `dev`. Статус: план на согласование, код не реализован.

## 1. Решение

**Добавить Telegram Stars рядом с Platega. Один активный провайдер через конфигурацию.**

| Вопрос | Решение | Почему |
|---|---|---|
| Новый способ | Telegram Stars | Цифровые товары/услуги внутри Telegram должны оплачиваться Stars |
| Существующий способ | Platega сохраняется | Живой flow, нужна совместимость и rollback |
| Третий агрегатор | Нет | Нет запроса; добавляет комплаенс и поддержку |
| Одновременный выбор Platega/Stars в одном Telegram UI | Нет в этой задаче | Отдельное продуктовое/юридическое решение |
| Изменение схемы БД | **Не делается** | Вся интеграция ложится в существующие колонки |

Официальная база: см. раздел «Официальные ссылки API Telegram» в конце документа.

## 2. Non-goals

- Не менять текущее поведение Platega, кроме provider-neutral очистки типов.
- Не делать одновременный выбор Platega/Stars пользователем внутри Telegram.
- Не конвертировать рубли в Stars автоматически.
- Не обходить ограничение single-use invoice через форварды.
- Не запускать `SyncSubscription` внутри pre-checkout.
- Не ослаблять транзакционность `ConfirmOrderPaidCAS`.
- Не добавлять новый HTTP-эндпоинт для Stars: Stars приходят через Telegram updates.
- **Не добавлять миграцию схемы БД.**
- **Не менять пользовательский интерфейс бота, кроме неизбежных адаптаций** (см. раздел 5.5): текущие кнопки, тексты и flow сохраняются; изменения — только там, где Stars физически не работает как Platega (нет URL-кнопки, валюта XTR).

## 3. Целевая архитектура

Ядро заказа остаётся в `internal/service/order.go`. Провайдер — адаптер внешнего протокола.

Ядро: pending order, expiry, settlement grace, atomic activation, идемпотентность, audit, уведомления.
Провайдер: сумма/валидация, создание checkout, разбор входящего события, refund.

Transport не объединяется искусственно:

- Platega: HTTP `POST /payment/callback` в `internal/web`.
- Stars: `pre_checkout_query` и `message.successful_payment` в `internal/bot`.

Источник истины для Stars — `successful_payment`.

## 4. Контракты

### 4.1 Новый пакет `internal/service/payment`

```go
package payment

type ProviderID string

const (
    ProviderPlatega       ProviderID = "platega"
    ProviderTelegramStars ProviderID = "telegram_stars"
)

type Currency string

const (
    CurrencyRUB Currency = "RUB"
    CurrencyXTR Currency = "XTR"
)

// MinorUnits — сумма в минимальной единице валюты:
// RUB — копейки; XTR — один целый Star.
type Amount struct {
    MinorUnits int64
    Currency   Currency
}

type Product struct {
    ID              uint
    Name            string
    PriceMinorUnits int64
    Currency        Currency
}

type CheckoutRequest struct {
    OrderID       uint
    TelegramID    int64
    Username      string
    Product       Product
    Amount        Amount
    InvoiceTTL    time.Duration
    ReturnBaseURL string
}

type CheckoutKind string

const (
    CheckoutHTTPRedirect    CheckoutKind = "http_redirect"
    CheckoutTelegramInvoice CheckoutKind = "telegram_invoice"
)

type Checkout struct {
    Kind      CheckoutKind
    URL       string
    MessageID int
    ExpiresAt time.Time
}

type Receipt struct {
    ProviderID        ProviderID
    ProviderPaymentID string
    Payload           string
    Amount            Amount
}

type RefundRequest struct {
    TelegramID        int64
    ProviderPaymentID string
    Amount            Amount
}

type Provider interface {
    ID() ProviderID
    Quote(product Product) (Amount, error)
    ValidateAmount(expected, actual int64) (feeMinorUnits int64, err error)
    CreateCheckout(ctx context.Context, req CheckoutRequest) (Checkout, error)
}

// Capability, а не обязательный no-op.
type Reconciler interface {
    Reconcile(ctx context.Context, orderID uint, providerPaymentID string) error
    ProviderFee(ctx context.Context, providerPaymentID string) (int64, error)
}

// Capability, а не обязательный no-op.
type Refunder interface {
    Refund(ctx context.Context, req RefundRequest) error
}
```

- `Quote`: Platega возвращает `product.PriceMinorUnits/Currency`; Stars — ту же пару, но интерпретирует её по активной конфигурации товара (см. раздел 5).
- Platega `ValidateAmount`: callback amount >= expected, разница — fee (текущая семантика).
- Stars `ValidateAmount`: точное равенство.
- `Reconciler` реализует только Platega.
- `Refunder` реализует Stars.

### 4.2 OrderService

```go
func (o *OrderService) RequestPayment(ctx context.Context, req payment.CheckoutRequest) (*PaymentInfo, error)
func (o *OrderService) ConfirmPayment(ctx context.Context, r payment.Receipt) (*PaymentConfirmation, error)
func (o *OrderService) CancelPaymentByProvider(ctx context.Context, provider payment.ProviderID, providerPaymentID string, outcome string) error
```

`RequestPayment` для любого провайдера:
1. загрузить product/subscription/plan.
2. получить `Amount` через `provider.Quote(product)`.
3. найти pending order по `subscription_id + product_id` **без фильтра provider**.
4. если pending order с другим провайдером жив — вернуть ошибку (не создавать новый, дать истечь).
5. создать pending order с текущим provider.
6. `MarkPaymentCreationUncertain`.
7. вызвать `provider.CreateCheckout`.
8. сохранить provider details.
9. вернуть provider-neutral `PaymentInfo`.

### 4.3 Цены товаров (без миграции)

Существующие колонки: `price_cents INTEGER`, `currency CHAR(3) DEFAULT 'RUB'`.

Для переключения на Stars оператор выполняет SQL:

```sql
UPDATE products SET price_cents = <stars>, currency = 'XTR' WHERE id = <id>;
```

Для возврата к Platega — обратное:

```sql
UPDATE products SET price_cents = <рубли>, currency = 'RUB' WHERE id = <id>;
```

Один товар хранит цену только для одного активного провайдера. Оператор записывает RUB-цены перед переключением. При едином активном провайдере это безопасно.

При переключении обратно на Platega цены в рублях восстанавливаются из записи оператора (см. раздел 9 «Rollout и rollback»).

В Go `Product.PriceMinorUnits` (существующее) — без переименования колонки.

### 4.4 Pending uniqueness без изменения индекса

Текущий partial unique index: `WHERE status='pending' AND payment_provider='platega'`.

Проблема: Stars pending orders индексом не защищены.

Решение без миграции индекса:

1. `FindOrCreatePendingPaymentOrder` — `SELECT` pending без фильтра provider **внутри транзакции**, начинающейся с `BEGIN IMMEDIATE` (`sql.TxOptions{Isolation: sql.LevelSerializable}`). SQLite с WAL при `BEGIN IMMEDIATE` сериализует параллельные покупки; второй конкурентный `SELECT` увидит pending и откажет.

2. Если pending найден с другим провайдером — возврат ошибки «оплата уже в процессе», без создания нового.

3. SQLite-сериализация (MaxOpenConns=4, WAL, busy timeout) гарантирует отсутствие race.

Примечание: без `BEGIN IMMEDIATE` SQLite вернёт `SQLITE_BUSY_SNAPSHOT` при конкурентной записи — это предотвращает дубли, но даёт ошибку. С `LevelSerializable` — чистая сериализация.

### 4.5 Refund без нового статуса заказа

`orders.status` имеет `CHECK (status IN ('pending','paid','expired','canceled'))` (миграция 017/034). Добавление `refunded` потребовало бы перестройки таблицы.

Решение: Stars refund использует `order.status='canceled'`. `audit_log.event_type` пишется как `payment_refunded` (на `payment_refunded` нет CHECK-ограничения в `audit_log`). История восстанавливается по `audit_log.entity_id` + `event_type`.

Flow:
1. `refundStarPayment` → Telegram возвращает успех.
2. `paid → canceled` transition (существующий `CancelPaidOrderAndDowngradeCAS`).
3. `payment_refunded` в `audit_log` (существующий конструктор, новая константа события).
4. Downgrade при отсутствии других `paid` заказов.

Недостаток: в таблице `orders` refunded и canceled неотличимы. Преимущество: `audit_log` полностью восстанавливает историю.

## 5. Telegram Stars flow

### 5.1 Создание счёта

`sendInvoice` через `tgbotapi.InvoiceConfig`:

| Поле | Значение |
|---|---|
| `ChatID` | telegram user |
| `Title` | название продукта |
| `Description` | краткое описание подписки |
| `Payload` | `order:<order_id>` (существующий формат, используется в ConfirmPayment fallback) |
| `ProviderToken` | пустая строка |
| `Currency` | `XTR` |
| `Prices` | ровно один `LabeledPrice{Amount: product.PriceMinorUnits}` |
| `StartParameter` | `stars-<order_id>` |
| `IsFlexible` | `false` |

Результат: `Checkout{Kind: CheckoutTelegramInvoice, MessageID: <message_id>}`.
Кнопка-ссылка не отправляется: invoice приходит отдельным сообщением.

**Back-кнопка — известный критический паттерн навигации проекта.** Invoice — это «контент в отдельном сообщении», как QR-код. Правила:

- при покупке: карточка тарифа редактируется в «Счёт отправлен ниже», invoice отправляется **новым сообщением**;
- «⬅️ Назад» на карточке: удаляется **только** сообщение invoice (его `messageID` приходит в callback), карточка не пересоздаётся — повторная отправка карточки создаёт дубликат (известный баг, guard `TestNavigation_OpenAndBack`);
- регресс-тест: аналог `TestNavigation_OpenAndBack` для сценария invoice+Back — в этапе 6.

### 5.2 Pre-checkout

Новый inbound `pre_checkout_query`:
1. обработка до обычной ветки non-command message.
2. Валидация: provider=`telegram_stars`, payload парсится, order pending/expired, currency=`XTR`, `total_amount` точно равен `order.amount`, `query.From.ID` совпадает с владельцем.
3. `answerPreCheckoutQuery` не позднее 10 секунд.
4. Не вызывать node/XUI API и `SyncSubscription`.

При отказе — короткая причина.

### 5.3 Successful payment

Новый inbound `update.Message.SuccessfulPayment`:
1. валидация provider/currency/amount/payload/owner.
2. `TelegramPaymentChargeID` = `provider_payment_id`.
3. Атомарная привязка charge ID + `paid` в одной транзакции (аналог `ConfirmOrderPaidCAS`, но без CAS по provider_payment_id — он ещё NULL → сразу UPDATE WHERE id= AND status IN ('pending','expired') AND provider_payment_id IS NULL).
4. Существующий flow далее: subscription expiry, plan, audit, уведомления, sync.
5. **Важно**: для Stars expired→paid без ограничения grace. `successful_payment` — авторитетный источник; деньги уже списаны, отказ — потеря денег без активации. Локальный expiry — UX-подсказка, не финансовый контроль.

Идемпотентность: повторное событие → `RowsAffected==0` → без повторного продления.

### 5.4 Refund

`refundStarPayment(user_id, telegram_payment_charge_id)`.

Через `tgbotapi.BotAPI.MakeRequest("refundStarPayment", params)`.

Порядок:
1. внешний Telegram refund.
2. после успеха — `CancelPaidOrderAndDowngradeCAS`.
3. `AuditPaymentRefunded` в той же транзакции.
4. после коммита — уведомления.

Если Telegram прошёл, а БД не записала — операционная ошибка, alert, ручная сверка по charge ID.

### 5.5 UI: минимальные изменения (принцип)

Пользовательский интерфейс бота сохраняется. Изменения — только там, где Stars физически не работает как Platega:

| Элемент | Platega (сейчас) | Stars (новое) | Обязательность |
|---|---|---|---|
| Список тарифов (`BuyProductList`) | `Название — 199₽` | `Название — 199XTR` (без formatter — сломано) | Обязательно: валюта из товара |
| Кнопка оплаты (`BuyProductConfirm`) | `💳 Оплатить 199₽` (URL) | Не существует: `sendInvoice` сам открывает invoice | Обязательно: у Stars нет URL |
| Текст карточки покупки | `После оплаты тариф активируется автоматически... _Платёжная система может дополнительно взимать комиссию._` | Без строки про комиссию | Обязательно: у Stars нет комиссии пользователю |
| Текст ошибки `MsgPaymentInProgress` | `...Откройте ссылку, отправленную ранее...` | `...Нажмите на счёт, отправленный ранее...` | Обязательно: у Stars нет ссылки |
| Возврат после invoice (Back) | Не применим | Удаляется только сообщение invoice, карточка не пересоздаётся | Обязательно: известный паттерн дублирования |
| Всё остальное UI | Текущее | Текущее, без изменений | — |

Минимум изменений: 5 точек выше. Остальной UI не трогаем.

## 6. Ожидаемые изменения по файлам

| Файл/пакет | Изменение |
|---|---|
| `internal/service/payment/platega/` | реализация `payment.Provider`/`Reconciler` |
| `internal/service/order.go` | provider-neutral `RequestPayment`/`ConfirmPayment`, убрать Platega-типы |
| `internal/interfaces/interfaces.go` | `OrderService` без uuid.UUID и Platega-типов |
| `internal/database/models.go` | Go-поля в терминах minor units; `Product.Currency` используется для XTR |
| `internal/database/orders.go` | pending lookup без provider-фильтра, string provider ID, атомарный attach |
| `internal/config/config.go` | `PAYMENT_PROVIDER = platega \| telegram_stars` |
| `internal/bot/handler.go` | dispatch pre-checkout и successful_payment; `AllowedUpdates += pre_checkout_query` |
| `internal/bot/subscription_handler.go` | provider-aware buy flow |
| `internal/bot/keyboard_builder.go` | URL-кнопка только для Platega |
| `internal/utils/format.go` | currency-aware `FormatAmount(payment.Amount)` |
| `cmd/bot/main.go` | wiring Stars adapter, `AllowedUpdates` |
| `internal/web/web.go` | Platega callback остаётся, использует новый контракт |

Миграция БД: **не делается.**

## 7. Implementation plan

### Этап 1. Provider-neutral контракт

- создать `internal/service/payment/types.go`, `internal/service/payment/provider.go`.
- убрать Platega-типы из `OrderService`/`interfaces`.
- `uuid.UUID` → `string` на границах заказа.
- заменить hardcode `"platega"` константой `payment.ProviderPlatega`.

Проверка: `go build ./...`, `go test ./internal/service ./internal/database`.

### Этап 2. Репозиторий (без схемы)

- `FindPendingPaymentOrder` без фильтра по provider.
- `FindOrCreatePendingPaymentOrder` с `BEGIN IMMEDIATE` (`sql.LevelSerializable`).
- provider-agnostic pending lookup — отказ при чужом провайдере.
- `provider_payment_id` как string.
- Атомарный attach `provider_payment_id` + transition pending→paid в одном UPDATE.

Проверка: unit-тесты идемпотентности, settlement grace, concurrent purchases.

### Этап 3. Platega adapter

- адаптировать к общему контракту.
- сохранить callback amount >= order amount = fee.

Проверка: регрессия Platega.

### Этап 4. Stars adapter

- `Quote`, `ValidateAmount`, `CreateCheckout`.
- `sendInvoice` с `XTR`, пустым provider token, одним price.
- `Refund` через `tgbotapi.MakeRequest("refundStarPayment", ...)`.

Проверка: unit-тесты параметров invoice, суммы, payload.

### Этап 5. Bot inbound

- обработка `pre_checkout_query` и `message.successful_payment`.
- `AllowedUpdates += pre_checkout_query`.
- `answerPreCheckoutQuery` за <10 с.

Проверка: unit-тесты `HandleUpdate`, в т.ч. неверный пользователь/сумма.

### Этап 6. UI (минимальные изменения, см. раздел 5.5)

- Форматтер цены currency-aware: `FormatPriceCents` → поддержка `XTR` (5 точек из раздела 5.5).
- Platega — URL-кнопка (текущее поведение, без изменений).
- Stars — «Счёт отправлен ниже» + invoice отдельным сообщением.
- Back после invoice: удалить только сообщение invoice (паттерн дублирования).
- Текст `MsgPaymentInProgress` для Stars: без упоминания ссылки.

Проверка: bot-тесты + регресс-тест аналога `TestNavigation_OpenAndBack`.

### Этап 7. Refund

- Stars refund → `CancelPaidOrderAndDowngradeCAS`.
- `AuditPaymentRefunded` (константа + конструктор в `audit_log.go`, без миграции).
- `payment_refunded` пишется в `subscription`.

Проверка: unit-тесты refund, duplicate refund, Telegram failure, DB failure alert path.

### Этап 8. Документация

- Проверить раздел 9 «Rollout и rollback» этого плана актуальным для README.
- При необходимости — обновить README.md (секция платёжных способов).

## 8. Тесты

| Сценарий | Ожидание |
|---|---|
| Platega существующий flow | Без регрессии |
| Stars buy → invoice | pending order, invoice параметры корректные |
| Stars pre-checkout valid | approved за <10 c |
| Stars pre-checkout invalid amount/currency/user | rejected |
| Stars successful payment first | order paid, subscription renewed |
| Stars successful payment duplicate | идемпотентно |
| Concurrent Stars purchases same sub/product | второй отказ (BEGIN IMMEDIATE сериализация) |
| Provider switch при живом pending | новая попытка отклоняется до expiry |
| Stars expired→paid (поздний successful_payment) | активируется, grace не ограничивает |
| Stars refund | order canceled, audit payment_refunded, downgrade |

Финальные гейты:

```bash
gofmt -l .
go build ./...
go vet ./...
golangci-lint run ./...
go test ./...
go test -race ./...
```

## 9. Rollout и rollback

### Переключение на Stars

1. Развернуть код с `PAYMENT_PROVIDER=platega`. Platega flow без изменений.
2. Записать RUB-цены (по продукту: `id, price_cents, name`).
3. Для каждого продукта выполнить: `UPDATE products SET price_cents = <stars>, currency = 'XTR' WHERE id = <id>;`.
4. Переключить `PAYMENT_PROVIDER=telegram_stars`.
5. Дать старым Platega pending заказам истечь.
6. Smoke test: buy → invoice → pre-checkout → payment → renewal → refund.

### Rollback на Platega

1. `PAYMENT_PROVIDER=platega`.
2. Восстановить RUB-цены из записей (шаг 2).
3. Новые Stars invoices не создаются.
4. Истечь старые Stars pending.

## Официальные ссылки API Telegram

Факты, использованные в плане, и их первоисточники:

| Тема | Ссылка |
|---|---|
| Telegram Stars (ограничения digital goods/services) | https://core.telegram.org/bots/payments-stars |
| sendInvoice (XTR, пустой provider_token, один price) | https://core.telegram.org/bots/api/methods/sendinvoice |
| answerPreCheckoutQuery (лимит 10 секунд) | https://core.telegram.org/bots/api/methods/answerprecheckoutquery |
| SuccessfulPayment (источник истины) | https://core.telegram.org/bots/api/types/successfulpayment |
| refundStarPayment (возврат средств) | https://core.telegram.org/bots/api/methods/refundstarpayment |
