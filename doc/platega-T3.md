 ```markdown
# Техническое задание: Интеграция платежной системы Platega.io

Версия: 1.3 · Дата: 2026-08-09 · Статус: к реализации

────────────────────────────────────────────────────────────────────────────────

## 0. Глоссарий и ссылки

- **Platega** — внешний платёжный провайдер (https://app.platega.io).
- **Endpoint (исходящий)** — `POST https://app.platega.io/v2/transaction/process` (без выбора метода; плательщик выбирает сам на странице Platega).
- **Callback (входящий)** — `POST /payment/callback` на нашем сервере, Platega шлёт `X-MerchantId`/`X-Secret` заголовки + JSON body.
- **Order** — запись в `orders` (см. миграцию 017). Статусы: `pending | paid | expired | canceled`.
- **Product** — тариф в `products` (см. миграцию 013/021). Поля: `id, plan_id, name, duration_days, price_cents, currency, is_active`.
- `PAYMENT_ENABLED=false` — флаг, при котором платёжная интеграция выключена, платёжные кнопки скрыты.
- **subURL** — публичная ссылка пользователя `Config.SubURL(subscriptionID)`.

Документация Platega: https://docs.platega.io/ (раздел «Создание платёжной ссылки без заданного метода», callback «об изменении статуса транзакции»). Или это: https://docs.platega.io/llms.txt

────────────────────────────────────────────────────────────────────────────────

## 1. Цели

1. Подключить приём платежей через Platega.io для Telegram-бота.
2. Тарифы для оплаты — только активные платные продукты из БД (`products.is_active=true AND price_cents > 0`), без хардкода.
3. Идемпотентная обработка webhook: параллельные и повторные callback'и не активируют подписку и не отправляют уведомление дважды.
4. После успешной оплаты — новое сообщение пользователю с subURL (без правки исходного экрана).
5. Полностью удалить старую hardcoded-кнопку `buy_premium_230` и весь связанный бесплатный upgrade flow.
6. Единая логика отображения тарифа, даты истечения и трафика в «Моя подписка» и сообщении после оплаты.

────────────────────────────────────────────────────────────────────────────────

## 2. Конфигурация (internal/config)

### 2.1 Новые env-переменные

| Имя                 | Тип    | Default   | Описание                                    |
|---------------------|--------|-----------|---------------------------------------------|
| `PAYMENT_ENABLED`     | bool   | false     | Глобальный флаг включения платежей          |
| `PAYMENT_PROVIDER`    | string | "platega" | Идентификатор провайдера (задел на будущее) |
| `PLATEGA_MERCHANT_ID` | string | ""        | UUID мерчанта (заголовок X-MerchantId)      |
| `PLATEGA_SECRET`      | string | ""        | API-ключ (заголовок X-Secret)               |

Базовый URL Platega (`https://app.platega.io`) — константа в коде, не env.

### 2.2 Новые поля Config (Go)

