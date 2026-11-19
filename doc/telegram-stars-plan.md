# Telegram Stars: план интеграции

Дата: 2026-11-18; ревью-правки 2026-11-19 (верификация против кода на `899af44`, внесены замечания B1/B2/H1/H2). Ветка: `feature/telegram-stars`. Статус: **реализовано 2026-11-19** (этапы 2–8; все гейты зелёные: build/vet/lint 0 issues, 2227 тестов, -race чисто). План переработан после ревью (сверка кода на `899af44` + 4 независимых ревью).

**Уже реализовано (коммит `899af44`):** uuid→string во всех 5 сигнатурах `OrderRepository` + `database` + `testutil` + тесты (0 вхождений `uuid.`), `paymentLocks` строковый, `GetTransactionStatus(string)`, `AuditSubscriptionRenewed` параметризован по provider, Stars-ветка `reconcilePendingOrder` (order.go:1244-1247, с тестом), fee-skip в `SyncProviderFees`.

**Осталось из прежнего этапа 2:** provider-параметр в `FindPendingPaymentOrder`/`FindOrCreatePendingPaymentOrder` (orders.go:114,174,206,229), атомарный Stars-TTL в find-or-create, in-process mutex вокруг find-or-create, `ConfirmOrderPaidCAS` += `providerPaymentID` + `provider_fee_cents`, `CancelPaidOrderAndDowngradeCAS` += `auditFn` (параметр `provider` там уже есть).

## 1. Решение

**Добавить Telegram Stars рядом с Platega. Один активный провайдер через конфигурацию.**

| Вопрос | Решение | Почему |
|---|---|---|
| Новый способ | Telegram Stars | Цифровые товары/услуги внутри Telegram оплачиваются Stars (Bot Developer Terms 6.2) |
| Существующий способ | Platega сохраняется как legacy | Нужна совместимость и расчёт старых заказов; новые продажи Platega после перехода на Stars не ведутся |
| Третий агрегатор | Нет | Нет запроса; добавляет комплаенс и поддержку |
| Одновременный выбор Platega/Stars в UI | Нет | Отдельное продуктовое/юридическое решение |
| Изменение схемы БД | **Не делается** | Вся интеграция ложится в существующие колонки |

## 2. Non-goals

- Не менять поведение Platega (его callback, reuse ссылки, комиссия, chargeback).
- Не вводить реестр провайдеров (`Provider`/`Quote`/`ValidateAmount`/`Checkout`/`Receipt`/`Reconciler`) — это переписывание Platega без выгоды.
- Не хранить `message_id` Stars-invoice в БД и не удалять старые invoice-сообщения (см. D6).
- Не менять набор и лейблы метрик (provider виден в `audit_log` и логах).
- Не делать одновременный выбор провайдера пользователем внутри Telegram.
- Не запускать `SyncSubscription` внутри pre-checkout.
- Не ослаблять транзакционность `ConfirmOrderPaidCAS`.
- Не добавлять HTTP-эндпоинт для Stars: Stars приходят через Telegram updates.
- **Не добавлять миграцию схемы БД.**
- Не менять UI бота, кроме адаптаций Stars (нет URL-кнопки, валюта XTR, экран «счёт отправлен ниже»).

## 3. Принятые решения

Это ядро плана: каждое решение закрывает конкретную ошибку прежней версии документа.

