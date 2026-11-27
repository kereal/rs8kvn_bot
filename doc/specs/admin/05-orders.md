# 05 — Заказы (только чтение)

Эталон структуры: [01-products.md](01-products.md). Общие правила:
[00-common.md](00-common.md), контракт: [ADR-0002](../../adr/0002-admin-panel-contract.md).

## 1. Цель и границы

Экран для разбора платежей: найти заказ по пользователю или
`provider_payment_id`, увидеть суммы (включая комиссию провайдера и фактически
списанное), даты и ссылку на подписку. `orders` — растущая таблица, пагинация
обязательна (P7).

**Экран строго read-only.** Ни одной мутации, ни одного POST/PUT/DELETE —
включая «пометить оплаченным» и «отменить»: `orders.status` — поле state
machine, его двигают только `ConfirmOrderPaidCAS` / `ExpirePendingOrder` /
`CancelOrderCAS` в `internal/database/orders.go` (P17). Панель без Payments
API платёжки не может корректно подтвердить оплату, а ручная правка статуса
сломала бы синхронизацию с провайдером и аудит транзакций.

Не входит: правка статусов, повторная отправка платёжной ссылки, refund/chargeback
из панели, статистика и выгрузки, просмотр платежей вне заказов.

## 2. Данные

Фактическая схема — миграция `034_orders_subscription_cascade.up.sql`
(пересоздала таблицу с каскадом и индексами):

```sql
CREATE TABLE orders (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  subscription_id INTEGER NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
  product_id INTEGER NOT NULL REFERENCES products(id),
  status TEXT NOT NULL CHECK (status IN ('pending','paid','expired','canceled')),
  amount_cents INTEGER NOT NULL CHECK (amount_cents >= 0),
  currency CHAR(3) NOT NULL DEFAULT 'RUB',
  payment_provider TEXT,
  provider_payment_id TEXT,
  created_at DATETIME NOT NULL,
  paid_at DATETIME,
  activated_at DATETIME,
  expires_at DATETIME,
  payment_url TEXT,
  payment_expires_at DATETIME,
  payment_creation_uncertain BOOLEAN NOT NULL DEFAULT FALSE
);
```

Колонки `callback_amount_cents INTEGER` и `provider_fee_cents INTEGER`
(nullable) добавлены моделью/`SaveOrderPaymentAmounts` (см. `models.go:211-212`):
фактически списанная сумма и комиссия провайдера.

Факты, на которые опирается реализация:

- индексы: `idx_orders_subscription_id`, `idx_orders_status`,
  `idx_orders_created_at`, `idx_orders_product_id`,
  `idx_orders_provider_payment_unique (payment_provider, provider_payment_id) WHERE …`;
- колонки `updated_at` **нет** (P14 про свежесть не применим — строка
  фактически неизменяемая, свежесть видна по `created_at`/`paid_at`);
- каскад `subscription_id ON DELETE CASCADE`: после `/del` заказы исчезают
  вместе с подпиской — «осиротевших» заказов не бывает;
- `products` не удаляются при наличии заказов (`ErrProductInUse` в ТЗ 01),
  `products.plan_id` тоже жив — JOIN по всей цепочке всегда разрешается;
- `subscriptions.telegram_id` у trial-подписок отрицательный; в поиске учесть
  (точное совпадение по цифрам, минус — допустимый символ).

Существующие методы (`internal/database/orders.go`): `GetOrderByID`,
`GetOrdersBySubscriptionID`, `FindPendingPaymentOrder` и платежные CAS-методы —
все предметные, для списка с фильтрами не годятся и **не дублируются**:
экрану нужен один новый метод (§5).

## 3. Права и guard'ы

| Операция | Условие отказа | Код |
|---|---|---|
| list | `status` вне набора из 4 значений | `400 bad_request` |
| list | `sort` вне белого списка | `400 bad_request` (P8) |
| list | `order` не `asc`/`desc` | `400 bad_request` (P8) |
| list | `offset` < 0 | `400 bad_request` |
| list | `from`/`to` не `YYYY-MM-DD` | `400 bad_request` |
| list | `limit` > 200 | clamp до 200, не ошибка (P7) |
| detail | заказа нет | `404 not_found` (`ErrOrderNotFound`) |

Мутаций нет — guard'ов на запись не существует по определению экрана.
`writeStoreError` (`admin/plans.go`) дополняется одним кейсом
`errors.Is(err, database.ErrOrderNotFound)` → `404 not_found`; sentinel уже
существует (`models.go:21`), новый не вводится.