```go
PaymentEnabled    bool
PaymentProvider   string
PlategaMerchantID string
PlategaSecret     string
 ```

 Регистрируются через существующий internal/flag/flag.go (flag.NewBool, flag.NewString).

 - PAYMENT_ENABLED=false → PLATEGA_* могут быть пустыми; валидация provider credentials пропускается.
 - PAYMENT_ENABLED=true → PAYMENT_PROVIDER обязан быть platega, PLATEGA_MERCHANT_ID и PLATEGA_SECRET обязательны и непусты.
 - Ошибка credentials: PLATEGA_MERCHANT_ID and PLATEGA_SECRET are required when PAYMENT_ENABLED=true.

 ### 2.3 Удаляемые env-переменные

 Удалить MAIN_MENU_BTN_PRODUCT и поле MainMenuBtnProductID, его флаг и validation (см. §11). .env.example — убрать соответствующую секцию.

 ### 2.4 Файлы .env и .env.example

 Добавить секцию (и удалить секцию «Main Menu Product Button» если есть):

 ```bash
# Payment Configuration (optional)
# Enables in-bot payment integration. Requires PLATEGA_* when true.
PAYMENT_ENABLED=false
PAYMENT_PROVIDER=platega
PLATEGA_MERCHANT_ID=
PLATEGA_SECRET=
 ```

 ────────────────────────────────────────────────────────────────────────────────

 3. База данных и модели

 ### 3.1 Миграции

 Не требуется. Решение зафиксировано: после оплаты бот шлёт новое сообщение, поля telegram_id/message_id в orders не добавляются. Существующая миграция 017_create_orders
 достаточна.

 ### 3.2 Модель Order (internal/database/models.go)

 #### 3.2.1 Семантика Order.ExpiresAt

 orders.expires_at — это снимок (snapshot) фактического срока подписки, выданной клиенту в результате этой оплаты: копия subscriptions.expires_at на момент подтверждения
 транзакции.

 Это НЕ инвойс-таймаут. Срок жизни платёжки (expiresIn, 15 минут в Platega) живёт только в провайдере и никогда не сохраняется в БД заказа.

 Заказ orders — это аудиторская запись факта сделки («что именно куплено и по какому сроку»). Поэтому он обязан нести полный снимок сделки независимо от будущих изменений живой
 подписки, по причинам:

 1. Аудит сделки. subscriptions.expires_at — текущее (живое) значение, которое сдвигается при каждом продлении, смене плана, отмене, chargeback. Через несколько продлений по
    живой подписке уже нельзя восстановить, какой срок был выдан именно за эту оплату. Заказ хранит это навсегда.
 2. Независимость от жизненного цикла подписки. Подписка может быть переписана на другой subscription_id или отвязана. orders должен оставаться самодостаточным документом сделки
    (связь только через subscription_id — это FK-указатель, а не источник истины о сроке).
 3. Разрешение споров и поддержка. При вопросе «я платил X — что мне выдали?» заказ даёт полный снимок: продукт + сумма + срок — не зависящий от последующего состояния.
 4. Идемпотентность. При повторном/параллельном CONFIRMED заказ уже paid и не перезаписывается; ExpiresAt остаётся первым снимком. Это защищает от «раздувания» срока повторами и
    подтверждает пункт 5.5 (переход совершает только один callback).

 #### 3.2.2 Заполнение значения

 Order.ExpiresAt заполняется однократно при переходе pending → paid и получает то же значение, что и subscriptions.expires_at:

 - newExpiry := calculateProductExpiry(now, sub.PlanID, sub.ExpiresAt, product) (вычисляется внутри DB-транзакции подтверждения — см. §5.5, шаг 3);
 - в той же атомарной DB-транзакции пишутся оба: order.ExpiresAt = &newExpiry и sub.ExpiresAt = &newExpiry;
 - таким образом orders.expires_at == subscriptions.expires_at на момент активации и эти два значения согласованы (никуда не расходятся).

 Явно: не вычислять Order.ExpiresAt независимо (например, как now + 30min или order.CreatedAt + timeout) — это была бы ошибочная «инвойс-семантика», которую §3.2.1 запрещает.
 Вычисление newExpiry и запись обоих полей выполняются в одной транзакции (CAS), а не отдельными шагами до/после.

 #### 3.2.3 Правка комментариев (обязательная)

 Актуализировать устаревшие формулировки в двух местах:

 - internal/database/models.go — заменить текущий комментарий, который ошибочно гласит «payment invoice expiry (e.g. 30 minutes from creation)»:
   ```go
// ExpiresAt — срок действия купленной подписки (копия subscriptions.expires_at
// на момент оплаты). НЕ invoice timeout.
ExpiresAt *time.Time `gorm:"column:expires_at"`
   ```
 - internal/database/migrations/017_create_orders.up.sql — заголовочный комментарий к полю expires_at там гласит «срок действия invoice (например, 30 минут с момента создания)»
   (в текущем файле битый пробел: «действияinvoice»). Заменить на:
   ```sql
-- expires_at           — срок действия подписки, выданной этой покупкой
--                        (копия subscriptions.expires_at на момент оплаты).
--                        НЕ таймаут инвойса.
   ```

 #### 3.2.4 Тест

 В тестах ConfirmPayment (см. §5.7) добавить явную проверку:

 - после успешной активации order.ExpiresAt == newExpiry и order.ExpiresAt == *sub.ExpiresAt;
 - при повторном CONFIRMED значение order.ExpiresAt не меняется (снимок первого активировавшего callback).

 ### 3.3 Новые методы репозиториев

 #### internal/database/products.go

 ```go
// ListActiveProducts возвращает все активные платные продукты,
// отсортированные по (price_cents ASC, id ASC) — детерминированный порядок.
func (s *Service) ListActiveProducts(ctx context.Context) ([]Product, error)
 ```

 SQL: SELECT products.* FROM products JOIN plans ON plans.id = products.plan_id WHERE products.is_active = true AND products.price_cents > 0 AND plans.is_active = true ORDER BY
 products.price_cents ASC, products.id ASC.

 #### internal/database/orders.go

 ```go
// GetOrderByProviderPaymentID находит заказ по (provider, provider_payment_id).
// Использует существующий уникальный индекс idx_orders_provider_payment_unique.
// Возвращает ErrOrderNotFound если заказ не найден.
func (s *Service) GetOrderByProviderPaymentID(
    ctx context.Context, provider, providerPaymentID string,
) (*Order, error)

// UpdateOrderProviderPaymentID обновляет провайдерный ID заказа после успешного
// CreateTransaction. Возвращает ErrOrderNotFound если заказ не найден.
Защита от апдейта уже paid order: WHERE status='pending'.
func (s *Service) UpdateOrderProviderPaymentID(
    ctx context.Context, orderID uint, providerPaymentID string,
) error

// ConfirmOrderPaidCAS атомарно переводит order pending→paid и обновляет
// подписку в одной DB-транзакции. Это CAS (compare-and-set): UPDATE ... WHERE
// id=? AND status='pending'. Если RowsAffected==0 → заказ уже не pending
// (идемпотентный повтор или запрещённый переход); caller перечитывает order
// для определения нового состояния.
//
// Внутри одной транзакции выполняется:
//   - CAS order pending→paid: paid_at, activated_at, expires_at=newExpiry;
//   - UPDATE subscriptions SET expires_at=newExpiry, product_id=product.ID,
//     started_at=now (если ранее null), reminders_sent=0 WHERE id=sub.ID;
//   - ApplyPlanToSubscription (DB-setup sync: ReconcilePlanNodes +
//     MarkActiveNodesPendingUpdate) — создаёт pending_* записи subscription_nodes.
//     При ошибке вся транзакция откатывается, order остаётся pending.
//
// Возвращает (activated bool, err error). activated=true если CAS затронул строку
// (переход pending→paid фактически выполнен этим вызовом).
func (s *Service) ConfirmOrderPaidCAS(
    ctx context.Context, orderID uint,
    paidAt time.Time, activatedAt time.Time,
    sub *Subscription, newExpiry time.Time, product *database.Product,
) (bool, error)

// CancelOrderCAS атомарно переводит order в canceled по условию статуса.
// fromStatuses — допустимые исходные статусы (['pending'] для CANCELED,
// ['pending','paid'] для CHARGEBACKED). Возвращает (canceled bool, err error):
// canceled=true если переход выполнен, false если уже в целевом/запрещённом.
func (s *Service) CancelOrderCAS(
    ctx context.Context, provider, providerPaymentID string,
    fromStatuses []OrderStatus,
) (bool, error)
 ```

 │ Примечание: ApplyPlanToSubscription выполняется внутри CAS-транзакции (см. обоснование §5.5/A2). Это отличает контракт ConfirmOrderPaidCAS от обычного status-UPDATE: провал
 │ DB-setup sync откатывает всю активацию, order остаётся pending, callback вернёт 5xx, Platega повторит с нуля. Внешняя VPN-синхронизация (SyncSubscription) остаётся после
 │ commit как best-effort.

 ### 3.4 Интерфейсы (internal/interfaces/interfaces.go)

 Добавить в ProductRepository:

 ```go
ListActiveProducts(ctx context.Context) ([]database.Product, error)
 ```

 Добавить в OrderRepository:

 ```go
GetOrderByProviderPaymentID(ctx context.Context, provider, providerPaymentID string) (*database.Order, error)
UpdateOrderProviderPaymentID(ctx context.Context, orderID uint, providerPaymentID string) error
ConfirmOrderPaidCAS(ctx context.Context, orderID uint, paidAt time.Time, activatedAt time.Time, sub *database.Subscription, newExpiry time.Time, product *database.Product)
(bool, error)
CancelOrderCAS(ctx context.Context, provider, providerPaymentID string, fromStatuses []database.OrderStatus) (bool, error)
 ```

 Success-сообщение собирается в OrderService.NotifyPaidUser (см. §6.3) через SubscriptionService.GetWithTraffic + cfg.SubURL, а не в web-слое. WebRepository не расширяется
 Product/Order-доступом и не дублирует бизнес-логику.

 ### 3.5 Fakes

 Реализовать новые методы в ProductRepositoryFake, OrderRepositoryFake и большом DatabaseService fake. Fakes поддерживают явные function fields и возвращают соответствующую
 ошибку по соглашениям текущих тестов, если callback не задан.

 ────────────────────────────────────────────────────────────────────────────────

 4. Platega клиент (новый пакет)

 ### 4.1 Расположение

 internal/service/payment/platega/client.go — клиент исходящего API.
 internal/service/payment/platega/callback.go — тип входящего callback и точный разбор денежных сумм.

 ### 4.2 Структуры

 ```go
package platega

type Config struct {
    MerchantID string
    Secret     string
    BaseURL    string
    HTTPClient *http.Client
}

type Client struct{ cfg Config }

func New(cfg Config) *Client
 ```

 Defaults: BaseURL = "https://app.platega.io", HTTPClient = &http.Client{Timeout: 5 * time.Second}.

 ### 4.3 Создание транзакции

 Используется именно endpoint без выбора метода:

 ```text
POST {BaseURL}/v2/transaction/process
 ```

 Поле paymentMethod не передаётся: способ оплаты выбирает пользователь на странице Platega. Схема общего CreateTransactionRequest, где paymentMethod указан обязательным, к этому
 endpoint не применяется.

 Заголовки:

 ```text
X-MerchantId: <UUID>
X-Secret: <secret>
Content-Type: application/json
 ```

 Тело (без paymentMethod):

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

 metadata.userId всегда заполняется Telegram ID; metadata.userName — username либо пустое значение по правилам API. payload содержит внутренний ID заказа.

 Сумма хранится в БД в копейках. Публичный метод клиента принимает AmountCents int64, а JSON формируется как точное десятичное число из копеек целочисленным форматтером
 (cents/100 с padding двух знаков); прямое преобразование через float64 запрещено.

 ### 4.4 Ответ и ошибки

 Platega публикует два варианта имени ссылки: endpoint-документация показывает url, схема CreateTransactionResponse — redirect. Клиент обязан принимать оба:

 ```go
type CreateTransactionResponse struct {
    TransactionID string `json:"transactionId"`
    Status        string `json:"status"`
    URL           string `json:"url"`
    Redirect      string `json:"redirect"`
}
 ```

 Использовать URL, иначе Redirect. Если transactionId или обе ссылки пусты, ответ считается malformed и возвращается ErrProvider.

 Ошибки:

 - 400 → ErrBadRequest;
 - 401 → ErrAuth;
 - 5xx, timeout, context cancellation, malformed response, отсутствующий ID или URL → ErrProvider;
 - тело ответа ограничивается io.LimitReader (например, 1 MB).

 ```go
var (
    ErrAuth       = errors.New("platega: authentication failed")
    ErrBadRequest = errors.New("platega: bad request")
    ErrProvider   = errors.New("platega: provider error")
)
 ```

 ### 4.5 Сигнатура

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

