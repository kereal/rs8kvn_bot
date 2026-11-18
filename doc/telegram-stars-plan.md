# Telegram Stars: план интеграции

Дата: 2026-11-18. Ветка: `feature/telegram-stars` от `dev`. Статус: план на согласование, код не реализован.

## 1. Решение

**Добавить Telegram Stars рядом с Platega, не добавлять третий агрегатор и не заменять Platega в этом же шаге.**

| Вопрос | Решение | Почему |
|---|---|---|
| Новый способ | Telegram Stars | Цифровые товары/услуги внутри Telegram должны оплачиваться Stars |
| Существующий способ | Platega сохраняется | Живой flow, нужна совместимость и rollback |
| Третий агрегатор | Нет | Нет запроса; добавляет комплаенс, миграции и поддержку |
| Пользователь выбирает Stars/Platega в одном Telegram UI | Нет в этой задаче | Требует отдельного продукта/юридического решения и защиты от двойных оплат |
| Способ переключения | Один активный провайдер через конфигурацию | Безопасно, просто откатывается |

Официальная база: [Telegram Stars](https://core.telegram.org/bots/payments-stars), [sendInvoice](https://core.telegram.org/bots/api/methods/sendinvoice), [answerPreCheckoutQuery](https://core.telegram.org/bots/api/methods/answerprecheckoutquery), [SuccessfulPayment](https://core.telegram.org/bots/api/types/successfulpayment), [refundStarPayment](https://core.telegram.org/bots/api/methods/refundstarpayment).

## 2. Non-goals

- Не менять текущее поведение Platega, кроме provider-neutral очистки типов.
- Не делать одновременный выбор Platega/Stars пользователем внутри Telegram.
- Не конвертировать рубли в Stars автоматически.
- Не обходить ограничение single-use invoice через форварды или второй `start_parameter`.
- Не запускать `SyncSubscription` внутри pre-checkout.
- Не ослаблять транзакционность `ConfirmOrderPaidCAS`.
- Не добавлять новый внешний HTTP-эндпоинт для Stars: Stars приходят через Telegram updates.
- Не менять `AGENTS.md` и память в этом PR без отдельного завершающего шага.

## 3. Целевая архитектура

Ядро заказа остаётся в `internal/service/order.go`. Провайдер — только адаптер внешнего протокола.

Ядро делает:

1. загрузку product/subscription/plan;
2. создание и переиспользование pending order;
3. expiry и settlement grace;
4. atomic activation;
5. идемпотентность;
6. audit и уведомления.

Провайдер делает:

1. выбор и валидацию суммы;
2. создание checkout;
3. provider-специфичный разбор входящего события;
4. provider-специфичный refund.

Transport не объединяется искусственно:

- Platega: HTTP `POST /payment/callback` в `internal/web`.
- Stars: `pre_checkout_query` и `message.successful_payment` в `internal/bot`.

Для Stars источник истины — `successful_payment`.

## 4. Контракты

### 4.1 Новый пакет `internal/service/payment`

Пакет не импортирует `database` и не знает про UUID/HTTP Platega.

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
    PriceMinorUnits int64 // текущий RUB price_cents
    Currency        Currency
    StarsPrice      int64 // whole Stars
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
    Kind         CheckoutKind
    URL          string
    MessageID    int
    ExpiresAt    time.Time
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

- Platega `ValidateAmount` сохраняет текущую семантику: callback amount может быть больше, разница — fee.
- Stars `ValidateAmount` требует точного равенства.
- `Reconciler` реализует только Platega; Stars без возможности восстановить незавершённый invoice его не реализует.
- `Refunder` реализует Stars; Platega refund/chargeback остаётся существующим callback-путём.

### 4.2 OrderService

```go
func (o *OrderService) RequestPayment(ctx context.Context, req payment.CheckoutRequest) (*PaymentInfo, error)
func (o *OrderService) ConfirmPayment(ctx context.Context, r payment.Receipt) (*PaymentConfirmation, error)
func (o *OrderService) CancelPaymentByProvider(ctx context.Context, provider payment.ProviderID, providerPaymentID string, outcome string) error
```

Общий порядок `RequestPayment`:

1. загрузить product/subscription/plan;
2. получить `Amount` через `provider.Quote`;
3. найти pending order по `subscription_id + product_id` без фильтра provider;
4. если pending order другого провайдера жив — не создавать новый, вернуть понятную ошибку «предыдущая оплата ещё активна»;
5. создать pending order с текущим provider;
6. `MarkPaymentCreationUncertain`;
7. вызвать `provider.CreateCheckout`;
8. сохранить provider details;
9. вернуть provider-neutral `PaymentInfo`.

### 4.3 Деньги и цены

`products` получают отдельную Stars-цену:

```sql
stars_price INTEGER NOT NULL DEFAULT 0 CHECK (stars_price >= 0)
```

- Platega продолжает использовать `price_cents`/`currency = 'RUB'`.
- Stars использует `stars_price`, единица — один целый Star.
- Никакой автоматической конвертации RUB → Stars.
- Go-поля обобщаются до minor units, но legacy колонки не переименовываются, чтобы не ломать БД/API/историю.
- Форматирование цены становится currency-aware: `FormatAmount(payment.Amount)`, а не безусловный `FormatPriceCents`.

## 5. БД и миграция

Новая миграция — `042_add_telegram_stars_pricing.up.sql` / `.down.sql`.

Up:

1. `ALTER TABLE products ADD COLUMN stars_price INTEGER NOT NULL DEFAULT 0 CHECK (stars_price >= 0);`
2. удалить текущий partial unique index `idx_orders_pending_subscription_product_unique`, у которого predicate привязан к `payment_provider='platega'`;
3. создать provider-agnostic partial unique index pending-заказа по `(subscription_id, product_id)`.
4. `payment_provider` остаётся колонкой заказа, но не создаёт отдельные pending-namespaces.

Down:

1. удалить `stars_price`;
2. восстановить текущий Platega-only pending index.

Backfill не делается. Перед включением Stars оператор обязан заполнить `stars_price`.

## 6. Telegram Stars flow

### 6.1 Создание счёта

`sendInvoice` через существующий `tgbotapi`:

| Поле | Значение |
|---|---|
| `ChatID` | telegram user |
| `Title` | название продукта, валидируется под лимит |
| `Description` | краткое описание подписки |
| `Payload` | `order:<order_id>` |
| `ProviderToken` | пустая строка |
| `Currency` | `XTR` |
| `Prices` | ровно один `LabeledPrice{Amount: starsPrice}` |
| `StartParameter` | `stars-<order_id>` |
| `IsFlexible` | `false` |

Результат — `Checkout{Kind: CheckoutTelegramInvoice, MessageID: ...}`.

Ссылки-кнопки как в Platega нет: invoice приходит отдельным сообщением.

### 6.2 Pre-checkout

Новый inbound `pre_checkout_query`:

1. обработать до обычной ветки non-command message;
2. validate:
   - активный provider `telegram_stars`;
   - payload существует и парсится;
   - order pending/expired в settlement grace;
   - currency `XTR`;
   - `total_amount` точно равен `order.amount`;
   - `query.From.ID` совпадает с владельцем subscription/order;
3. ответить `answerPreCheckoutQuery` не позднее 10 секунд;
4. не вызывать внешние node/XUI API и `SyncSubscription`.

При отказе — короткая понятная причина.

### 6.3 Successful payment

Новый inbound `update.Message.SuccessfulPayment`:

1. validate provider, currency, amount, payload, owner;
2. взять `TelegramPaymentChargeID` как `provider_payment_id`;
3. атомарно привязать charge ID и перевести order в `paid` в одной транзакции;
4. далее — существующий `ConfirmOrderPaidCAS`-путь: subscription expiry, plan, audit `payment_confirmed` + `subscription_renewed`, post-commit notification, best-effort sync;
5. повторное событие — идемпотентно, без второго продления и без повторного сообщения.

### 6.4 Refund

`refundStarPayment(user_id, telegram_payment_charge_id)`.

Порядок:

1. внешний Telegram refund;
2. после успешного ответа — транзакция `paid -> refunded` и downgrade при отсутствии других активных платных заказов;
3. audit `payment_refunded` в той же DB-транзакции;
4. после коммита — уведомления.

Если Telegram refund прошёл, а DB-транзакция не прошла, нельзя показывать «refunded». Возвращаем ошибку, логируем операционную проблему для ручной сверки по charge ID.

## 7. Ожидаемые изменения по файлам

| Файл/пакет | Изменение |
|---|---|
| `internal/service/payment/` | provider-neutral типы, контракт, Stars adapter |
| `internal/service/payment/platega/` | реализация общего `payment.Provider`/`Reconciler` |
| `internal/service/order.go` | provider-neutral `RequestPayment`/`ConfirmPayment`, убирание Platega-типов |
| `internal/interfaces/interfaces.go` | `OrderService` без Platega UUID и типов |
| `internal/database/models.go` | `Product.StarsPrice`, Go-поля в терминах minor units |
| `internal/database/orders.go` | pending lookup без provider, string provider ID, atomic attach |
| `internal/database/migrations/042_*` | `stars_price`, provider-agnostic pending index |
| `internal/database/audit_log.go` | событие `payment_refunded` при реализации refund |
| `internal/config/config.go` | `PAYMENT_PROVIDER = platega | telegram_stars` |
| `internal/bot/handler.go` | dispatch pre-checkout и successful payment |
| `internal/bot/subscription_handler.go` | provider-aware buy flow |
| `internal/bot/keyboard_builder.go` | URL-кнопка только для Platega |
| `cmd/bot/main.go` | `AllowedUpdates += pre_checkout_query`, wiring Stars adapter |
| `internal/web/web.go` | Platega callback остаётся, использует новый контракт |
| `README.md` | новый способ, конфигурация, rollback |

## 8. План реализации

### Этап 1. Provider-neutral контракт

- создать `internal/service/payment` типы и интерфейс;
- убрать Platega-типы из `OrderService`/`interfaces`;
- `uuid.UUID` → `string` на границах заказа;
- заменить hardcode `"platega"` константой `payment.ProviderPlatega`.

Проверка: `go build ./...`, `go test ./internal/service ./internal/database ./internal/web`.

### Этап 2. Миграция и репозиторий

- миграция 042;
- provider-agnostic pending lookup;
- `provider_payment_id` как string;
- atomic attach `provider_payment_id` внутри того же `ConfirmOrderPaidCAS`.

Проверка: миграционные тесты, уникальный индекс, идемпотентность, settlement grace.

### Этап 3. Platega adapter

- адаптировать текущий клиент/webhook к общему контракту;
- сохранить callback amount > order amount = fee;
- сохранить существующие HTTP-заголовки и readiness.

Проверка: существующие Platega тесты, плюс новые provider-нейтральные.

### Этап 4. Stars adapter

- `Quote`, `ValidateAmount`, `CreateCheckout`;
- `sendInvoice` с `XTR`, пустым provider token и одним price;
- refund через `tgbotapi.MakeRequest("refundStarPayment", ...)`.

Проверка: unit-тесты параметров invoice, суммы, отказа неверного payload.

### Этап 5. Bot inbound

- обработка `pre_checkout_query` и `successful_payment`;
- `AllowedUpdates += pre_checkout_query`;
- короткий deadline pre-checkout;
- идемпотентное подтверждение.

Проверка: unit-тесты `HandleUpdate`, в том числе отказ при неправильном пользователе/сумме.

### Этап 6. UI

- список продуктов показывает цену активного провайдера;
- Platega — URL-кнопка;
- Stars — «счёт отправлен», без URL-кнопки;
- currency-aware формат цены.

Проверка: bot-тесты клавиатуры/сообщений.

### Этап 7. Refund

- отдельный статус/переход `paid -> refunded`;
- `payment_refunded` в `audit_log`;
- admin-инициированный refund через context actor.

Проверка: refund-тесты, повторный refund, отказ Telegram, отказ DB.

### Этап 8. Документация

- README: конфигурация, политики, rollback;
- описание, что Platega внутри Telegram не показывается одновременно со Stars в этой задаче.

## 9. Тесты

Минимальная матрица:

| Сценарий | Ожидание |
|---|---|
| Platega существующий flow | Без регрессии |
| Stars buy → invoice | pending order создаётся, invoice параметр корректный |
| Stars pre-checkout valid | approved за <10 c |
| Stars pre-checkout invalid amount/currency/user | rejected |
| Stars successful payment first | order paid, subscription renewed |
| Stars successful payment duplicate | идемпотентно |
| Stars refund | charge ID, статус refunded, audit, downgrade |
| Provider switch при живом pending | новая попытка не создаётся до expiry |
| DB миграция | индекс не даёт двух pending |
| Refund DB failure | внешний refund не маскируется как локальный успех |

Финальные гейты:

```bash
gofmt -l .
go build ./...
go vet ./...
golangci-lint run ./...
go test ./...
go test -race ./...
```

## 10. Rollout и rollback

1. выкатить код с `PAYMENT_PROVIDER=platega`;
2. убедиться, что Platega flow не изменился;
3. заполнить `stars_price`;
4. переключить `PAYMENT_PROVIDER=telegram_stars`;
5. дать старым Platega pending заказам истечь;
6. прогнать smoke-сценарии Stars.

Rollback:

- вернуть `PAYMENT_PROVIDER=platega`;
- Stars-заказы не удалять;
- новые Stars invoices перестают создаваться;
- `stars_price` не удалять до сверки paid/refund истории.