## 4. API

| Method | Path | Query | Ответ | Ошибки |
|---|---|---|---|---|
| GET | `/admin/api/orders` | `?limit=&offset=&status=&q=&from=&to=&sort=&order=` | `{"items":[…],"total":N}` | `400`, `401`, `500` |
| GET | `/admin/api/orders/{id}` | — | `200` + детальный DTO | `401`, `404`, `500` |

Параметры списка:

- `limit` — по умолчанию 50, максимум 200 (clamp); `offset` — по умолчанию 0;
- `status` — `pending|paid|expired|canceled`, пусто = все;
- `q` — если строка из цифр (минус допустим): точное совпадение
  `subscriptions.telegram_id` **или** `provider_payment_id LIKE '%q%'`;
  иначе — только `provider_payment_id LIKE '%q%'`;
- `from` / `to` — дата `YYYY-MM-DD` (инпут `type="date"`), окно по
  `created_at` UTC: `from` — начало дня включительно, `to` — конец дня
  включительно;
- `sort` — белый список: `id`, `created_at` (по умолчанию), `paid_at`,
  `amount_cents`; `order` — `asc|desc`, по умолчанию `desc`;
- `total` **обязателен** и считается по тому же WHERE, что и `items` (§5).

DTO списка (поле-в-поле):

```json
{
  "id": 1042,
  "subscription_id": 42,
  "telegram_id": 123456789,
  "plan_name": "premium",
  "product_name": "30 дней",
  "duration_days": 30,
  "status": "paid",
  "amount_cents": 39900,
  "currency": "RUB",
  "payment_provider": "platega",
  "provider_payment_id": "8f3c…",
  "created_at": "2026-11-01T10:00:00Z",
  "paid_at": "2026-11-01T10:05:12Z",
  "activated_at": "2026-11-01T10:05:13Z",
  "payment_creation_uncertain": false,
  "callback_amount_cents": 38703,
  "provider_fee_cents": 1197
}
```

`callback_amount_cents` / `provider_fee_cents` — `null`, если ещё не записаны.
Детальный DTO добавляет: `product_id`, `expires_at`, `payment_url`,
`payment_expires_at`. `payment_url` — не секрет (P16 его не покрывает) и нужен
админу для ручной сверки; отдаётся только в детали.

## 5. Сервер

Файл `internal/database/orders_admin.go`:

```go
// OrderAdminFilter — фильтры экрана «Заказы». Zero value = без фильтра.
// Валидация строковых полей — в хендлере (белые списки P8), здесь — только SQL.
type OrderAdminFilter struct {
    Status string     // "" | pending | paid | expired | canceled
    Query  string     // telegram_id (цифры) или provider_payment_id
    From   *time.Time // окно created_at, from включительно
    To     *time.Time // to включительно (конец дня, см. §4)
    Sort   string     // id | created_at | paid_at | amount_cents
    Desc   bool
    Limit  int
    Offset int
}

// OrderOverview — заказ с denormalized полями для панели.
type OrderOverview struct {
    Order
    TelegramID   int64  // из subscriptions (JOIN)
    PlanName     string // из products -> plans (JOIN)
    ProductName  string // из products (JOIN)
    DurationDays int    // из products (JOIN)
}

// Единый query builder: WHERE строится в одном месте и используется
// и для COUNT, и для SELECT — фильтры total и items не могут разъехаться.
func buildOrderAdminQuery(s *Service, ctx context.Context, f OrderAdminFilter) *gorm.DB

func (s *Service) ListOrderOverviews(ctx context.Context, f OrderAdminFilter) (items []OrderOverview, total int64, err error)
func (s *Service) GetOrderOverview(ctx context.Context, id uint) (*OrderOverview, error)
```

Правила реализации:

- **Единый query builder.** `buildOrderAdminQuery` возвращает свежую цепочку
  `s.db.WithContext(ctx).Model(&Order{})` с условиями (status, q, окно дат) и
  JOINs. `ListOrderOverviews` вызывает его **дважды** — один раз для
  `Count(&total)`, один раз для `Order(sort).Limit().Offset().Find(&items)` —
  вместо повторного использования одного `*gorm.DB` (повторный терминальный
  вызов на той же цепочке в GORM наследует накопленные clauses; два свежих
  вызова детерминированы). WHERE описан один раз: рассинхрон фильтров
  невозможен по построению.