func (c *Client) CreateTransaction(
    ctx context.Context, req CreateTransactionRequest,
) (*CreateTransactionResponse, error)
 ```

 ### 4.6 Callback payload

 ```go
// CallbackPayload — входящий callback от Platega.
// Строго обязательны id, amount, currency, status.
// Payload и PaymentMethod опциональны: приходят не во всех вариантах (схемы Platega противоречивы).
type CallbackPayload struct {
    ID            string      `json:"id"`
    Amount        json.Number `json:"amount"`
    Currency      string      `json:"currency"`
    Status        string      `json:"status"`
    PaymentMethod *int        `json:"paymentMethod,omitempty"` // nil = поле отсутствует
    Payload       string      `json:"payload,omitempty"`
}
 ```

 Разрешённые статусы Platega: PENDING, CANCELED, CONFIRMED, CHARGEBACKED. Строго обязательны только id, amount, currency, status. Поля payload и paymentMethod — опциональны и
 обрабатываются defensive: если пришли — используются, если нет — callback всё равно считается валидным.

 Обоснование: опубликованные схемы Platega противоречивы — в описании webhook чисто payload обязателен, а в схеме CallbackPayload — paymentMethod (при additionalProperties:
 false). Требование несуществующего поля заставило бы отклонять легитимные callback'и и ломало бы приём платежей (Platega ретраила бы их).

 Денежная сумма callback переводится из json.Number в копейки fixed-point parser'ом (ParseCallbackAmount). Значения с более чем двумя знаками после запятой, отрицательные, пустые
 или нечисловые значения отклоняются.

 ### 4.7 Тесты

 - httptest.Server: успешный ответ с url;
 - успешный ответ только с redirect;
 - отсутствующий URL или transaction ID;
 - 400, 401, 500, malformed JSON;
 - проверка заголовков и JSON body без paymentMethod;
 - проверка metadata и точного преобразования копеек;
 - context cancellation и timeout через Config.HTTPClient.

 ────────────────────────────────────────────────────────────────────────────────

 5. OrderService

 ### 5.1 Удаление legacy flow

 Полностью удалить старый бесплатный upgrade flow: handleUpgradePremium, handleConfirmUpgradePremium, его callbacks, промо-расчёт и связанные тесты. requestPayment также удалить
 как заглушку.

 Платный сценарий не использует старый ActivateProduct: создание pending order переносится в RequestPayment, платная активация — в ConfirmPayment. Удалить и ActivateProduct, и
 SubscriptionService.RenewSubscription — оба старых пути теряют смысл в платном потоке. Общую логику активации вынести в applyPaidSubscription.

 RenewSubscription в текущем коде вызывается только из тестов (нет production-caller вне *_test.go); ActivateProduct — единственный production-caller через
 handleConfirmUpgradePremium, который удаляется. После миграции callers удалить оба метода целиком.

 ### 5.2 Ядро активации

 applyPaidSubscription — общее ядро подтверждения платной покупки. Вычисление newExpiry, атомарный CAS order+sub, DB-setup sync выполняются в одной DB-транзакции (через
 ConfirmOrderPaidCAS); внешняя VPN-синхронизация (SyncSubscription) — после commit, best-effort.

 ```go