| # | Решение | Почему | Что отменяет |
|---|---|---|---|
| D1 | Два клиента Telegram: polling-инстанс **не трогаем** (ни объект, ни отсутствие таймаута у его http.Client), плюс отдельный payment-инстанс `tgbotapi.NewBotAPIWithClient(token, APIEndpoint, &http.Client{Timeout: 5 * time.Second})` | Единственный бот в коде создан через `tgbotapi.NewBotAPI` (main.go:215) на клиенте **без** таймаута; `GetUpdates` держит соединение 60 c (`BotUpdateTimeout=60`, internal/config/constants.go:34-35) через тот же `bot.Client`, поэтому любой общий таймаут < 60 c (5 c, 9 c) убивает long-poll. Payment-инстанс изолирован: окно pre-checkout 10 c закрывается таймаутом 5 c (HTTP) + бюджетом 6 c (§5.2). `pre_checkout_query` обязательно добавляется в `AllowedUpdates` (сейчас только `message`, `callback_query`, main.go:624-627). Общий инстанс намеренно не трогаем: клиент без таймаута — текущее поведение; если таймаут когда-либо добавится, он обязан превышать `BotUpdateTimeout` (напр. 75 c), иначе polling уйдёт в цикл переподключений | «общий `NewBotAPIWithClient` с 9 s для всего бота» — такого кода в репозитории нет, а правило несовместимо с long-poll 60 c и остаётся неприменённым (5.2, этап 5, чек-лист #22 прежнего плана) |
| D2 | Активный провайдер = `cfg.PaymentProvider` (`platega` по умолчанию \| `telegram_stars`). Существующий `service.PaymentProvider` (Platega-типы) не меняем, реестра нет | 5 гвардов `o.payment == nil` (order.go:561,903,1105,1189,1417) и Platega-специфичные типы не должны ломаться. Stars-ветка обязана обходить эти гарды ранним return (§4.4, §5.1 шаг 0) | `payment.Provider`, `Quote`, `ValidateAmount`, `Checkout`, `CheckoutKind`, `Receipt`, `Reconciler`, `Refunder` (§4.1 прежнего плана) |
| D3 | **Invoice отправляет бот-слой.** Сервис создаёт pending-заказ, ставит локальный TTL и возвращает `PaymentInfo{Provider:"telegram_stars", ExpiresAt}` + `*database.Order` | Бот уже знает `chat_id`, `product` и получает `message_id` invoice из ответа `Send` — не нужно хранить его в БД; `payment.TelegramAPI` в сервисе не нужен | `payment.TelegramAPI{Send,Request,MakeRequest}` в сервисе, `Checkout` DTO, `payment_url = stars:<id>:<msg>` |
| D4 | `provider_payment_id` (charge ID) Stars пишется **в том же CAS**, что и `status='paid'` | Отдельный attach+paid UPDATE делает CAS no-op (CAS смотрит `status IN ('pending','expired')`, orders.go:410) | Двухшаговый attach (§5.3 прежнего плана) |
| D5 | Сумма Stars — целое число Stars (1 Star = 1 minor unit), точное равенство `total_amount == order.amount_cents`, fee = 0 | `platega.ParseCallbackAmount("199")` = 19900 (×100), что в 100 раз завышает `callback_amount_cents` и даёт «комиссию 19801» | Общий Platega-парсер суммы для Stars (order.go:956) |
| D6 | `payment_url` Stars пуст, `message_id` invoice в БД не хранится, старые invoice не удаляются | Хранение message_id требует гонки с быстрым `successful_payment` и создаёт риск удалить чек **оплаченного** заказа; серверного TTL у Telegram-invoice нет, отзыв всё равно не гарантирован | `stars:<order_id>:<msg>`, `PaymentInfo.MessageID/SupersedeMessageID`, `GetOrdersBySubscriptionID`, «replacement-инвойс» |
| D7 | Локальный TTL Stars = `STARS_INVOICE_TTL` (по умолчанию 15 мин), применяется к pending; `expired → paid` принимается без grace | Деньги уже списаны; отказ = потеря денег без активации. Локальный expiry — UX, а не финансовый контроль | 5-минутный settlement grace Platega для Stars |
| D8 | Stars **никогда** не ставит `payment_creation_uncertain` | У Stars нет provider-side транзакции: неоднозначность `sendInvoice` = «возможно, доставлено», и её закрывает `successful_payment` по payload. `uncertain` у Stars не истекает (`GetStalePendingOrdersAfterID` фильтрует `uncertain=false`, orders.go:59-62; фильтра по provider там нет — Stars-заказы видны свипу) и блокирует покупки навсегда | `MarkPaymentCreationUncertain` в Stars-ветке |
| D9 | Refund: идемпотентность — **по БД** (`status='canceled'` → no-op) **плюс** трактовка `CHARGE_ALREADY_REFUNDED` как успеха (возврат в Telegram уже совершён — доводим CAS); **прочие** ошибки Telegram при `paid` **не трогают БД**; точка входа — admin-команда `/refund <order_id>` | «Любая ошибка Telegram = already refunded» отзывает доступ без возврата (например, не хватило Stars на балансе бота). И зеркальный разрыв: refund прошёл в Telegram, CAS упал → повторный `/refund` получает «already refunded» и без трактовки как успеха оставил бы `paid` при возвращённых деньгах (M2). Рефанд без точки входа недоступен оператору | «сервисный метод вызывается вручную» + трактовка ошибки Telegram как успеха |
| D10 | Terms **уже есть** (`TermsText()`, legal_docs.go:165-170, кнопка `menu_terms` в keyboard_builder.go:143). Добавляем только команды `/terms` и `/paysupport`; подтверждение условий — текст «оплачивая счёт, вы принимаете соглашение» | Флаг согласия в `BotStore` = новая миграция, а миграции запрещены | «В проекте `/paysupport`/Terms нет» (этап 9 прежнего плана) |
| D11 | `provider_fee_cents = 0` для Stars пишется в той же tx, что и `paid` | Fee-очередь уже provider-scoped: `GetPaidOrdersWithoutProviderFeeAfterID` фильтрует `payment_provider='platega'` (orders.go:336) — Stars-заказы в неё не попадают | «иначе GetPaidOrdersWithoutProviderFee возвращает Stars-заказы вечно» (5.3, чек-лист #17) |
| D12 | Legacy-расчёт Platega после перехода на Stars — отдельный флаг `PLATEGA_CALLBACK_ENABLED` (по умолчанию false) | Web-callback включается по `PAYMENT_ENABLED` + Platega-креды (main.go:286-292); новый итоговый гейт — §4.1. Нужен сценарий «новые продажи Stars, старые Platega-заказы ещё рассчитываются» | «Platega credentials и callback-эндпоинт НЕ убирать» как единственный способ |
| D13 | Метрики не меняем | Label `provider` в `PaymentOperationsTotal` меняет кардинальность и все места вызова; provider и так виден в `audit_log` (`AuditPaymentConfirmed` пишет заказ с `payment_provider`) и в логах. Уточнение: `PaymentAmountCentsTotal{operation,currency}` на Stars пишет **единицы Stars** (1 Star = 1 minor unit) рядом с RUB-копейками — лейбл `currency` разделяет значения, но масштаб разный; отдельная метрика не заводится | «добавить label provider» из ops-ревью |
| D14 | Формат суммы: один общий XTR-форматтер (целое число Stars с разделителями тысяч + ` XTR`), RUB-вывод не меняется | `formatMoneyCents` делит на 100 (199 XTR → «1,99 XTR»), `utils.FormatPriceCents` печатает жёсткий `₽` и тоже делит на 100 | Утверждение «formatMoneyCents уже currency-aware — правки не требуются» (§5.5) |

### 3.1 Что осознанно не делаем

- **Не удаляем старые invoice-сообщения.** Telegram не даёт отозвать invoice; локальный TTL — только UX. Политика: поздняя оплата старого invoice = ещё одно продление (деньги не теряются). Пока pending не истёк, повторный клик **не создаёт** второй invoice (см. 5.1), поэтому в норме у пользователя один живой счёт.
- **Не меняем формат вывода RUB** — иначе ломаются существующие ожидания тестов и тексты без выигрыша.
- **Не переписываем Platega-flow.** Все его точки (web callback, `ConfirmPayment`, `CancelPaymentByProvider`, `SyncProviderFees`, fee-комиссия) остаются как есть.

## 4. Контракты изменений

### 4.1 Конфигурация (`internal/config/config.go`)

| Переменная | Тип | Default | Назначение |
|---|---|---|---|
| `PAYMENT_PROVIDER` | `platega` \| `telegram_stars` | `platega` | Активный провайдер новых продаж |
| `PLATEGA_CALLBACK_ENABLED` | bool | `false` | Legacy HTTP-callback Platega для расчёта старых заказов. Итоговый гейт: callback включён, если (`PAYMENT_ENABLED=true` и `PAYMENT_PROVIDER=platega`) **или** `PLATEGA_CALLBACK_ENABLED=true` (при непустых Platega-кредах). `PAYMENT_ENABLED=false` + `PAYMENT_PROVIDER=platega` (default) → callback выключен, как сегодня (main.go:286-292) |
| `STARS_INVOICE_TTL` | duration | `15m` | Локальный срок жизни Stars-invoice (`payment_expires_at`) |

Правила валидации (`Config.validate`, платёжный блок сейчас config.go:248-263). Переменная `PAYMENT_PROVIDER` уже существует (default `platega`, config.go:71/144); план расширяет allowlist, а не добавляет поле.

- `PAYMENT_ENABLED=true` → `PAYMENT_PROVIDER ∈ {platega, telegram_stars}` (иначе ошибка старта) — allowlist вместо жёсткой проверки `!= "platega"`;
- Platega-креды (`PLATEGA_MERCHANT_ID`/`PLATEGA_SECRET`, merchant ID — UUID) обязательны, **только** если (`PAYMENT_ENABLED=true` и `PAYMENT_PROVIDER=platega`) **или** (`PAYMENT_PROVIDER=platega` и `PLATEGA_CALLBACK_ENABLED=true`). Выключенные оплаты (`PAYMENT_ENABLED=false`, provider по умолчанию `platega`, пустые креды — как в `.env.example`) остаются валидным конфигом: безусловное требование кредов сломало бы все деплои без оплат и CI;
- `PAYMENT_PROVIDER=telegram_stars` → Platega-креды обязательны **только** при `PLATEGA_CALLBACK_ENABLED=true`;
- `STARS_INVOICE_TTL` > 0 (иначе ошибка старта; незаданное значение берёт default 15m).

`Config.String()` (config.go:315-332) дополняется `PaymentEnabled` и `PaymentProvider` (без секретов) — иначе по логам нельзя определить активный провайдер при инциденте.

### 4.2 БД (`internal/database/orders.go`, `internal/interfaces/interfaces.go`)

Новых колонок и миграций нет. Четыре изменения сигнатур:

```go
// 1) provider вместо жёсткого PaymentProviderPlatega (orders.go:114,174,206,229)
FindPendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, provider string, now time.Time) (*Order, error)
// paymentExpiresAt != nil → срок проставляется атомарно в той же tx, что и
// INSERT pending (Stars: локальный TTL). nil → прежнее поведение Platega
// (срок приходит из ответа провайдера через SavePaymentDetails).
FindOrCreatePendingPaymentOrder(ctx context.Context, subscriptionID, productID uint, provider string, amountCents int64, currency string, paymentExpiresAt *time.Time, now time.Time) (*Order, error)

// 2) Stars: charge ID и fee=0 пишутся в том же UPDATE, что и status='paid'
ConfirmOrderPaidCAS(ctx context.Context, orderID uint, paidAt, activatedAt time.Time, sub *Subscription, product *Product, applyPlan ApplyPlanInTxFn, callbackAmountCents int64, providerPaymentID string, providerFeeCents *int64) (bool, error)

// 3) refund пишет своё audit-событие, chargeback — своё
CancelPaidOrderAndDowngradeCAS(ctx context.Context, provider string, providerPaymentID string, now time.Time, freePlanID uint, applyPlan ChargebackPlanInTxFn, auditFn func(*Order, *Subscription, bool) *AuditLog) (*ChargebackResult, error)
```

Детали реализации:

- **provider в find/create:** литерал `PaymentProviderPlatega` заменяется параметром в SELECT, CREATE и reload-after-unique-violation. Фильтр по provider в pending-lookup **остаётся**: XTR- и RUB-товары разные (`product_id`), кросс-провайдерной блокировки не существует.
- **mutex:** partial unique index `034` покрывает только `payment_provider='platega'`, поэтому Stars-pending защищается in-process mutex: `pendingMu sync.Mutex` в `database.Service`, захват вокруг `FindOrCreatePendingPaymentOrder`. Один процесс, один SQLite-файл — этого достаточно.
- **CAS:** в существующий `Updates` добавляется `provider_payment_id` через `COALESCE(NULLIF(?, ''), provider_payment_id)` (пустая строка = не трогать, для Platega) и `provider_fee_cents` добавляется в map только при `providerFeeCents != nil`. Platega-путь передаёт `providerPaymentID=""`, `providerFeeCents=nil` → его поведение не меняется.
- **`CancelPaidOrderAndDowngradeCAS`:** параметр `provider` в сигнатуре уже есть (orders.go:526, interfaces.go:227) — новое только `auditFn`: вместо жёсткого `AuditChargebackReceived` (orders.go:557) вызывается переданный `auditFn` (существующий caller передаёт `database.AuditChargebackReceived`, refund — `database.AuditPaymentRefunded`).
- **TTL Stars атомарен при создании (M4):** `FindOrCreatePendingPaymentOrder` получает `paymentExpiresAt *time.Time`; для Stars он не-nil и пишется в том же INSERT, что и `status='pending'`. Platega передаёт nil — его поведение не меняется (срок как прежде приходит из ответа провайдера через `SavePaymentDetails`, orders.go:277-285). Отдельный вызов `SavePaymentDetails` в Stars-ветке не нужен. Причина: pending с `payment_expires_at IS NULL` не истекает ни в `FindPendingPaymentOrder` (проверка только при `PaymentExpiresAt != nil`), ни в свипе (`payment_expires_at IS NOT NULL`) — вечная блокировка покупки, тот самый режим, который закрывает D8. Пустой `provider_payment_id` допустим: partial unique-индекс `idx_orders_provider_payment_unique ON (payment_provider, provider_payment_id) WHERE provider_payment_id IS NOT NULL AND provider_payment_id <> ''` (034:49-54) → пустые значения не конфликтуют между заказами.
- **Новый audit-событие:** `AuditEventPaymentRefunded = "payment_refunded"` + конструктор `AuditPaymentRefunded` в `internal/database/audit_log.go` (CHECK на event_type нет — миграция не нужна).

### 4.3 Новый пакет `internal/service/payment/stars`

Leaf-пакет (не знает про БД), два назначения: построить invoice и выполнить refund.

```go
package stars

const Currency = "XTR"

// MakeRequester — единственный метод, нужный для refundStarPayment:
// в tgbotapi v5.5.1 нет typed-хелпера (bot.go: MakeRequest без ctx).
type MakeRequester interface {
    MakeRequest(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error)
}

type Client struct{ api MakeRequester }

func New(api MakeRequester) *Client

// Refund выполняет полный возврат Stars. ctx не прерывает HTTP-запрос
// (tgbotapi v5.5.1 не строит request с контекстом) — ограничение даёт
// 5-секундный таймаут payment-клиента.
func (c *Client) Refund(ctx context.Context, telegramID int64, chargeID string) error

type InvoiceParams struct {
    OrderID  uint
    ChatID   int64
    Title    string
    Amount   int64   // Stars, minor units (1 Star = 1 minor unit)
    Currency string
}

func BuildInvoice(p InvoiceParams) (tgbotapi.InvoiceConfig, error)
```

`BuildInvoice` — fail-closed, без БД:

- `Currency` обязана быть `XTR`, `Amount > 0`, иначе ошибка (ренты в Stars не отправляются);
- `Title` обрезается до **32 runes без многоточия** (у `utils.TruncateString` хвост `...` выводит за лимит: maxLen+3);
- `Description = Title` (у `database.Product` нет description, models.go:156-163);
- `Payload` = десятичный `order.ID` (≤128 bytes), `StartParameter = "stars-"+Payload` (ASCII, ≤64 chars);
- `ProviderToken = ""`, `Prices` — ровно один `LabeledPrice{Amount: int(Amount)}`, `IsFlexible=false`, чаевые не задаются;
- `ChatID` = **private chat покупателя** (`telegram_id`), не `chat_id` текущего update: invoice всегда уходит в личку, форвард в группу невозможен.

### 4.4 `OrderService` (`internal/service/order.go`)

Новых интерфейсов провайдера нет. Активный провайдер читается из конфига: `activeProvider()` → `cfg.PaymentProvider` (пусто → `platega`).

| Изменение | Детали |
|---|---|
| Поле `stars *stars.Client` + `SetStarsClient(c *stars.Client)` | setter, как `SetBot`; nil → Stars недоступен (`ErrPaymentDisabled`) |
| `RequestPayment` | Ветка Stars (5.1) при `activeProvider()==telegram_stars` — **ранний return** до гарда `o.payment == nil` (order.go:561) и до всех Platega-проверок (reuse ссылки `ProviderPaymentID != "" && PaymentURL != ""`, `MarkPaymentCreationUncertain`, `CreateTransaction`): в Stars-only режиме Platega-клиента нет, а `payment_url` Stars пустой, поэтому Platega-путь неприменим. Platega-ветка не меняется. `PaymentInfo` не расширяется: Stars → `{Provider:"telegram_stars", ExpiresAt:...}` |
| `ConfirmStarsPayment(ctx, chargeID, payload string, amount int64, currency string, telegramID int64) (*PaymentConfirmation, error)` | Provider-aware подтверждение: заказ по payload, проверки provider/currency/amount/владельца, один CAS (D4, D5) |
| `ValidateStarsPreCheckout(ctx, payload string, telegramID int64, amount int64, currency string) error` | Проверки pre-checkout без побочных эффектов; nil = OK |
| `RefundStarsOrder(ctx, orderID uint) error` | Telegram-first refund, затем CAS cancel+downgrade с `AuditPaymentRefunded` (5.4) |
| Общий хвост подтверждения | Хвост `confirmPayment` (syncSvc-guard → загрузка product/sub → CAS → invalidate кэша → `notifyAdminPaid` → `SyncSubscription`) выносится в private-метод и используется обеими ветками. Поведение Platega не меняется |
| `ReconcilePendingOrders` | Guard `if o.payment == nil && activeProvider()!=telegram_stars { return nil }` — иначе Stars-pending не истечёт. Внутри `reconcilePendingOrder` Platega-ветка при `o.payment == nil` → skip; Stars-ветка (order.go:1244-1247) — локальный expiry |
| `SyncProviderFees`, `CancelPaymentByProvider` | Без изменений: fee-очередь provider-scoped, chargeback — только Platega |

### 4.5 Bot (`internal/bot`)

`interfaces.BotAPI` уже содержит `Send` и `Request` — payment-инстанс его реализует, новых интерфейсов не нужно.

| Место | Изменение |
|---|---|
| `Handler` | Поле `paymentBot interfaces.BotAPI` + `SetPaymentBot(api)`; используется для `sendInvoice`, `answerPreCheckoutQuery` и Stars-уведомлений |
| `HandleUpdate` (handler.go:655+) | Первая ветка — `update.PreCheckoutQuery != nil` (до rate-limiter и команд; сейчас rate-limiter стоит первым, handler.go:690-698). В message-ветке — `update.Message.SuccessfulPayment != nil` **до** generic non-command обработки |
| `normalizeCommand` + dispatch (handler.go:38-40,711-752) | Команды `paysupport`, `terms`, `refund` |
| `handleBuyProduct` (subscription_handler.go:421+) | Ветка Stars: invoice вместо URL-кнопки (5.1). Карточка редактируется на месте (`NewEditMessageText`), как в Platega-ветке |
| `handleBuyPremiumList` (subscription_handler.go:387) | При `provider=telegram_stars` список тарифов фильтруется по `currency='XTR'` (M1): иначе активные RUB-товары видны, а покупка упирается в fail-closed ошибку |
| `callback.go` | Callback `stars_invoice_back_<msg_id>_<product_id>`: удалить invoice-сообщение, вернуть карточку к списку тарифов |
| `admin.go` | `HandleRefund` — admin-only `/refund <order_id>` (5.4) |

## 5. Stars flow

### 5.1 Покупка

**Сервис (`OrderService.requestPayment`, ветка Stars)**

0. Ранняя ветка на входе: `if activeProvider() == telegram_stars` → весь Stars-flow ниже и `return` **до** `o.payment == nil` (order.go:561) и до Platega reuse / `MarkPaymentCreationUncertain` / `CreateTransaction`. В Stars-режиме `o.payment` может быть nil — это не `ErrPaymentDisabled`.
1. Общая prologue-логика Platega-ветки не дублируется: canonical product, активность товара и плана, resolve подписки (`GetOrCreateSubscription`).
2. Fail-closed по валюте: `product.Currency == "XTR"` и `PriceCents > 0`, иначе ошибка (ренты в Stars не отправляются).
3. `FindPendingPaymentOrder(ctx, sub.ID, product.ID, "telegram_stars", now)`:
   - найден pending и `now < payment_expires_at` → `ErrPaymentAlreadyInProgress` (второй invoice не создаётся — invoice нельзя переоткрыть);
   - найден expired → новый заказ (find-or-create сам истекает старый).
4. `FindOrCreatePendingPaymentOrder(..., provider="telegram_stars", amount, currency, paymentExpiresAt=&ttlAt, now)`:
   - TTL проставляется **атомарно в той же tx**, что и INSERT pending (M4) — иначе pending с `payment_expires_at IS NULL` не истекает ни в lookup, ни в свипе и блокирует покупку навсегда (режим D8);
   - вернулся существующий pending (`PaymentExpiresAt != nil`) → `ErrPaymentAlreadyInProgress` (гонка/повторный клик);
   - вернулся новый заказ → TTL уже стоит, отдельный `SavePaymentDetails` не нужен; `ExpiresAt` в ответе = `*order.PaymentExpiresAt`.
5. `MarkPaymentCreationUncertain` **не вызывается** (D8): у Stars нет provider-side транзакции, неоднозначность `sendInvoice` закрывается `successful_payment` по payload.
6. Возврат: `PaymentInfo{Provider:"telegram_stars", ExpiresAt: expiresAt}` + `*database.Order`.

**Бот (`handleBuyProduct`, ветка Stars)**

7. `stars.BuildInvoice(...)` → `tgbotapi.InvoiceConfig` (4.3).
8. `paymentBot.Send(invoice)` → `message_id`.
   - Ошибка отправки → `showBuyError` с **отдельным** текстом (M5): «Не удалось отправить счёт. Попробуйте снова через несколько минут.» — не `MsgPaymentInProgress`: счёт фактически не отправлен. Денег не списано, заказ остаётся `pending` и истечёт по TTL; до истечения повторный клик даст «счёт уже отправлен» — формулировки различают эти два случая.
9. Карточка тарифа **редактируется на месте** в «Счёт отправлен ниже» + кнопка «⬅️ Назад» с `callback_data = stars_invoice_back_<message_id>_<product_id>`. Invoice — **отдельное сообщение** (паттерн QR из AGENTS.md: Open = новое сообщение, карточка остаётся под ним).
10. Текст карточки дополнит строку про комиссию (у Stars её нет) и строку согласия: «Оплачивая счёт, вы принимаете Пользовательское соглашение (/terms)».

**Back-callback (`stars_invoice_back_<message_id>_<product_id>`)**

11. Удалить **только** invoice-сообщение по `message_id` (best-effort, при ошибке — `Warn`).
12. Отредактировать карточку обратно в список тарифов (переиспользовать рендер ветки `buy_premium_list`, callback.go:93). Карточку не пересоздавать — иначе появляется дубль.

Ошибки сервиса мапятся на существующие тексты: `ErrPaymentAlreadyInProgress` → `MsgPaymentInProgress` (для Stars формулировка «счёт отправлен ранее», без упоминания ссылки).

### 5.2 Pre-checkout

1. Отдельная ветка `HandleUpdate` **до** rate-limiter и обработки команд; собственный `ctx` с таймаутом **6 c**: ответ Telegram должен уйти ≤10 c, а payment-клиент ограничен 5 c (D1).
2. `OrderService.ValidateStarsPreCheckout(ctx, payload, telegramID, amount, currency)`:
   - payload → десятичный `order.ID` → `GetOrderByID`; заказ не найден → ошибка;
   - `order.PaymentProvider == "telegram_stars"`;
   - `order.Currency == currency == "XTR"`;
   - `amount == order.AmountCents` — **точное равенство**, XTR не масштабируется ×100 (D5);
   - `subscription.TelegramID == telegramID` (владелец счёта);
   - `order.Status ∈ {pending, expired}` — `paid`/`canceled` отклоняются (повторная оплата того же invoice не пройдёт).
3. Ответ **всегда**: `OK=true` при `nil`; `OK=false` с короткой причиной при ошибке. Отправляется через `paymentBot.Request(PreCheckoutConfig)`.
4. Node/XUI API и `SyncSubscription` не вызываются. В лог — provider, payload, результат.

### 5.3 successful_payment

1. Ветка `update.Message.SuccessfulPayment != nil` — до generic non-command обработки.
2. `OrderService.ConfirmStarsPayment(ctx, chargeID=Message.SuccessfulPayment.TelegramPaymentChargeID, payload=InvoicePayload, amount=int64(TotalAmount), currency=Currency, telegramID=Message.From.ID)`:
   - заказ по payload + проверки provider/currency/amount/владельца — как в pre-checkout;
   - `order.Status == paid` → идемпотентный no-op, без второго продления;
   - `order.Status == canceled` → `NotifyPaymentIssue` (деньги списаны, доступ отозван) → `Activated=false`;
   - `pending`/`expired` → продолжение: `expired → paid` принимается без grace (деньги важнее, D7);
   - **один CAS**: `ConfirmOrderPaidCAS(..., callbackAmountCents=amount, providerPaymentID=chargeID, providerFeeCents=&zero)` → в одной tx: `status='paid'`, `provider_payment_id=chargeID`, `provider_fee_cents=0`, обновление подписки, `payment_confirmed` + `subscription_renewed` (D4, D11).
3. Post-commit (общий хвост с Platega-веткой): invalidate кэша подписки, `notifyAdminPaid`, `SyncSubscription` (best-effort, `paymentSyncTimeout`).
4. Уведомление пользователю: `BuildPaidUserNotification(ctx, order)` → текст → `paymentBot.Send`; при ошибке — `NotifyPaymentIssue` (как web.go:538-551).
5. Ошибка после списания (подписка удалена, БД недоступна) → `NotifyPaymentIssue` + пользователю «обратитесь в /paysupport»; возврат — вручную `/refund`.

### 5.4 Refund

Точка входа — admin-команда `/refund <order_id>` (`isAdmin`, admin.go:107-120 — тот же паттерн, что у `/del`).

`OrderService.RefundStarsOrder(ctx, orderID)`:

1. `stars`-клиент не задан → `ErrPaymentDisabled`.
2. `order = GetOrderByID`; `order.PaymentProvider == "telegram_stars"`, иначе ошибка «заказ не Stars».
3. `order.Status == canceled` → **no-op, успех** (идемпотентность по БД, а не по тексту ошибки Telegram).
4. `order.Status != paid` → ошибка.
5. `chargeID = order.ProviderPaymentID` (непустой), `telegramID = subscription.TelegramID`.
6. `stars.Refund(ctx, telegramID, chargeID)`:
   - **ошибка «уже возвращён»** (`CHARGE_ALREADY_REFUNDED` и эквиваленты) → трактуем как успех: в Telegram деньги уже возвращены, идём к шагу 7 (CAS идемпотентен). Без этой ветки сценарий «refund прошёл, CAS упал» навсегда оставляет `paid` при возвращённых деньгах (M2);
   - **прочие ошибки Telegram** (например, не хватило Stars на балансе бота) → `NotifyPaymentIssue`, **БД не трогаем**, доступ сохраняется, возвращаем ошибку;
   - успех → шаг 7.
7. `CancelPaidOrderAndDowngradeCAS(ctx, "telegram_stars", chargeID, now, freePlan.ID, o.chargebackPlanInTx(), database.AuditPaymentRefunded)` → `paid → canceled` + downgrade на free-план (если нет других активных paid-заказов) + `payment_refunded` в той же tx. **После коммита**, при `Downgraded` — тот же шаг, что у chargeback: post-commit `SyncSubscription` (`syncChargebackAfterCommit`, order.go:1618) и admin-уведомление refund-алерт (по образцу `notifyAdminChargeback`). Деньги возвращены — доступ должен отключаться сразу, а не когда `SyncPendingNodes` дорабёт `pending_remove` (M3).
8. Ошибка БД **после** успешного refund → `NotifyPaymentIssue` («деньги возвращены, состояние не применено») + ручная сверка по charge ID.
9. Уведомление пользователю и админу (refund-алерт; при downgrade — после post-commit sync из шага 7).

Особенности: возврат только полный, `refundStarPayment(user_id, telegram_payment_charge_id)` не принимает сумму; средства списываются с баланса Stars бота → держать запас, покрывающий возвраты.

### 5.5 Истечение, повторная оплата, uncertain

- **Истечение pending:** `payment_expires_at` проставлен → `GetStalePendingOrdersAfterID` видит заказ → `reconcilePendingOrder` (Stars-ветка, order.go:1244-1247) → `ExpirePendingOrder`. Sweep идёт с запасом 10 мин (`pendingOrderGrace`), фактическое истечение ≈ TTL + 10 мин.
- **Повторный клик до истечения:** `ErrPaymentAlreadyInProgress`, второго invoice нет → в норме у пользователя один живой счёт.
- **Поздняя оплата:** после истечения пользователь может оплатить старое сообщение invoice → `successful_payment` по expired-заказу → принимается → **второе продление**. Принятая политика: обе оплаты — настоящие покупки, деньги не теряются; локальный expiry — UX, а не финансовый контроль (D7).
- **`payment_creation_uncertain`:** Stars его не выставляет (D8) → вечной блокировки покупок нет. Случай ручной правки БД разбирается вручную, как для Platega.
- **Лимиты XTR:** проверить актуальные ограничения Telegram перед созданием товаров.

## 6. UI и тексты

| Элемент | Platega (сейчас) | Stars (новое) |
|---|---|---|
| Список тарифов (`BuyProductList`) | `Название — 199₽` | `Название — 199 XTR` — валюта из `product.Currency`; при `provider=telegram_stars` список фильтруется по `currency='XTR'` (M1) |
| Кнопка оплаты (`BuyProductConfirm`) | URL-кнопка `💳 Оплатить 199₽` | URL-кнопки нет: оплата — кнопка `Pay` внутри invoice |
| Карточка покупки | «После оплаты тариф активируется автоматически… _Платёжная система может дополнительно взимать комиссию._» | Без строки про комиссию; строка «Оплачивая счёт, вы принимаете Пользовательское соглашение (/terms)» |
| После покупки | Ссылка в кнопке | Invoice — отдельное сообщение; карточка редактируется на «Счёт отправлен ниже» + «⬅️ Назад» |
| `MsgPaymentInProgress` | «Откройте ссылку, отправленную ранее…» | «Нажмите на счёт, отправленный ранее…» (без упоминания ссылки) |
| Ошибка отправки invoice (Stars) | — | Новый ключ: «Не удалось отправить счёт. Попробуйте снова через несколько минут.» — заказ остаётся `pending` и истечёт по TTL (M5) |
| Back после invoice | — | Удаляется **только** invoice-сообщение; карточка возвращается к списку тарифов |

### 6.1 Форматтеры суммы

Один источник правды для XTR, RUB-вывод не меняется:

- `utils.FormatPriceCents(amountMinor int64, currency string) string` — сигнатура меняется; call sites: `keyboard_builder.go:60,72`, `subscription_handler.go:464`. Для `XTR` → целое число Stars с разделителями тысяч + ` XTR` (`199` → `199 XTR`), для `RUB` → текущий вывод без изменений (`199₽`).
- `utils.FormatStarsAmount(amountMinor int64) string` — общий XTR-форматтер, используется обоими местами.
- `service.formatMoneyCents(amountMinor int64, currency string)` — делегирует `utils.FormatStarsAmount` для `XTR`; RUB-вывод не меняется. **Не** делит XTR на 100 (сейчас 199 XTR печатается как «1,99 XTR»).

`utils.TruncateString` для заголовка invoice не используется: он возвращает `r[:maxLen] + "..."` — до 35 runes при лимите Telegram 32. Обрезка — rune-safe slice без многоточия (в `stars.BuildInvoice`).

## 7. Файлы

| Файл | Изменение |
|---|---|
| `internal/config/config.go` | `PLATEGA_CALLBACK_ENABLED`, `STARS_INVOICE_TTL`, allowlist `PAYMENT_PROVIDER`, валидация, `String()` |
| `internal/config/config_test.go` | Кейсы: оба провайдера, неизвестный provider, Platega-креды только для Platega, **`PAYMENT_ENABLED=false` + `PAYMENT_PROVIDER=platega` (default) без кредов — ок (регрессия B1)**, `PLATEGA_CALLBACK_ENABLED` без кредов → ошибка, TTL ≤ 0 → ошибка |
| `internal/database/orders.go` | 4 сигнатуры (§4.2), `pendingMu` вокруг find-or-create, поля CAS, `auditFn` в cancel-CAS |
| `internal/interfaces/interfaces.go` | Те же 4 сигнатуры в `OrderRepository` |
| `internal/database/audit_log.go` | `AuditEventPaymentRefunded` + `AuditPaymentRefunded` |
| `internal/service/payment/stars/` | **новый пакет**: `invoice.go`, `refund.go`, тесты |
| `internal/service/order.go` | `stars`-клиент + `SetStarsClient`, 3 новых метода, Stars-ветки, общий хвост `confirmPayment`, guard reconcile |
| `internal/utils/format.go` | `FormatPriceCents(amount, currency)`, `FormatStarsAmount` |
| `internal/bot/handler.go` | `paymentBot` + `SetPaymentBot`, dispatch `pre_checkout_query` и `message.successful_payment`, команды `paysupport`/`terms`/`refund` |
| `internal/bot/subscription_handler.go` | Stars buy-flow, отправка invoice, карточка «Счёт отправлен ниже», фильтр списка тарифов по `currency='XTR'` при provider=stars (M1), текст ошибки отправки invoice (M5) |
| `internal/bot/keyboard_builder.go` | Currency-aware цена, клавиатура Stars-карточки |
| `internal/bot/callback.go` | `stars_invoice_back_<msg_id>_<product_id>` |
| `internal/bot/admin.go` | `HandleRefund` (`/refund <order_id>`) |
| `internal/bot/command.go` | `HandlePaySupport`, `HandleTerms` (тексты уже есть в `legal_docs.go`) |
| `internal/bot/messages.go` | Текст `MsgPaymentInProgress` для Stars (новый ключ, чтобы не ломать Platega) + ключ ошибки отправки invoice (M5) |
| `internal/testutil/` | Fake `OrderRepository` под новые сигнатуры + fake payment API (`Send`/`Request`/`MakeRequest`) |
| `cmd/bot/main.go` | `initBot` возвращает payment-инстанс (`http.Client{Timeout: 5s}`), `AllowedUpdates += pre_checkout_query` (main.go:624-627), `SetPaymentConfig` — по итоговому гейту §4.1: `(PAYMENT_ENABLED && PAYMENT_PROVIDER=platega) || PLATEGA_CALLBACK_ENABLED` (main.go:286-292) |
| `cmd/bot/lifecycle.go` | Wiring: `stars.New(paymentAPI)` → `orderService.SetStarsClient`, `handler.SetPaymentBot`, Platega-клиент по флагам (lifecycle.go:138-146; создание только при `PAYMENT_ENABLED`, в Stars-режиме `payment interface` остаётся nil — это нормально, §4.4) |
| `README.md`, `doc/operations.md`, `.env.example` | Новые env, порядок переключения, процедура возврата |

Миграция БД: **не делается.**

## 8. Implementation plan

| Этап | Содержание | Проверка |
|---|---|---|
| 1 | **Выполнен (899af44)**: uuid→string, `paymentLocks`, fee-skip, Stars-ветка reconcile | build/vet/lint/test — зелёные |
| 2 | **Выполнен**: Конфиг (§4.1) + БД (§4.2): сигнатуры, `pendingMu`, CAS-поля, `AuditPaymentRefunded` | `go test ./internal/config ./internal/database ./internal/interfaces` |
| 3 | **Выполнен**: пакет `internal/service/payment/stars`: `BuildInvoice` + `Refund` + unit-тесты | `go test ./internal/service/payment/stars` |
| 4 | **Выполнен**: `OrderService`: Stars-ветки, `ConfirmStarsPayment`, `ValidateStarsPreCheckout`, `RefundStarsOrder`, общий хвост `confirmPaidOrderTail`, guard reconcile | `go test ./internal/service` |
| 5 | **Выполнен**: Бот: payment-клиент, dispatch pre-checkout/successful_payment, buy-flow, back-callback, `/paysupport`, `/terms`, `/refund` | `go test ./internal/bot` |
| 6 | **Выполнен**: UI и форматтеры (§6): `FormatStarsAmount`, `formatMoneyCents` XTR без деления на 100, XTR-фильтр списка, тексты M5/Stars | `go test ./internal/utils ./internal/bot` |
| 7 | **Выполнен**: Wiring (`cmd/bot/main.go`: отдельный payment-инстанс D1, `AllowedUpdates += pre_checkout_query`, гейт `ShouldEnablePlategaCallback`; `lifecycle.go`: Platega-клиент по гейту) | `go build ./... && go test ./cmd/...` |
| 8 | **Выполнен**: Документация: README, doc/operations.md (§8 Stars: rollout/rollback/compliance//refund), doc/installation.md, .env.example | — |

Финальные гейты:

```bash
gofmt -l .
go build ./...
go vet ./...
golangci-lint run ./...
go test ./...
go test -race ./...
```

## 9. Тесты

### 9.1 Что меняется в существующих тестах

| Файл | Что меняется |
|---|---|
| `internal/testutil/testutil.go` | Fake `OrderRepository` под новые сигнатуры (provider-параметр, атомарный TTL, CAS-поля, `auditFn`); fake payment API (`Send`/`Request`/`MakeRequest`) — у существующего fake `BotAPI` (`ServiceStoreFake`/`BotStoreFake`) есть только `Send`/`Request`, `MakeRequest` добавляется |
| `internal/database/orders_test.go` | Вызовы `ConfirmOrderPaidCAS` с новыми аргументами |
| `internal/database/orders_payment_intent_test.go` | Вызовы `FindOrCreatePendingPaymentOrder` с provider-параметром и атомарным TTL (M4) |
| `internal/database/orders_payment_lifecycle_test.go` | Вызовы `CancelPaidOrderAndDowngradeCAS` с `auditFn` |
| `internal/database/audit_payment_test.go` | Вызовы CAS + кейс `payment_refunded` |
| `internal/service/order_test.go` | Фейки `FindPendingPaymentOrder`/`ConfirmOrderPaidCAS` + кейсы Stars (покупка, pre-checkout, confirm, refund) |
| `internal/service/order_concurrency_test.go` | Фейки CAS/pending + конкурентные покупки Stars (второй отказ) |
| `internal/web/payment_test.go` | Фейки CAS/cancel-CAS (механическая правка, поведение Platega не меняется) |
| `internal/bot/payment_handler_test.go` | Фейки `FindPendingPaymentOrder`/`FindOrCreatePendingPaymentOrder`, ожидания |
| `internal/bot/keyboard_test.go` | Ожидания цены (currency-aware) |
| `internal/config/config_test.go` | Новые env и правила валидации |
| `internal/utils/format_test.go` | Кейсы XTR |

**Не меняются:** `internal/database/payment_migration_test.go` (индекс `034` не трогаем), `internal/web/web_test.go` — регрессия Platega обязана остаться зелёной.

### 9.2 Новые сценарии

| Сценарий | Слой | Ожидание |
|---|---|---|
| Stars buy → invoice | bot/service | pending `payment_provider=telegram_stars`, `payment_expires_at = TTL`, invoice: `XTR`, `provider_token` пуст, `prices=[1]`, `payload`=ID, `start_parameter=stars-<ID>` |
| BuildInvoice: не-XTR валюта / нулевая сумма | stars | ошибка, invoice не собран |
| BuildInvoice: длинный title | stars | ≤32 runes, без `...` |
| Stars повторный клик при живом pending | service | `ErrPaymentAlreadyInProgress`, второй invoice не создан |
| Stars: ошибка отправки invoice | bot | карточка с ошибкой (**отдельный** текст, не «счёт уже отправлен»), заказ `pending`, истечёт по TTL |
| RUB-товар при `provider=telegram_stars` | bot/service | в списке тарифов не показывается (фильтр `currency='XTR'`, M1); прямой buy → fail-closed ошибка |
| Pre-checkout valid | bot/service | `OK=true` |
| Pre-checkout: чужой user / неверная сумма / неверная валюта / provider≠stars / заказ не найден / заказ `paid` | bot/service | `OK=false`, ответ отправлен **всегда** |
| Pre-checkout: ошибка БД | bot | `OK=false`, не «тихий» pre-checkout |
| successful_payment первый | service | `paid`, `provider_payment_id`=charge ID, `provider_fee_cents=0`, подписка продлена, audit `payment_confirmed` + `subscription_renewed` |
| successful_payment дубль | service | идемпотентно, без второго продления |
| successful_payment после expiry | service | активируется (деньги списаны) |
| successful_payment: заказ `canceled` | service | alert, `Activated=false`, БД не меняется |
| CAS Stars | database | одна tx: `provider_payment_id` + `status='paid'` + `provider_fee_cents=0` + подписка + audit |
| Stars TTL атомарен | database | в одной tx создан pending **и** проставлен `payment_expires_at` (M4); Platega-путь передаёт nil и не меняется |
| Stars pending истёк | service/database | `reconcilePendingOrder` → `expired`; Platega-путь без регресса |
| Refund успех | service | `canceled`, audit `payment_refunded`, downgrade на free-план |
| Refund повторный | service | no-op по БД (`status='canceled'`) |
| Refund: ошибка Telegram | service | БД **не тронута**, alert, доступ сохранён |
| Refund: `CHARGE_ALREADY_REFUNDED` | service | трактуется как успех → CAS доводится до `canceled` + downgrade + audit (M2) |
| Refund с downgrade | service | после CAS — post-commit `SyncSubscription` + admin refund-алерт (M3) |
| Refund: ошибка БД после успешного refund | service | alert + ручная сверка по charge ID |
| `/refund` от не-админа | bot | отказ |
| `/paysupport`, `/terms` | bot | отвечают |
| Конфиг | config | `PAYMENT_ENABLED=false` + `PAYMENT_PROVIDER=platega` (default) без кредов — ок (регрессия B1: `.env.example` остаётся валидным); platega + `PLATEGA_CALLBACK_ENABLED` без кредов — ошибка; stars без Platega-кредов — ок; stars + `PLATEGA_CALLBACK_ENABLED` без кредов — ошибка; неизвестный provider — ошибка; `STARS_INVOICE_TTL` ≤ 0 — ошибка |
| Формат цены XTR | utils/service | `199 XTR`, `2 300 XTR` — не «1,99 XTR» и не «₽» |
| Invoice + Back | bot | удалено только invoice-сообщение, карточка вернулась к списку, дублей нет |
| Polling не сломан | cmd/bot | payment-инстанс и polling-инстанс — разные `http.Client` |

## 10. Rollout, rollback, compliance

### 10.1 Переключение на Stars

1. Развернуть код с `PAYMENT_PROVIDER=platega` — поведение не меняется.
2. Создать **отдельные** XTR-товары (`INSERT`, `currency='XTR'`, `price_cents` = цена в Stars). RUB-товары и их цены не трогать: `UpdateProductGuarded` запрещает менять цену после первого заказа. После создания **деактивировать RUB-товары** (`is_active=false`): при `PAYMENT_PROVIDER=telegram_stars` список тарифов уже фильтруется по `currency='XTR'` (§4.5), но деактивация убирает RUB из любой другой выдачи; цены и строки RUB-товаров при этом не изменяются (M1).
3. Проверить активные товары: `currency='XTR'`, `price_cents > 0`, лимиты Telegram.
4. Если есть незавершённые Platega-заказы → `PLATEGA_CALLBACK_ENABLED=true` + оставить Platega-креды (расчёт старого без новых продаж).
5. `PAYMENT_PROVIDER=telegram_stars`, `PAYMENT_ENABLED=true`, перезапуск.
6. Проверить до приёма трафика: `/paysupport` и `/terms` отвечают; на балансе бота достаточно Stars для возвратов.
7. Smoke: buy → invoice → pre-checkout `OK=true` → оплата → продление + уведомление; повторный клик → «счёт уже отправлен»; `/refund <id>` → `canceled` + `payment_refunded`.
8. После расчёта старых Platega-заказов: `PLATEGA_CALLBACK_ENABLED=false`, Platega-креды ротируются по регламенту секретов.

### 10.2 Rollback

1. `PAYMENT_ENABLED=false` — продажи остановлены, новые Stars-invoice не создаются; legacy Platega-callback продолжает работать при `PLATEGA_CALLBACK_ENABLED=true`.
2. Уже оплаченные Stars-заказы не откатываются; возврат — `/refund <order_id>`.
3. **Возврат к продажам через Platega — юридическое решение, а не технический дефолт:** цифровые товары и услуги внутри Telegram оплачиваются Stars (Bot Developer Terms 6.2). Технический rollback по умолчанию — остановка продаж с сохранением возвратов.

### 10.3 Compliance (обязательно до production)

- `/paysupport` — обязательная команда (FAQ Telegram); текст `SupportText()` уже есть.
- `/terms` — доступ к соглашению; текст `TermsText()` уже есть, кнопка в меню тоже.
- Подтверждение условий — текст в карточке покупки («Оплачивая счёт, вы принимаете соглашение»). Персистентный флаг согласия не вводится: это потребовало бы миграции, а покупка сама является согласием (D10).
- Споры и возвраты — владелец бота; процедура возврата описана в 5.4 и исполняется командой `/refund`.
- На балансе Stars-бота держать запас на возвраты.

## 11. Готовность к реализации

| # | Пункт | Статус |
|---|---|---|
| 1 | Схема БД не меняется: `payment_provider`, `provider_payment_id`, `payment_expires_at`, `payment_creation_uncertain`, `provider_fee_cents` достаточно | ✓ проверено по миграциям: `provider_payment_id`=017, `payment_url/payment_expires_at/payment_creation_uncertain`=031, индексы=034, `provider_fee_cents/callback_amount_cents`=035 |
| 2 | Активный provider из конфига; Platega-flow не переписывается; реестра провайдеров нет | D2, §4.4 |
| 3 | Polling-инстанс не трогаем (ни объект, ни отсутствие таймаута у его клиента); payment-клиент (5 c) — отдельный инстанс; `AllowedUpdates += pre_checkout_query` | D1, этап 7 |
| 4 | Pre-checkout: ctx 6 c, ответ ≤10 c, `OK=false` при любой ошибке | §5.2, этап 5 |
| 5 | Сумма Stars — целое число Stars, точное равенство, fee=0; Platega-парсер не применяется | D5, §5.2–5.3 |
| 6 | Charge ID и `status='paid'` — одна транзакция | D4, §4.2 |
| 7 | `payment_creation_uncertain` Stars не выставляет + TTL проставляется атомарно в tx создания → нет вечной блокировки покупок | D8, §4.2, §5.1 |
| 8 | Invoice отправляет бот; `message_id` в БД не хранится; старые invoice не удаляются | D3, D6, §5.1 |
| 9 | Refund: идемпотентность по БД + `CHARGE_ALREADY_REFUNDED` как успех; прочие ошибки Telegram не трогают БД; downgrade с post-commit sync; есть исполнимый `/refund` | D9, §5.4 |
| 10 | `payment_refunded` — новое событие аудита без миграции | §4.2 |
| 11 | Конфиг: allowlist provider, креды только под активным платежом/callback-режимом, `PLATEGA_CALLBACK_ENABLED`, `STARS_INVOICE_TTL` | §4.1 |
| 12 | Форматтеры: XTR целым числом, RUB-вывод не изменён | §6.1 |
| 13 | `/paysupport` + `/terms`; Terms-текст уже есть, дубли не создаём | D10, §10.3 |
| 14 | Rollback = остановка продаж + рабочий возврат | §10.2 |
| 15 | Список файлов, интерфейсов, fake-ов и тестов полный | §7, §9 |
| 16 | Единственный источник XTR-форматирования, обрезка title в пределах лимита | §6.1, §4.3 |

**Статус:** план приведён к непротиворечивому виду, решения приняты (D1–D14); замечания верификации от 2026-11-19 (B1/B2 — конфиг, H1 — D1, H2 — ранняя Stars-ветка, M1–M5, L1–L8 — точность ссылок и крайние случаи) внесены. Реализация начинается с этапа 2.

## Официальные ссылки API Telegram

| Тема | Ссылка |
|---|---|
| Telegram Stars: ограничения digital goods/services, forwarding behavior | https://core.telegram.org/bots/payments-stars |
| Bot Developer Terms 6.2 (цифровые товары: `/paysupport`, Terms, споры) | https://telegram.org/tos/bot-developers#6-2-digital-goods-and-services |
| Bot Developer Terms 6.2.1/6.2.4 (возвраты списываются с баланса) | https://telegram.org/tos/bot-developers#6-2-1-payment-disputes-for-digital-goods-and-services |
| sendInvoice (XTR, пустой provider_token, ровно один price, лимиты title/description/payload) | https://core.telegram.org/bots/api/methods/sendinvoice |
| answerPreCheckoutQuery (лимит 10 секунд) | https://core.telegram.org/bots/api/methods/answerprecheckoutquery |
| SuccessfulPayment (источник истины) | https://core.telegram.org/bots/api/types/successfulpayment |
| refundStarPayment (полный возврат по charge ID) | https://core.telegram.org/bots/api/methods/refundstarpayment |
| Валюты и единицы XTR | https://core.telegram.org/bots/payments/currencies.json |