- JOINs — `LEFT JOIN subscriptions` (telegram_id), `LEFT JOIN products`
  (name, duration_days), `LEFT JOIN plans ON products.plan_id = plans.id`
  (name → `plan_name`). Один SQL на items, один на total, N+1 запрещён
  (требование ТЗ 01 §4 то же).
- Поиск `q`: если значение состоит из цифр (со знаком) —
  `(CAST(subscriptions.telegram_id AS TEXT) = ? OR orders.provider_payment_id LIKE ?)`
  (равенство использует `idx_subscriptions_telegram_id`), иначе — только
  `LIKE` по `provider_payment_id`. `LIKE` без префиксного индекса на
  растущей, но низкооборотной таблице приемлем; индекс не добавлять.
- `GetOrderOverview` при отсутствии строки возвращает `ErrOrderNotFound`
  (существующий sentinel) — хендлер маппит в `404`.
- Хендлеры в `admin/orders.go`: узкий интерфейс `OrderStore` с двумя методами
  выше (образец — `PlanStore` в `admin/server.go:38`), парсинг query в хендлере
  (белые списки, clamp limit), DTO-структуры с json-тегами в `admin/orders.go`
  (P24: модель БД в JSON не отдаётся).

## 6. Аудит

**Не пишется.** Экран read-only: мутаций нет — писать нечего (P20 пишет аудит
только на мутации). Все события заказов (`order_created`,
`payment_confirmed`, `order_expired`, `order_canceled`,
`payment_creation_uncertain`, `chargeback_received`) уже существуют в
`internal/database/audit_log.go` и пишутся ботом/платёжными транзакциями;
панель их не добавляет и новых конструкторов не требует.

## 7. UI

`admin/src/views/Orders.svelte` (список) и `admin/src/views/OrdersDetail.svelte`
(`#/orders/{id}`).

Список: `ID · ПОЛЬЗОВАТЕЛЬ · ПРОДУКТ · ПЛАН · СТАТУС · СУММА · СПИСАНО ·
ПРОВАЙДЕР · СОЗДАН · ОПЛАЧЕН`.

- пользователь: `@telegram_id` моноширинно, кликабельно → детальная страница
  подписки (ТЗ 04), если подписка ещё жива (каскад: заказы без подписки не
  существуют);
- статус — бейдж: `pending` жёлтый, `paid` зелёный, `expired` серый,
  `canceled` красный; словарь статусов — **локальная константа во вью**
  (`pending → «Ожидает оплаты»`, `paid → «Оплачен»`, `expired → «Истёк»`,
  `canceled → «Отменён»`), не в `format.js` (форматирование сущности — не
  забота общего файла; см. ТЗ 07 §7);
- сумма: `formatMoney(amount_cents, currency)` из `format.js`; `RUB` — копейки,
  `XTR` — целые Stars, конвертация только в UI (P15);
- списано: `callback_amount_cents`; `null` → «—»; если отличается от
  `amount_cents` — подсветка (комиссия провайдера);
- фильтры: select статуса, два инпута `type="date"` (от/до), поиск
  (debounce 300 мс, серверный; плейсхолдер «telegram_id или ID платежа»);
- счётчик «найдено N» над таблицей — из `total`;
- пагинация: «50 из 1240», кнопки «‹ ›», select на 50/100/200 (как в ТЗ 04).

Детальная страница `#/orders/{id}`: полный набор полей карточками —
«Платёж» (status, amount, callback_amount, provider_fee, provider,
provider_payment_id, payment_url с кнопкой «Открыть», payment_expires_at),
«Даты» (created, paid, activated, expires), «Продукт» (product_name,
plan_name, duration_days), ссылка «Подписка #{subscription_id}» →
`#/subscriptions/{subscription_id}` (ТЗ 04).

Маршрут `#/orders/{id}` уже поддержан общим роутером в `App.svelte`:
хеш разбирается на имя экрана и id, id приходит во вью пропом
`let { id } = $props()`. Экран не правит роутер — только добавляет строку в
`routes` (§10).

Состояния (P27): загрузка, пусто («Заказы не найдены» + сброс фильтров),
ошибка (код из `format.js` через `humanError` + «Повторить»), бейдж
«только чтение». Кнопка «Обновить», никакого polling (P28).

## 8. Тесты

`internal/database/orders_admin_test.go` — таблица кейсов на реальной SQLite
через `testutil.NewTestDatabaseService` (P31, фейки БД не пишутся):