// applyPaidSubscription — общее ядро подтверждения платной покупки.
// Вычисляет newExpiry через calculateProductExpiry, выполняет атомарный CAS
// order pending→paid + обновление подписки + DB-setup sync в одной транзакции
// (ConfirmOrderPaidCAS). При провале CAS/DB-setup — транзакция откатывается,
// order остаётся pending, ошибка возвращается caller (callback вернёт 5xx).
// Внешняя VPN-синхронизация (SyncSubscription) выполняется после commit, best-effort.
func (o *OrderService) applyPaidSubscription(
    ctx context.Context,
    order *database.Order,
    sub *database.Subscription,
    product *database.Product,
    now time.Time,
) (newExpiry time.Time, err error)
 ```

 Внутри applyPaidSubscription:

 1. newExpiry := calculateProductExpiry(now, sub.PlanID, sub.ExpiresAt, product).
 2. activated, err := o.db.ConfirmOrderPaidCAS(ctx, order.ID, now, now, sub, newExpiry, product) — транзакция выполняет:
   - CAS order pending→paid (paid_at, activated_at, expires_at=newExpiry);
   - UPDATE subscriptions SET expires_at=newExpiry, product_id=..., started_at=now, reminders_sent=0 WHERE id=sub.ID;
   - ApplyPlanToSubscription (DB-setup sync) — внутри транзакции; провал → rollback.
 3. Возвращает newExpiry, activated, err для передачи в ConfirmPayment.

 │ Сброс reminders_sent=0 обязателен при активации/продлении подписки (иначе reminder-воркер не сработает по новой дате). Включён в транзакцию ConfirmOrderPaidCAS.

 ### 5.3 Провайдер и конструктор

 Для тестируемости OrderService зависит от минимального интерфейса создания транзакции; production-реализация — *platega.Client. Интерфейс определяется в
 internal/service/order.go (потребитель), ссылается на типы service/payment/platega (новый пакет) — циклической зависимости нет (service → service/payment/platega).

 ```go
type PaymentProvider interface {
    CreateTransaction(ctx context.Context, req platega.CreateTransactionRequest) (*platega.CreateTransactionResponse, error)
}
 ```

 ```go
type OrderService struct {
    db          interfaces.DatabaseService
    subSvc      *SubscriptionService
    syncSvc     *SyncService
    payment     PaymentProvider
    botUsername string
}

func NewOrderService(
    db interfaces.DatabaseService,
    subSvc *SubscriptionService,
    syncSvc *SyncService,
    payment PaymentProvider,
    botUsername string,
) *OrderService

// SetBotUsername устанавливает bot username после initBot/getMe,
// т.к. на момент initServices botConfig ещё не загружен.
func (o *OrderService) SetBotUsername(username string)
 ```

 payment == nil означает PAYMENT_ENABLED=false.

 При смене сигнатуры конструктора обновить все callers:

 - cmd/bot/main.go / lifecycle.go — production-wiring: создание OrderService переносится из startBackgroundWorkers в initServices (см. §8.1, §8.3). В initServices botUsername ещё
   неизвестен → передаётся "", затем SetBotUsername вызывается после initBot.
 - internal/service/order_test.go — обновить конструирование OrderService под новую сигнатуру.
 - cmd/bot/main_test.go — не вызывает NewOrderService напрямую, обновление не требуется (но сборка должна проходить).

 Перед изменением прогнать grep -rn "NewOrderService" и обновить избирательно.

 ### 5.4 RequestPayment

 ```go
func (o *OrderService) RequestPayment(
    ctx context.Context,
    telegramID int64,
    username string,
    product *database.Product,
) (*PaymentInfo, *database.Order, error)
 ```

 Алгоритм:

 1. payment == nil → ErrPaymentDisabled.
 2. telegramID <= 0, product == nil, !product.IsActive, product.PriceCents <= 0 → ошибка.
 3. Получить или создать подписку.
 4. Создать pending order с PaymentProvider="platega" и пустым ProviderPaymentID.
 5. Создать транзакцию с AmountCents, валютой продукта, payload=order.ID, Telegram metadata и https://t.me/<bot_username> в return/failedUrl.
 6. При ошибке Platega вернуть ошибку; order остаётся pending и не активирует подписку.
 7. Сохранить transactionId через UpdateOrderProviderPaymentID (WHERE status='pending' — защита от апдейта уже paid order).
 8. Вернуть provider, ID и URL (url, затем redirect).

 ### 5.5 ConfirmPayment

 ```go
type PaymentConfirmation struct {
    Order     *database.Order
    Activated bool
}

func (o *OrderService) ConfirmPayment(
    ctx context.Context,
    providerPaymentID string,
    amount json.Number,
    currency string,
) (*PaymentConfirmation, error)
 ```

 Алгоритм (порядок шагов исправлен — вычисление newExpiry внутри транзакции, см. §3.2.2):

 1. Найти заказ по (provider, provider_payment_id).
 2. Проверить валюту и точное совпадение суммы в копейках. Несовпадение → ErrAmountMismatch/ErrCurrencyMismatch (бизнес-ошибка, callback вернёт 400 без активации).
 3. Загрузить продукт заказа и подписку. Вычислить newExpiry := calculateProductExpiry(now, sub.PlanID, sub.ExpiresAt, product). Выполнить одну DB-транзакцию через
    ConfirmOrderPaidCAS:
   - атомарный переход pending → paid с условием WHERE status='pending';
   - обновление order.paid_at, order.activated_at, order.expires_at=newExpiry;
   - обновление subscriptions.expires_at=newExpiry, product_id, started_at, reminders_sent=0;
   - DB-setup sync (ApplyPlanToSubscription) внутри транзакции; провал → rollback, order остаётся pending, ошибка возвращается caller (callback → 5xx, Platega повторит с нуля).
 4. Если CAS затронул 0 строк, перечитать order: paid → идемпотентный Activated=false; canceled/expired → запрещённый переход (ErrInvalidTransition).
 5. После успешного commit выполнить внешнюю VPN-синхронизацию SyncSubscription (best-effort; ошибка логируется Warn, не отменяет commit, callback всё равно 200).
 6. Вернуть Activated=true только callback, который фактически выполнил переход в paid (CAS затронул строку). Только он имеет право отправить success-сообщение.

 Допустимые переходы:

 ```text
