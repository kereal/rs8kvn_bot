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
// RUB — копейки (order.amount_cents); XTR — один целый Star.
// Соответствует существующим полям orders.amount_cents / products.price_cents.
type Amount struct {
    MinorUnits int64
    Currency   Currency
}

type Product struct {
    ID              uint
    Name            string
    PriceMinorUnits int64   // из products.price_cents
    Currency        Currency // из products.currency
}

type CheckoutRequest struct {
    OrderID    uint
    TelegramID int64
    Username   string
    Product    Product
    Amount     Amount
    InvoiceTTL time.Duration
}

type CheckoutKind string

const (
    CheckoutHTTPRedirect    CheckoutKind = "http_redirect"
    CheckoutTelegramInvoice CheckoutKind = "telegram_invoice"
)

type Checkout struct {
    Kind      CheckoutKind
    URL       string // Platega; для Stars пусто
    MessageID int    // Stars: message_id отправленного invoice
    ExpiresAt time.Time
}

type Receipt struct {
    ProviderID        ProviderID
    ProviderPaymentID string // Platega — UUID-строка; Stars — TelegramPaymentChargeID
    Payload           string // десятичный order.ID
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
    // ValidateAmount сверяет фактически списанную сумму с ожидаемой.
    // Возвращает комиссию провайдера (0 для Stars).
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

// Узкий seam к Telegram API: sendInvoice и answerPreCheckoutQuery доступны
// через Send/Request, refundStarPayment — только через MakeRequest.
type TelegramAPI interface {
    Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
    Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
    MakeRequest(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error)
}
```

- `Quote`: Platega возвращает `product.PriceMinorUnits/Currency`; Stars — ту же пару, но интерпретирует её по активной конфигурации товара (см. раздел 5).
- Platega `ValidateAmount`: callback amount >= expected, разница — fee (текущая семантика).
- Stars `ValidateAmount`: точное равенство.
- `Reconciler` реализует только Platega.
- `Refunder` реализует Stars.
- **Telegram seam**: для `sendInvoice` (`Send(InvoiceConfig)` Chattable ✓) и `answerPreCheckoutQuery` (`Request(PreCheckoutConfig)` ✓) хватает `interfaces.BotAPI`. Для `refundStarPayment` typed-хелпера нет (`tgbotapi` не содержит его) — используется `MakeRequest("refundStarPayment", params)`, которое не входит в `interfaces.BotAPI`. Решение: узкий `payment.TelegramAPI { Send/Request/MakeRequest }`, реализуемый `*tgbotapi.BotAPI` в `cmd/bot/main.go`. Фейки BotAPI в тестах и web получают пустую реализацию `MakeRequest` — без изменения `interfaces.BotAPI`.

### 4.2 OrderService

Сигнатуры **не меняются**, кроме типов provider ID (минимальный дифф):

```go
// текущая сигнатура — без изменений
func (o *OrderService) RequestPayment(ctx context.Context, telegramID int64, username string, product *database.Product) (*PaymentInfo, *database.Order, error)
// изменение: uuid.UUID → string (Stars charge ID — строка)
func (o *OrderService) ConfirmPayment(ctx context.Context, providerPaymentID string, payload string, amount int64, currency string) (*PaymentConfirmation, error)
// изменение: uuid.UUID → string
func (o *OrderService) CancelPaymentByProvider(ctx context.Context, provider string, providerPaymentID string, outcome string) error
```

`PaymentInfo` (`internal/service/order.go:97`) — единственная изменяемая структура:

```go
type PaymentInfo struct {
    URL       string  // Platega — ссылка; Stars — пусто
    Provider  string  // "platega" | "telegram_stars"
    PaymentID string  // было uuid.UUID; Platega — UUID-строка, Stars — пусто до оплаты
    ExpiresAt time.Time
}
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

**Правило reuse pending-счёта (Stars):** у Stars нет URL — invoice не переоткрыть, второй `sendInvoice` создаст второй счёт. Поэтому Stars-pending без `provider_payment_id` → `ErrPaymentAlreadyInProgress` («счёт уже отправлен»), а не новый invoice. Platega-ветка сохраняет текущее поведение reuse ссылки (order.go:675-691).

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

Текущий partial unique index: `WHERE status='pending' AND payment_provider='platega'` (034:52-53).

Проблема: Stars pending orders индексом не защищены.

**Важно (проверено по драйверу): `sql.TxOptions{Isolation: sql.LevelSerializable}` НЕ даёт `BEGIN IMMEDIATE`.** `mattn/go-sqlite3` `BeginTx` игнорирует уровень изоляции (`sqlite3_sql.go:40` → `c.begin(ctx)`); `BEGIN IMMEDIATE` задаётся только DSN-параметром `_txlock=immediate`, который меняет блокировку для ВСЕХ транзакций приложения. Поэтому:

Решение без миграции и без изменения DSN:

1. `FindOrCreatePendingPaymentOrder` — `SELECT` pending **без фильтра provider** (параметр provider вместо литерала `"platega"`).
2. Сериализация конкурентных покупок — **in-process mutex** вокруг find-or-create в `database.Service` (бот — один процесс, `OrderService` один экземпляр; конкурентность только между горутинами, `MaxConcurrentHandlers=10`).
3. Второй уровень защиты сохраняется: partial unique index продолжает ловить гонку для Platega (существующий reload-after-unique-violation код остаётся).
4. Для Stars pending, созданный раньше живого Platega pending, блокирует новую покупку до его expiry — сообщение «оплата уже в процессе».

Ограничение: mutex защищает один процесс. Несколько процессов на одной БД не поддерживаются проектом и сейчас (SQLite-файл, один бинарь).

### 4.5 Refund без нового статуса заказа

`orders.status` имеет `CHECK (status IN ('pending','paid','expired','canceled'))` (миграция 017/034). Добавление `refunded` потребовало бы перестройки таблицы.

Решение: Stars refund использует `order.status='canceled'`. Событие `payment_refunded` **не существует** (реестр: order_created, payment_confirmed, order_expired, order_canceled, chargeback_received, payment_creation_uncertain — audit_log.go:61-66) — добавляется новая константа `AuditEventPaymentRefunded` + конструктор `AuditPaymentRefunded` (без миграции: CHECK на event_type нет).

**Существующий `CancelPaidOrderAndDowngradeCAS` (orders.go:527) — чарджбэк-метод**: сигнатура `(ctx, provider string, providerPaymentID uuid.UUID, now, freePlanID uint, applyPlan ChargebackPlanInTxFn)`, пишет `chargeback_received` в tx. Переиспользование для refund требует:
- `uuid.UUID` → `string` (Stars charge ID — строка `TelegramPaymentChargeID`, не UUID);
- параметризация audit-события (конструктор передаётся аргументом), чтобы refund писал `payment_refunded`, а не `chargeback_received`. Точка вызова одна (order.go:1486) — дифф малый.

Flow:
1. `refundStarPayment` → Telegram возвращает успех.
2. `paid → canceled` transition (переиспользованный CAS с параметризованным аудитом).
3. `payment_refunded` в той же транзакции.
4. Downgrade при отсутствии других `paid` заказов.

**Точка входа refund — операторская процедура, не UI.** Активного refund-вызова в коде нет (Platega refund — только пассивная реакция на CHARGEBACKED callback, order.go:1486). В этой задаче: сервисный метод `RefundStarsOrder(ctx, orderID)` вызывается оператором вручную (без команды бота и без панели — money-мутации панели исключены по ADR). Повторный refund идемпотентен: заказ уже `canceled` → CAS вернёт no-op; Telegram вернёт ошибку «already refunded» → трактуется как успех.

Недостаток: в таблице `orders` refunded и canceled неотличимы. Преимущество: `audit_log` полностью восстанавливает историю.

## 5. Telegram Stars flow

### 5.1 Создание счёта

`sendInvoice` через `tgbotapi.InvoiceConfig`:

| Поле | Значение |
|---|---|
| `ChatID` | telegram user |
| `Title` | название продукта, **обрезать до 32 символов** (лимит API) |
| `Description` | краткое описание подписки, **обрезать до 255 символов** (лимит API) |
| `Payload` | **десятичный `order.ID` без префикса** — существующий формат (`fmt.Sprint(order.ID)`, order.go:749); fallback-парсер `orderByCallbackPayload` читает чистое число (`strconv.ParseUint`, order.go:826) и работает для обоих провайдеров |
| `ProviderToken` | пустая строка (для Stars токен не нужен) |
| `Currency` | `XTR` |
| `Prices` | ровно один `LabeledPrice{Amount: product.PriceMinorUnits}` (для Stars API требует ровно один элемент) |
| `StartParameter` | `stars-<order_id>` — **непустой обязателен**: при пустом форвард invoice сохраняет кнопку Pay, и другой пользователь оплатит чужой заказ (валидация владельца отклонит оплату — деньги уйдут). Непустой `start_parameter` делает invoice single-chat/одноразовым: в форварде — URL-кнопка вместо Pay |
| `IsFlexible` | `false` (для Stars игнорируется) |
| `MaxTipAmount` | не задавать (чаевые в Stars не поддерживаются) |

Результат: `Checkout{Kind: CheckoutTelegramInvoice, MessageID: <message_id>}`.
Кнопка-ссылка не отправляется: invoice приходит отдельным сообщением.

**У Stars нет URL — `orders.payment_url` остаётся пустым.** Это меняет смысл существующей reuse-логики (order.go:675): она требует одновременно `provider_payment_id != ""` и `payment_url != ""`. У Stars `provider_payment_id` появляется только после оплаты, а `payment_url` пуст всегда → условие reuse не срабатывает никогда. Поэтому:

- существующий `SavePaymentDetails` (orders.go:278) для Stars не используется как есть — он требует `paymentURL`; нужен вариант, сохраняющий только `payment_expires_at` (+ `payment_creation_uncertain=false`) без provider ID и URL;
- либо `payment_url` для Stars кладёт техническое значение (например, `stars:<order_id>`), что сохраняет текущую структуру без нового метода. **Выбор: техническое значение** — меньше кода, формат `stars:<id>` однозначен и не конфликтует с HTTP-ссылками Platega;
- проверка reuse в order.go:675 должна учитывать provider: для Stars проверяется только `payment_expires_at` (иначе повторный клик создаст второй invoice — см. 4.2).

**Back-кнопка — известный критический паттерн навигации проекта.** Invoice — это «контент в отдельном сообщении», как QR-код. Правила:

- при покупке: карточка тарифа редактируется в «Счёт отправлен ниже», invoice отправляется **новым сообщением**;
- «⬅️ Назад» на карточке: удаляется **только** сообщение invoice (его `messageID` приходит в callback), карточка не пересоздаётся — повторная отправка карточки создаёт дубликат (известный баг, guard `TestNavigation_OpenAndBack`);
- регресс-тест: аналог `TestNavigation_OpenAndBack` для сценария invoice+Back — в этапе 6.

### 5.2 Pre-checkout

Новый inbound `pre_checkout_query`:
1. **Обработка до общей ветки non-command message и ДО rate-limiter** (`HandleUpdate`, handler.go:657+): `update.Message`/`update.CallbackQuery` равны nil → `chatID` остаётся 0 → текущий код rate-limit-ветку пропускает, но это поведение неявное и хрупкое. Pre-checkout обрабатывается отдельной веткой `update.PreCheckoutQuery != nil` первым делом.
2. **Собственный дедлайн <10 с**, не общий `HandlerTimeout=30s` (handler.go:33): `answerPreCheckoutQuery` обязан уйти в пределах 10 секунд (Telegram). Ветка получает `ctx` с таймаутом ~8 с; тяжёлые операции (node API, `SyncSubscription`) исключены.
3. Валидация: provider=`telegram_stars`, payload парсится как десятичный order ID, order существует и его provider=`telegram_stars`, currency=`XTR`, `total_amount` точно равен `order.amount_cents`, `query.From.ID` совпадает с владельцем подписки (`subscription.telegram_id` — через `order.subscription_id`).
4. `answerPreCheckoutQuery` не позднее 10 секунд: `OK=true` при успехе, `OK=false` с короткой причиной при отказе (ответ отправляется в любом случае — «тихий» pre-checkout ломает оплату).
5. Не вызывать node/XUI API и `SyncSubscription`.
6. Pre-checkout **не активирует** подписку: активация — только по `successful_payment`.

При отказе — короткая причина.

### 5.3 Successful payment

Новый inbound `update.Message.SuccessfulPayment`:
1. Валидация provider/currency/amount/payload/owner (payload — десятичный order ID).
2. `TelegramPaymentChargeID` (строка, **не UUID**) = `provider_payment_id`. `ProviderPaymentChargeID` у Stars пустой — не используется.
3. Поиск заказа: по `provider_payment_id` НЕ найти (его ещё нет) → по payload (order ID) → fallback-путь `orderByCallbackPayload` (order.go:823) уже умеет привязать provider id; для Stars он должен использовать provider `"telegram_stars"` и строковый ID.
4. Атомарная привязка charge ID + `paid` в одной транзакции: `UPDATE orders SET provider_payment_id=?, status='paid' WHERE id=? AND status IN ('pending','expired') AND (provider_payment_id IS NULL OR provider_payment_id='')`. Уникальный индекс `idx_orders_provider_payment_unique (payment_provider, provider_payment_id)` защищает от повторной привязки.
5. Существующий flow далее: subscription expiry, plan, audit, sync — через существующий `ConfirmOrderPaidCAS` (orders.go:375), без изменения его транзакционной семантики.
6. **Важно**: для Stars expired→paid без ограничения grace. `successful_payment` — авторитетный источник; деньги уже списаны, отказ — потеря денег без активации. Локальный expiry — UX-подсказка, не финансовый контроль.

Идемпотентность: повторное событие → `RowsAffected==0` → без повторного продления.

**Уведомление об оплате шлёт БОТ, а не web-слой.** Для Platega это делает web (`web.go:538-551`: `BuildPaidUserNotification` → `bot.Send` → при ошибке `NotifyPaymentIssue`). У Stars событие приходит в бота, поэтому бот повторяет эту же последовательность: `BuildPaidUserNotification` + `bot.Send` + fallback `NotifyPaymentIssue`. Web-слой в Stars-ветке не участвует.

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
| **Второй форматтер цены** — `formatMoneyCents` (service/order.go:503) | `2 300 ₽` в уведомлении об оплате и карточке заказа | `2 300 XTR` | Обязательно: иначе уведомление врёт о валюте |
| Всё остальное UI | Текущее | Текущее, без изменений | — |

Форматтеров цены в проекте **два**, оба жёстко печатают `₽`:
- `utils.FormatPriceCents` (utils/format.go:114) — кнопки списка тарифов и кнопка оплаты (keyboard_builder.go:60,72; subscription_handler.go:464);
- `formatMoneyCents` (service/order.go:503) — уведомление об оплате и карточка заказа (order.go:430,494).

Оба становятся currency-aware. Значение валюты берётся из `product.Currency` / `order.Currency`, а не из константы.

Минимум изменений: 6 точек выше. Остальной UI не трогаем.

## 6. Ожидаемые изменения по файлам

| Файл/пакет | Изменение |
|---|---|
| `internal/service/payment/types.go`, `provider.go` | **новые**: типы, `Provider`, `Reconciler`, `Refunder`, узкий `TelegramAPI` |
| `internal/service/payment/platega/` | уже существует (client.go, callback.go, status.go) — адаптация к `payment.Provider`/`Reconciler` |
| `internal/service/payment/stars/` | **новый**: Stars adapter (invoice, pre-checkout, refund) |
| `internal/service/order.go` | `PaymentInfo.PaymentID` uuid→string; `formatMoneyCents` currency-aware; `orderByCallbackPayload` — provider и string ID; `ConfirmPayment`/`CancelPaymentByProvider` — string ID |
| `internal/interfaces/interfaces.go` | `OrderRepository`: `uuid.UUID` → `string` в 6 сигнатурах (212, 213-217, 226-228) |
| `internal/testutil/testutil.go` | фейки `OrderRepository`: 10 мест с `uuid.UUID` → `string` |
| `internal/database/models.go` | `Order.ProviderPaymentID` уже `string` — без изменений; `Product.Currency` используется для XTR |
| `internal/database/orders.go` | pending lookup: provider-параметр вместо литерала `"platega"`; `uuid.UUID`→`string` в 6 сигнатурах; mutex вокруг find-or-create; атомарный attach+paid для Stars |
| `internal/database/audit_log.go` | новая константа `AuditEventPaymentRefunded` + конструктор `AuditPaymentRefunded` (без миграции) |
| `internal/config/config.go` | `PAYMENT_PROVIDER = platega \| telegram_stars`; валидация: `platega`-креды обязательны только для platega (config.go:240-246) |
| `internal/bot/handler.go` | dispatch `pre_checkout_query` и `message.successful_payment` (первой веткой, до rate-limiter); pre-checkout с собственным таймаутом <10 с |
| `internal/bot/subscription_handler.go` | provider-aware buy flow: Stars — invoice вместо URL-кнопки; отправка уведомления об оплате |
| `internal/bot/keyboard_builder.go` | URL-кнопка только для Platega; формат цены currency-aware |
| `internal/utils/format.go` | `FormatPriceCents` — currency-aware (utils/format.go:114) |
| `cmd/bot/main.go` | wiring Stars adapter + `payment.TelegramAPI`; `AllowedUpdates += pre_checkout_query` (main.go:626) |
| `internal/web/web.go` | Platega callback остаётся, использует новый контракт; Stars-ветки нет |
| `internal/bot/repro_qr_test.go` | аналог guard-теста для invoice+Back |

Миграция БД: **не делается.** `orders.status`, `products.currency`, `provider_payment_id` (TEXT) — без изменений.

## 7. Implementation plan

### Этап 1. Provider-neutral контракт + смена типа provider ID

- создать `internal/service/payment/types.go`, `provider.go` (типы, `Provider`, `Reconciler`, `Refunder`, узкий `TelegramAPI`).
- `uuid.UUID` → `string` в 6 сигнатурах `database` (orders.go) и `interfaces.OrderRepository` (212, 213-217, 226-228).
- обновить фейки `internal/testutil/testutil.go` (10 мест) и **5 существующих тест-файлов**: `service/order_test.go` (91 вхождение `uuid.`), `service/order_concurrency_test.go`, `database/audit_payment_test.go`, `database/orders_payment_lifecycle_test.go`, `web/payment_test.go`.
- `PaymentInfo.PaymentID` → `string`.
- заменить hardcode `"platega"` константой `payment.ProviderPlatega`.

Проверка: `go build ./...`, `go test ./internal/service ./internal/database ./internal/web` — все существующие тесты зелёные.

### Этап 2. Репозиторий (без схемы)

- `FindPendingPaymentOrder` / `FindOrCreatePendingPaymentOrder`: provider-параметр вместо литерала `"platega"`, lookup без фильтра provider.
- mutex вокруг find-or-create (сериализация в процессе) — **не** `sql.LevelSerializable` (драйвер игнорирует, см. 4.4).
- provider-agnostic pending lookup — отказ при чужом провайдере.
- атомарный attach `provider_payment_id` + transition pending→paid в одном UPDATE (для Stars).

Проверка: unit-тесты идемпотентности, settlement grace, concurrent purchases (существующий `TestRequestPayment_ConcurrentCallersSingleProviderTransaction` должен остаться зелёным).

### Этап 3. Platega adapter

- адаптировать к общему контракту (пакет `payment/platega` уже существует).
- сохранить callback amount >= order amount = fee.

Проверка: регрессия Platega — все существующие web/service тесты зелёные.

### Этап 4. Stars adapter

- `Quote`, `ValidateAmount`, `CreateCheckout`.
- `sendInvoice` с `XTR`, пустым provider token, одним price, payload = десятичный order ID.
- `Refund` через `MakeRequest("refundStarPayment", ...)` (typed-хелпера в tgbotapi v5.5.1 нет).
- `Stars.ValidateAmount` — точное равенство.

Проверка: unit-тесты параметров invoice, суммы, payload.

### Этап 5. Bot inbound

- dispatch `pre_checkout_query` и `message.successful_payment` — **первой веткой** `HandleUpdate`, до rate-limiter; pre-checkout с собственным таймаутом <10 с.
- `AllowedUpdates += pre_checkout_query` (main.go:626).
- `answerPreCheckoutQuery` за <10 с, ответ в любом случае (OK=false при отказе).
- уведомление об оплате шлёт бот (`BuildPaidUserNotification` + `bot.Send` + fallback `NotifyPaymentIssue`).

Проверка: unit-тесты `HandleUpdate`: валидный/невалидный pre-checkout (пользователь, сумма, валюта, провайдер), successful_payment (первый, дубликат, поздний после expiry).

### Этап 6. UI (минимальные изменения, см. раздел 5.5)

- Оба форматтера цены currency-aware: `utils.FormatPriceCents` и `service.formatMoneyCents` (6 точек из 5.5).
- Platega — URL-кнопка (текущее поведение, без изменений).
- Stars — «Счёт отправлен ниже» + invoice отдельным сообщением.
- Back после invoice: удалить только сообщение invoice (паттерн дублирования).
- Текст `MsgPaymentInProgress` для Stars: без упоминания ссылки.

Проверка: bot-тесты (в т.ч. существующие `keyboard_test.go:148,175`, `payment_handler_test.go:297` — ожидания `₽` обновляются) + регресс-тест аналога `TestNavigation_OpenAndBack`.

### Этап 7. Refund

- `CancelPaidOrderAndDowngradeCAS`: `uuid.UUID` → `string`, аудит-событие параметризуется (chargeback vs refund).
- `AuditPaymentRefunded` — новая константа + конструктор в `audit_log.go` (без миграции).
- сервисный метод `RefundStarsOrder(ctx, orderID)` — операторский вход, без UI.
- идемпотентность повторного refund.

Проверка: unit-тесты refund, duplicate refund, Telegram failure, DB failure alert path.

### Этап 8. Конфигурация и документация

- `PAYMENT_PROVIDER`: `platega | telegram_stars`; валидация — `PLATEGA_MERCHANT_ID/SECRET` обязательны **только** для platega (config.go:240-246).
- тесты конфига: оба провайдера валидны; неизвестный — ошибка; platega без кредов — ошибка; stars без platega-кредов — ок.
- Проверить раздел 9 «Rollout и rollback» этого плана актуальным для README.
- При необходимости — обновить README.md (секция платёжных способов).

## 8. Тесты

### 8.1 Существующие тесты — что сломается и что обновляется

Смена `uuid.UUID` → `string` затрагивает существующий код и тесты. Это **не** «добавить новые тесты», а механическая правка существующих:

| Файл | Что меняется | Объём |
|---|---|---|
| `internal/service/order_test.go` | `uuid.New()` → строковые provider ID в фикстурах | 91 вхождение `uuid.` |
| `internal/service/order_concurrency_test.go` | то же | конкурентные кейсы |
| `internal/database/audit_payment_test.go` | то же + проверка нового audit-события | 4 теста |
| `internal/database/orders_payment_lifecycle_test.go` | то же | lifecycle-кейсы |
| `internal/web/payment_test.go` | то же | Platega callback |
| `internal/testutil/testutil.go` | фейки `OrderRepository` (не тест, но обязателен) | 10 мест |
| `internal/bot/keyboard_test.go` | ожидания `₽` → currency-aware | :148, :175 |
| `internal/bot/payment_handler_test.go` | ожидания `₽` → currency-aware; фейк провайдера | :297 |
| `internal/config/config_test.go` | новый провайдер + правила валидации | +4 кейса |

Тесты, которые **не должны** меняться (контракт миграции фиксирует текущее поведение индекса):
- `internal/database/payment_migration_test.go:106` `TestMigration031_PendingIndexEnforcesOnlyPlategaPendingOrders` — индекс `034` не трогаем, тест остаётся зелёным.

Тесты, которые **должны остаться зелёными без правок** (регрессия Platega):
- `internal/bot/payment_handler_test.go` — 11 кейсов buy flow;
- `internal/web/web_test.go` — 11 кейсов;
- `internal/service/order_test.go` — 44 кейса;
- `internal/database/orders_test.go`, `orders_payment_intent_test.go`, `orders_payment_lifecycle_test.go`, `audit_payment_test.go` — lifecycle/CAS/audit.

### 8.2 Новые сценарии

| Сценарий | Слой | Ожидание |
|---|---|---|
| Platega существующий flow | все | Без регрессии |
| Stars buy → invoice | bot/service | pending order, invoice: currency=XTR, provider_token пуст, prices=[1], payload=десятичный order ID |
| Stars buy повторно при живом Stars-pending | service | `ErrPaymentAlreadyInProgress`, второй invoice НЕ создан |
| Stars pre-checkout valid | bot | `answerPreCheckoutQuery OK=true` за <10 с |
| Stars pre-checkout invalid amount | bot | `OK=false`, подписка не активирована |
| Stars pre-checkout invalid currency | bot | `OK=false` |
| Stars pre-checkout чужой user | bot | `OK=false` |
| Stars pre-checkout provider≠stars | bot | `OK=false` |
| Stars pre-checkout: ответ отправлен при ошибке БД | bot | `OK=false`, не «тихий» pre-checkout |
| Stars successful payment first | bot/service | order paid, subscription renewed, audit payment_confirmed |
| Stars successful payment duplicate | service | идемпотентно, `RowsAffected==0`, без второго продления |
| Stars successful payment: charge ID привязан атомарно | database | provider_payment_id = charge ID, status=paid |
| Stars successful payment → уведомление | bot | `BuildPaidUserNotification` отправлено, fallback `NotifyPaymentIssue` при ошибке |
| Concurrent Stars purchases same sub/product | database/service | второй отказ (mutex + partial index) |
| Provider switch при живом pending | service | новая попытка отклоняется до expiry |
| Stars expired→paid (поздний successful_payment) | service | активируется, grace не ограничивает |
| Stars refund | service | order canceled, audit `payment_refunded`, downgrade |
| Stars refund повторный | service | идемпотентно, no-op |
| Stars refund: Telegram error | service | БД не тронута, alert |
| Stars refund: БД error после успешного refund | service | alert + ручная сверка по charge ID |
| Формат цены XTR | utils/service | `199XTR`, `2 300 XTR` (не `₽`) |
| Invoice + Back | bot | удалено только сообщение invoice, карточка тарифа осталась, дублей нет |
| Конфиг: stars без Platega-кредов | config | валидно |
| Конфиг: неизвестный провайдер | config | ошибка |

### 8.3 Гейты

После каждого этапа — узкая проверка (пакетная). Финально:

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
2. Записать RUB-цены (по продукту: `id, price_cents, name`) — в отдельный файл вне репозитория.
3. Выбрать цену в Stars: целое число (1 Star = 1 minor unit), в пределах лимитов Telegram для XTR. Проверить актуальные лимиты перед записью цен.
4. Для каждого продукта выполнить: `UPDATE products SET price_cents = <stars>, currency = 'XTR' WHERE id = <id>;`.
5. Проверить: `SELECT id, name, price_cents, currency FROM products;` — у всех активных товаров `currency='XTR'` и `price_cents > 0`.
6. Переключить `PAYMENT_PROVIDER=telegram_stars` (перезапуск).
7. Дать старым Platega pending заказам истечь (иначе новые покупки Stars будут отклоняться как «оплата уже в процессе»).
8. Smoke test (тестовый бот или реальный тестовый аккаунт):
   - buy → приходит invoice с `Pay`, кнопка оплаты отсутствует;
   - pre-checkout → approved (в логах видно `answerPreCheckoutQuery OK=true`);
   - оплата → подписка продлена, пользователю пришло уведомление об оплате;
   - повторный клик «Оплатить» → «счёт уже отправлен», второй invoice не создан;
   - refund → заказ `canceled`, в `audit_log` событие `payment_refunded`, подписка понижена.

### Rollback на Platega

1. `PAYMENT_PROVIDER=platega` (перезапуск).
2. Восстановить RUB-цены из записей (шаг 2): `UPDATE products SET price_cents = <рубли>, currency = 'RUB' WHERE id = <id>;`.
3. Новые Stars invoices не создаются.
4. Истечь старые Stars pending (иначе покупки Platega будут отклоняться).
5. Уже оплаченные Stars заказы **не откатываются** — их возврат делается через `RefundStarsOrder` вручную.

## 10. Готовность к реализации

Чек-лист — все пункты проверены по исходникам (2026-11-18):

| # | Пункт | Статус |
|---|---|---|
| 1 | Схема БД не меняется: `orders.status`, `products.currency`, `provider_payment_id` (TEXT) — достаточно | ✓ проверено по миграциям 017/034/035 |
| 2 | Payload-формат совместим с существующим fallback-парсером | ✓ исправлено в 5.1 |
| 3 | Механизм сериализации pending работает с текущим драйвером | ✓ исправлено в 4.4 (mutex, не `LevelSerializable`) |
| 4 | `payment_refunded` создаётся как новое событие аудита | ✓ исправлено в 4.5 |
| 5 | Точка входа refund определена (операторская, без UI) | ✓ 4.5 |
| 6 | `refundStarPayment` доступен через узкий `payment.TelegramAPI` | ✓ 4.1 |
| 7 | Pre-checkout укладывается в 10 с и не попадает под rate-limiter | ✓ 5.2 |
| 8 | Уведомление об оплате для Stars шлёт бот | ✓ 5.3 |
| 9 | Оба форматтера цены currency-aware | ✓ 5.5 |
| 10 | `payment_url` для Stars: техническое значение `stars:<order_id>` | ✓ 5.1 |
| 11 | Конфиг-валидация: Platega-креды обязательны только для platega | ✓ этап 8 |
| 12 | Invoice: один price, непустой `start_parameter`, лимиты title/description | ✓ 5.1 |
| 13 | Влияние на существующие тесты оценено и перечислено | ✓ 8.1 |
| 14 | Индекс `034` не трогаем → `TestMigration031_...` остаётся зелёным | ✓ 8.1 |
| 15 | UI: 6 точек, остальное не трогаем | ✓ 5.5 |

**Открытые продуктовые/операционные вопросы (не блокируют код):**
- `/paysupport` и Terms of Service — Telegram требует обработку споров для Stars; в проекте этих команд нет (для Platega тоже). Вне scope этой задачи.
- Максимальная сумма Stars в одном invoice — проверить актуальный лимит Telegram при выборе цен (шаг 3 rollout).

## Официальные ссылки API Telegram

Факты, использованные в плане, и их первоисточники:

| Тема | Ссылка |
|---|---|
| Telegram Stars (ограничения digital goods/services) | https://core.telegram.org/bots/payments-stars |
| sendInvoice (XTR, пустой provider_token, один price) | https://core.telegram.org/bots/api/methods/sendinvoice |
| answerPreCheckoutQuery (лимит 10 секунд) | https://core.telegram.org/bots/api/methods/answerprecheckoutquery |
| SuccessfulPayment (источник истины) | https://core.telegram.org/bots/api/types/successfulpayment |
| refundStarPayment (возврат средств) | https://core.telegram.org/bots/api/methods/refundstarpayment |