| Кейс | Ожидание |
|---|---|
| список без фильтров | `total` == число строк, порядок `created_at desc` |
| фильтр по статусу | только заказы статуса, `total` == len(items) — единый WHERE |
| поиск по telegram_id (цифры) | только заказы этой подписки |
| поиск по provider_payment_id (частичный) | LIKE находит |
| окно from/to | только заказы внутри дня UTC, границы включительно |
| пагинация offset/limit | items не пересекаются, `total` неизменен |
| clamp limit=1000 | SQL получает 200 |
| join: план/продукт | `plan_name`/`product_name`/`duration_days` из цепочки |
| сортировка amount_cents asc | порядок по сумме |
| деталь несуществующего | `ErrOrderNotFound` |

`admin/orders_test.go` — HTTP: `200` список с `total`, `200` деталь, `400` на
`status=foo`, `400` на `sort=evil`, `400` на `offset=-1`, `400` на
`from=2026-13-99`, `401` без токена, `404` на несуществующий id, clamp
`limit=1000` → 200 записей максимум. Ручной чек-лист UI — в
`admin/AGENTS.md` (P30).

## 9. Критерии приёмки

- [x] В `admin/` и `internal/database/orders_admin.go` нет ни одного INSERT/UPDATE/DELETE по `orders` и ни одного метода, кроме двух read-методов (проверяется грепом в ревью). Поверхность: `ListOrderOverviews` (`orders_admin.go:99`), `GetOrderOverview` (`:131`), интерфейс `OrderStore` (`admin/orders.go:14`).
- [x] `total` и `items` строятся из одного `buildOrderAdminQuery` — фильтр статуса одновременно меняет и список, и счётчик (тест-кейс «единый WHERE»). `orders_admin.go:44-76`, count `:105`, select `:115`; `TestListOrderOverviews_StatusFilter_UniformWhere`.
- [x] `limit=1000` отдаёт максимум 200 строк и `total` реальный, а не clamp-нутый. `admin/orders.go:121`, `TestAPI_OrdersList_ClampsLimit`.
- [x] Поиск по цифрам находит и по `telegram_id`, и по `provider_payment_id`. `orders_admin.go:60-63`, `TestListOrderOverviews_QueryByTelegramID`, `_QueryByProviderPaymentID`.
- [x] Список из 10 000 заказов — два SQL (count + select), без N+1. Ровно два терминальных вызова (`Count` + `Scan`), JOIN денормализует строку; нагрузочного прогона на 10 000 нет, вывод из структуры запроса.
- [x] Детальная страница содержит ссылку на подписку; при переходе открывается ТЗ 04. `OrdersDetail.svelte:114` → `#/subscriptions/{id}`, роут `App.svelte:29`.
- [x] `writeStoreError` маппит `ErrOrderNotFound` в `404`, а не `500`. `admin/plans.go:150`, `TestAPI_OrdersDetail_NotFound`.
- [x] `go test -race ./admin/ ./internal/database/` зелёный, `golangci-lint` без замечаний. — проверено: прогнано 2026-11-05: go test -race ./admin/ ./internal/database/ ./internal/bot/ ./cmd/bot/ — ok (74.5s); golangci-lint run ./... — 0 issues.

## 10. Файлы

Создаёт:

- `internal/database/orders_admin.go`
- `internal/database/orders_admin_test.go`
- `admin/orders.go`
- `admin/orders_test.go`
- `admin/src/views/Orders.svelte`
- `admin/src/views/OrdersDetail.svelte`

Правит:

- `admin/server.go` — один `mux.HandleFunc` в `Handler()` для каждого маршрута
  (одной строкой);
- `admin/src/App.svelte` — по одной строке: `routes` и меню; плюс общий разбор
  вложенного пути в `routeFromHash` (если ещё не сделан ТЗ 04 — координировать,
  механизм один на все экраны);
- `admin/plans.go` — один кейс `ErrOrderNotFound` в `writeStoreError`.

Не трогает: `internal/database/orders.go` (платёжные CAS-методы),
`internal/database/audit_log.go`, `admin/src/lib/*` (`formatMoney` добавляет
ТЗ 01), `admin/dist/*`, `internal/service/*`, ADR-0002.

## 11. Вне скоупа

- любые мутации заказов, включая «пометить оплаченным» и «отменить»;
- повторная выдача платёжной ссылки;
- выгрузка CSV/статистика по платежам;
- агрегаты (сумма продаж за период) — только по запросу;
- фильтр по `product_id` / `payment_provider` (нужны — добавить в один
  query builder, структура это допускает);
- вебхуки и просмотр сырых callback-ов провайдера.