pending → paid
pending → canceled
pending → expired
paid → paid                 no-op (идемпотентный повтор)
paid → canceled             только CHARGEBACKED, подписка не отзывается автоматически
canceled → canceled         no-op
canceled → paid             запрещено
 ```

 │ A2-обоснование (DB-setup sync внутри транзакции): предыдущая формулировка ставила ApplyPlanToSubscription после commit. При провале order уже paid, повторный callback даёт
 │ Activated=false, sync не повторяется → subscription_nodes без pending_* → фоновый SyncPendingNodes не имеет что retry'ить → пользователь без VPN. Выполнение
 │ ApplyPlanToSubscription внутри CAS-транзакции гарантирует: либо order+sub+nodes согласованы и закоммичены, либо всё откачено и Platega повторяет с pending.

 ### 5.6 CancelPaymentByProvider

 ```go
func (o *OrderService) CancelPaymentByProvider(
    ctx context.Context,
    providerPaymentID string,
    status string,
) error
 ```

 Неизвестный order обрабатывается best-effort с Warn. CANCELED переводит только pending в canceled (CancelOrderCAS с fromStatuses=['pending']). CHARGEBACKED может перевести
 pending или paid в canceled (fromStatuses=['pending','paid']), но не отзывает подписку автоматически и логируется для ручного разбора. Повторный callback — no-op (CancelOrderCAS
 вернёт canceled=false).

 ### 5.7 Тесты

 - RequestPayment: success, disabled, invalid product, invalid Telegram ID, provider error, missing URL/ID;
 - ConfirmPayment: activation, exact amount/currency, amount mismatch, currency mismatch, not found;
 - повторный и параллельный CONFIRMED активируют только один раз;
 - продление активной подписки и сохранение orders.expires_at; order.ExpiresAt должен быть снимком newExpiry (равен *sub.ExpiresAt и не меняется при повторном CONFIRMED) —
   подробная проверка в §3.2.4;
 - CANCELED, CHARGEBACKED, запрещённые переходы и повторные callback;
 - DB-setup error (ApplyPlanToSubscription внутри транзакции падает) → транзакция откатывается, order остаётся pending, ошибка возвращается caller; external sync error
   (SyncSubscription после commit) не отменяет commit и логируется Warn;
 - reminders_sent=0 сброшен после активации/продления.

 ────────────────────────────────────────────────────────────────────────────────

 6. Webhook handler (internal/web/web.go)

 ### 6.1 Изменения Server

 ```go
type Server struct {
    // ...существующие поля...
    bot        interfaces.BotAPI
    orderSvc   *service.OrderService
    paymentCfg PaymentConfig
}

type PaymentConfig struct {
    Enabled    bool
    MerchantID string
    Secret     string
}
 ```

 Сеттеры:

 ```go
func (s *Server) SetBot(bot interfaces.BotAPI)
func (s *Server) SetOrderService(svc *service.OrderService)
func (s *Server) SetPaymentConfig(cfg PaymentConfig)
 ```

 До запуска web-сервера должны быть настроены orderSvc, bot и paymentCfg. Если зависимости не настроены, callback отвечает 503 Service Unavailable и не подтверждает оплату (см.
 §6.2, шаг 2).

 bot — жёсткая зависимость endpoint'а: если он не заведён в Server (bot == nil), endpoint отвечает 503, как и при orderSvc == nil.

 Эта жёсткость НЕ смешивается с runtime-сбоями отправки уведомления после подтверждения (см. §6.2 и §6.3): если оплата уже подтверждена (pending → paid), а bot.Send в
 notifyUserOnSuccess завершился ошибкой — заказ и подписка НЕ откатываются, callback остаётся успешным (200), ошибка логируется как best-effort (Warn).

 ### 6.2 Обработка callback

 Обработчик выполняет DB-часть синхронно и отвечает 200 только после успешной обработки. Обработка в detached goroutine после 200 запрещена: это может потерять оплату при
 рестарте процесса и отключает retry Platega.

 Алгоритм endpoint:

 1. Только POST; иначе 405 и Allow: POST.
 2. Если платежи выключены (paymentCfg.Enabled == false) или orderSvc == nil/bot == nil — 503.
 3. Проверить X-MerchantId и X-Secret. Сравнение секретов — constant-time (crypto/subtle.ConstantTimeCompare); пустые credentials невалидны. Ошибка авторизации — 401.
 4. Ограничить body через http.MaxBytesReader(..., 64*1024).
 5. Декодировать JSON с json.Decoder.UseNumber() и запретить второй JSON-документ/trailing data (decoder.More() проверка). Ошибка — 400.
 6. Проверить UUID id, непустые amount, currency, status; payload и paymentMethod — опциональны (если пришли, валидируются; paymentMethod может быть 0 — валидное значение, если
    поле присутствует). Неизвестный статус не является ошибкой формата. Отсутствие paymentMethod НЕ является ошибкой формата и не должно приводить к 400.
 7. Для CONFIRMED вызвать ConfirmPayment. Уведомить пользователя только если результат содержит Activated=true.
 8. Для CANCELED и CHARGEBACKED вызвать CancelPaymentByProvider.
 9. Для PENDING и неизвестных будущих статусов залогировать Warn и вернуть 200 без изменения заказа.
 10. Ошибки формата/валидации callback — 400. Ошибки авторизации — 401. Ошибки метода — 405. Amount/currency mismatch — 400 без активации. Временные DB/инфраструктурные ошибки
     (включая провал ApplyPlanToSubscription внутри CAS-транзакции) — 5xx, чтобы Platega повторила callback.

 После commit ошибка отправки Telegram success-message не откатывает оплату и не превращает callback в повторную активацию; она логируется как best-effort failure (Warn).

 Успешный ответ:

 ```json
{"ok":true}
 ```

 ### 6.3 notifyUserOnSuccess

 notifyUserOnSuccess отправляет новое сообщение только после фактического перехода заказа pending → paid (только при Activated=true). Повторный callback не отправляет второе
 сообщение.

 Success-сообщение собирается в OrderService.NotifyPaidUser (не в web-слое), через переиспользование presentation helpers из handleMySubscription:

 ```go
// NotifyPaidUser формирует текст success-сообщения после подтверждения оплаты.
// Переиспользует SubscriptionService.GetWithTraffic → *TrafficInfo (тот же формат
// тарифа, даты, трафика, что экран «Моя подписка») и cfg.SubURL.
// Возвращает chatID (sub.TelegramID) и готовый текст; web вызывает bot.Send.
func (o *OrderService) NotifyPaidUser(ctx context.Context, order *database.Order) (chatID int64, text string, err error)
 ```

 Сообщение должно использовать общие presentation helpers с handleMySubscription:

 - название купленного продукта;
 - тот же формат даты, что и TrafficInfo.ExpiresAtFormatted;
 - тот же формат трафика, что и экран «Моя подписка», а не отдельный упрощённый формат;
 - Config.SubURL(sub.SubscriptionID).

 WebRepository не расширяется Product/Order-доступом; web вызывает orderSvc.NotifyPaidUser и bot.Send.

 Если sub.TelegramID <= 0, подписка активируется, но сообщение не отправляется; событие логируется Warn.

 Если активация прошла, а отправка сообщения завершилась ошибкой (bot.Send вернул ошибку) — оплата и подписка НЕ откатываются; callback в любом случае отвечает 200 (активация уже
 закоммичена). Ошибка логируется как best-effort (Warn). Идемпотентность сохраняется: повторный CONFIRMED вернёт Activated=false, NotifyPaidUser не вызывается, повторно сообщение
 не отправляется.

 ### 6.4 Webhook-тесты

 - CONFIRMED подтверждает заказ и приводит к одному success-сообщению;
 - повторный и параллельный CONFIRMED не продлевают подписку и не дублируют сообщение;
 - CANCELED и CHARGEBACKED обрабатываются согласно state machine;
 - неверные merchant ID/secret → 401;
 - отключённые платежи и отсутствующие зависимости → 503;
 - неверный метод → 405;
 - malformed JSON, trailing JSON, отсутствующие обязательные поля, invalid UUID, invalid amount → 400;
 - body больше 64 KB → 400;
 - unknown status → 200, Warn, никаких действий;
 - amount/currency mismatch → 400 без активации;
 - notifyUserOnSuccess использует тот же формат тарифа, даты и трафика, что «Моя подписка»;
 - runtime-сбой отправки success-сообщения после подтверждения не откатывает оплату и не превращает callback в повторную активацию (callback отвечает 200, ошибка в Warn);
 - sub.TelegramID <= 0: заказ подтверждается и подписка активируется, сообщение не отправляется, пишется Warn;
 - провал ApplyPlanToSubscription (DB-setup sync внутри CAS) → callback 5xx, order остаётся pending (Platega повторит).

 ────────────────────────────────────────────────────────────────────────────────

 7. Telegram-бот UI

 ### 7.1 Удаление старого бесплатного flow

 Удалить полностью, без сохранения совместимых callback-веток и deprecated aliases:

 - callback buy_premium_230 и его обработку;
 - callback upgrade_premium;
 - callback confirm_upgrade_premium;
 - методы handleUpgradePremium, handleConfirmUpgradePremium и делегаты к ним;
 - freeUpgradeLabel и getFreeUpgradeLabel;
 - MainMenuBtnProductID и env MAIN_MENU_BTN_PRODUCT (поле, флаг, регистрация, validation — во всех файлах);
 - связанные сообщения MsgPremiumOffer, MsgPremiumAlready, MsgPremiumUnavailable, MsgPremiumConfirm, MsgPremiumSuccess, если после удаления flow они не используются новым
   платёжным UI;
 - связанные тесты, fixtures и проверки старой кнопки.

 Затронутые файлы (~6 исходных + ~6 тестовых): internal/config/config.go (+ config_test.go), internal/bot/handler.go, internal/bot/subscription_handler.go, internal/bot/menu.go,
 internal/bot/keyboard_builder.go, internal/bot/callback.go, internal/bot/messages.go и тесты subscription_test.go, content_test.go, handlers_extended_test.go, keyboard_test.go,
 callbacks_test.go, menu_test.go.

 В проекте не должно остаться бесплатной кнопки или callback для получения платного тарифа бесплатно. Бесплатные продукты не показываются в платном UI и не запускаются через
 OrderService.RequestPayment.

 ### 7.2 keyboard_builder.go

 MainMenu больше не принимает freeUpgradeLabel:

 ```go
func (kb *KeyboardBuilder) MainMenu(
    hasSubscription bool,
    paymentEnabled bool,
) tgbotapi.InlineKeyboardMarkup
 ```

 Логика:

 - paymentEnabled && hasSubscription → кнопка 💎 Купить Premium с callback buy_premium_list;
 - paymentEnabled == false → платёжная кнопка отсутствует;
 - бесплатных upgrade-кнопок нет ни при каком значении флага;
 - документы, помощь, донат, подписка и share-кнопка сохраняются.

 Источник флага paymentEnabled: только config.Config.PaymentEnabled. Handler хранит его один раз при конструировании (из cfg.PaymentEnabled) и передаёт в
 MainMenu(hasSubscription, paymentEnabled). BotConfig НЕ расширяется — иначе флаг можно задать расходящимся из двух источников. В KeyboardBuilder флаг приходит только как
 аргумент метода, отдельного поля не добавляется.

 Новые методы:

 ```go
func (kb *KeyboardBuilder) BuyProductList(products []database.Product) tgbotapi.InlineKeyboardMarkup
func (kb *KeyboardBuilder) BuyProductConfirm(product *database.Product, paymentURL string) tgbotapi.InlineKeyboardMarkup
 ```

 BuyProductList:

 - каждый активный платный продукт — кнопка {Name} — {price} ₽ с callback buy_product_{id};
 - в конце — ⬅️ Назад с callback back_to_start;
 - продукты с PriceCents <= 0 не отображаются даже если пришли из fake/ошибочного repository.

 BuyProductConfirm:

 - URL-кнопка 🔗 Оплатить {price} ₽;
 - ⬅️ Назад с callback buy_premium_list.

 ### 7.3 callback.go

 Использовать switch { ... } (без значения), чтобы корректно сочетать точные совпадения и prefix-проверки (прежняя запись switch data { case strings.HasPrefix(...): }
 синтаксически невалидна в Go):

 ```go
switch {
case data == "buy_premium_list":
    if err := c.h.handleBuyPremiumList(ctx, chatID, username, messageID); err != nil {
        return fmt.Errorf("handle buy_premium_list: %w", err)
    }
case strings.HasPrefix(data, "buy_product_"):
    rawID := strings.TrimPrefix(data, "buy_product_")
    productID, err := strconv.ParseUint(rawID, 10, 64)
    if err != nil || productID == 0 || productID > uint64(^uint(0)) {
        logger.Warn("callback: invalid buy_product id", zap.String("raw", rawID))
        return nil
    }
    if err := c.h.handleBuyProduct(ctx, chatID, username, messageID, uint(productID)); err != nil {
        return fmt.Errorf("handle buy_product: %w", err)
    }
}
 ```

 Поддельный callback не должен приводить к panic или запускать оплату без повторной проверки продукта в БД.

 ### 7.4 Handlers — internal/bot/subscription_handler.go

 ```go
func (sh *SubscriptionHandler) handleBuyPremiumList(ctx context.Context, chatID int64, username string, messageID int) error
func (sh *SubscriptionHandler) handleBuyProduct(ctx context.Context, chatID int64, username string, messageID int, productID uint) error
 ```

 Делегаты в handler.go должны вызывать только эти два платёжных метода.

 handleBuyPremiumList:

 1. Получить ListActiveProducts.
 2. При ошибке показать временную ошибку и Back().
 3. При пустом списке показать Нет доступных тарифов и Back().
 4. Показать список активных платных продуктов.

 handleBuyProduct:

 1. Загрузить продукт из БД.
 2. Повторно проверить IsActive и PriceCents > 0; иначе показать Тариф недоступен.
 3. Вызвать RequestPayment.
 4. При ErrPaymentDisabled показать Платежи временно недоступны; при прочей ошибке показать временную ошибку.
 5. Отредактировать текущее сообщение, показать название/цену и кнопку оплаты.
 6. После успешной оплаты пользователь получает отдельное новое сообщение с активированной подпиской.

 ### 7.5 UI-тесты

 - при PAYMENT_ENABLED=false в меню нет платёжных кнопок;
 - при PAYMENT_ENABLED=true кнопка появляется только у пользователя с подпиской;
 - бесплатные кнопки, freeUpgradeLabel, upgrade_premium, confirm_upgrade_premium и buy_premium_230 отсутствуют;
 - список содержит только активные платные продукты и сортируется по цене, затем ID;
 - inactive/free product не запускает оплату;
 - корректный и поддельный buy_product_{id} callback;
 - URL-кнопка содержит ссылку Platega;
 - back-navigation возвращает в список/главное меню без дубликатов.

 ────────────────────────────────────────────────────────────────────────────────

 8. Wiring (cmd/bot/lifecycle.go + cmd/bot/main.go)

 ### 8.1 Порядок инициализации

 Создать зависимости в порядке:

 ```text
config → database → SubscriptionService → SyncService → Platega client → OrderService → Handler/Web setters → web server → Telegram bot → workers
 ```

 OrderService нельзя создавать внутри startBackgroundWorkers: callback endpoint не должен запускаться с nil order service. Создание SyncService также переносится в initServices
 (ранее создавалось в startBackgroundWorkers).

 botUsername недоступен в initServices (нужен getMe → botConfig.Username, который загружается в initBot после initServices). Поэтому OrderService создаётся с botUsername="", а
 реальный username устанавливается через SetBotUsername после initBot (§8.3).

 ### 8.2 Platega client

 ```go
var paymentProvider service.PaymentProvider
if cfg.PaymentEnabled {
    paymentProvider = platega.New(platega.Config{
        MerchantID: cfg.PlategaMerchantID,
        Secret:     cfg.PlategaSecret,
    })
}
 ```

 ### 8.3 OrderService

 ```go
// в initServices (botUsername="" — ещё неизвестен)
orderSvc := service.NewOrderService(dbService, subService, syncSvc, paymentProvider, "")
handler.SetOrderService(orderSvc)

// после initBot (main.go, после получения botConfig.Username)
orderSvc.SetBotUsername(bc.Username)
 ```

 BotConfig не расширять полем PaymentEnabled: он содержит только metadata Telegram getMe; состояние платежей берётся из config.Config при построении keyboard/handler.

 ### 8.4 WebServer

 До запуска web-сервера:

 ```go
webServer.SetOrderService(orderSvc)
webServer.SetBot(bot) // placeholder до initBot; повторить с real api после initBot
webServer.SetPaymentConfig(web.PaymentConfig{
    Enabled:    cfg.PaymentEnabled,
    MerchantID: cfg.PlategaMerchantID,
    Secret:     cfg.PlategaSecret,
})
// после initBot:
webServer.SetBot(api)
 ```

 При PAYMENT_ENABLED=false платёжные кнопки скрыты, а /payment/callback отвечает 503; credentials не проверяются как рабочие.

 ────────────────────────────────────────────────────────────────────────────────

 9. Конфигурация webhook

 ### 9.1 Production

 Platega требует HTTPS, публичный IP/домен, валидный сертификат доверенного CA и запрещает localhost, loopback и приватные IP.

 В личном кабинете Platega указать:

 ```text
https://<your_domain>/payment/callback
 ```

 ### 9.2 Development

 Использовать ngrok/cloudflared с публичным HTTPS URL, проксирующим локальный web-сервер. Пример команды добавить в README.

 ### 9.3 Retry policy

 Platega отменяет запрос после 60 секунд без успешного ответа и выполняет до трёх повторов с интервалом 5 минут. Поэтому handler отвечает 200 только после успешной DB-обработки;
 временные ошибки (включая провал ApplyPlanToSubscription внутри CAS) отвечают 5xx.

 ────────────────────────────────────────────────────────────────────────────────

 10. Документация

 Добавить POST /payment/callback: заголовки, body, статусы, коды ответов, synchronous processing и retry policy.

 ### 10.2 README.md

 Добавить раздел «Платежи (Platega)»: env-переменные, callback URL, HTTPS/публичный домен и dev-пример.

 ### 10.3 Serena memory

 Обновить .serena/memories/subscription-nodes/orders-table.md: Platega как источник ProviderPaymentID, state transitions и поведение chargeback. Обновить architecture memory:
 payment integration.

 ────────────────────────────────────────────────────────────────────────────────

 11. Удаление старого кода

 Удалить:

 - internal/service/order.go — requestPayment, ActivateProduct, legacy free upgrade methods и связанные тесты;
 - internal/service/subscription.go — RenewSubscription (после миграции callers; общую логику вынести в applyPaidSubscription);
 - internal/web/web.go — callback-заглушку;
 - internal/bot/callback.go — buy_premium_230, upgrade_premium, confirm_upgrade_premium;
 - internal/bot/keyboard_builder.go — hardcoded Premium 230₽ и freeUpgradeLabel;
 - internal/bot/handler.go — freeUpgradeLabel, getFreeUpgradeLabel, аргументы free upgrade;
 - internal/bot/subscription_handler.go — handleUpgradePremium, handleConfirmUpgradePremium;
 - internal/bot/menu.go — getFreeUpgradeLabel использование в getMainMenuContent;
 - internal/config/config.go — MainMenuBtnProductID, MAIN_MENU_BTN_PRODUCT и их validation/tests;
 - internal/bot/messages.go — сообщения, использовавшиеся только удалённым бесплатным flow;
 - все связанные тесты, fixtures и упоминания в subscription_test.go, content_test.go, handlers_extended_test.go, keyboard_test.go, callbacks_test.go, menu_test.go,
   config_test.go.

 После cleanup поиск по репозиторию не должен находить buy_premium_230, freeUpgradeLabel, getFreeUpgradeLabel, upgrade_premium, confirm_upgrade_premium, MainMenuBtnProductID или
 MAIN_MENU_BTN_PRODUCT, ActivateProduct, RenewSubscription.

 ────────────────────────────────────────────────────────────────────────────────

 12. Acceptance criteria

 1. PAYMENT_ENABLED=false — бот стартует без PLATEGA_*, платёжных кнопок нет, webhook отвечает 503.
 2. PAYMENT_ENABLED=true без PLATEGA_* — config.validate() завершается понятной ошибкой.
 3. PAYMENT_ENABLED=true — кнопка «Купить Premium» есть только у пользователей с подпиской.
 4. Бесплатные кнопки и бесплатный upgrade flow полностью удалены.
 5. В списке отображаются только active paid products (is_active=true, price_cents > 0, active plan), порядок: price_cents ASC, id ASC.
 6. Выбор продукта создаёт pending order и транзакцию Platega без paymentMethod.
 7. Ответ Platega с url и ответ только с redirect поддерживаются.
 8. Отсутствующий transaction ID или URL отклоняется как provider error.
 9. Валидный CONFIRMED переводит order в paid и активирует подписку атомарно (CAS, WHERE status='pending').
 10. Только callback, совершивший переход pending→paid, отправляет новое сообщение с subURL.
 11. Повторный и параллельный CONFIRMED не продлевают подписку и не дублируют сообщение.
 12. Дата, тариф и трафик в success-сообщении используют те же presentation helpers, что «Моя подписка» (через OrderService.NotifyPaidUser).
 13. При покупке на активной подписке newExpiry = currentExpiry + DurationDays согласно calculateProductExpiry.
 14. order.ExpiresAt — снимок newExpiry (равен *sub.ExpiresAt), не меняется при повторном CONFIRMED.
 15. reminders_sent=0 сбрасывается при активации/продлении подписки.
 16. Провал DB-setup sync (ApplyPlanToSubscription) откатывает транзакцию: order остаётся pending, callback → 5xx, Platega повторяет.
 17. MAIN_MENU_BTN_PRODUCT и связанные поля/флаги полностью удалены.
 ```

---

Это полное исправленное ТЗ v1.3. Сводка внесённых изменений относительно v1.2:

**Исправленные ошибки (A1–A8):**
- **A1** §5.5: порядок шагов — `newExpiry` вычисляется и пишется внутри транзакции, не после.
- **A2** §5.5/§3.3: `ApplyPlanToSubscription` выполняется **внутри** CAS-транзакции; провал → rollback, 5xx, повтор Platega.
- **A3** §5.1/§11: удалить и `ActivateProduct`, и `RenewSubscription`; общая логика в `applyPaidSubscription`.
- **A4** §7.3: переписан на валидный `switch { }`.
- **A5** §5.3: уточнён список callers (`main_test.go` не трогать; создание перенести в `initServices`).
- **A6** §8.3: добавлен `SetBotUsername` setter (botUsername недоступен в `initServices`).
- **A7** §6.3: `notifyUserOnSuccess` собирается в `OrderService.NotifyPaidUser`; `WebRepository` не расширяется.
- **A8** §3.2.3: отмечен битый пробел в комментарии миграции.

**Дополнения (B1–B9 кроме B5):**
- **B1** §3.3: новый метод `ConfirmOrderPaidCAS` (CAS pending→paid атомарно с sub).
- **B2** §3.3: `UpdateOrderProviderPaymentID` с `WHERE status='pending'`.
- **B3** §3.3: новый метод `CancelOrderCAS`.
- **B4** §7.1/§11: расширенный список затронутых файлов (~12).
- **B6** §5.2/§5.5: сброс `reminders_sent=0` внутри транзакции (критерий 15).
- **B7** §5.3: место определения `PaymentProvider` зафиксировано (без циклической зависимости).
- **B8** §5.3: `main_test.go` подтверждён как не-caller.
- **B9** §5.2: `applyPaidSubscription` как общий знаменатель.
