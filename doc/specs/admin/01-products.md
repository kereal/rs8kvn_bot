# 01 — Продукты

Эталон шаблона: остальные ТЗ ссылаются на этот документ по структуре.

## 1. Цель и границы

Экран управляет каталогом: какие тарифы продаются, на сколько дней, за сколько.
Продукт — это «план × срок × цена», покупка создаёт заказ и привязывает
подписку к плану.

Не входит: конструктор цен, промо, скидки, автопродление, правка цен
существующих подписок (у них `price_paid_cents` — исторический факт).

## 2. Данные

```sql
CREATE TABLE products (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
  duration_days INTEGER NOT NULL,
  price_cents INTEGER NOT NULL,
  currency CHAR(3) NOT NULL DEFAULT 'RUB',
  is_active INTEGER NOT NULL DEFAULT 1,
  name VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (plan_id, duration_days)
);
```

Факты, на которые опирается реализация:

- `plan_id` каскадно удаляется вместе с планом (уже учтено guard'ом планов);
- уникальность — пара (`plan_id`, `duration_days`), не имя;
- `orders.product_id` ссылается на продукт **без** `ON DELETE`, поэтому
  удаление продукта с заказами упадёт на FK — это перехватывается guard'ом;
- `subscriptions.product_id` тоже ссылается на продукт.

Существующие методы: `ListActiveProducts`, `GetProductByID`,
`UpdateProductGuarded` (гард на смену `plan_id`/`duration_days` при активных
подписках — переиспользовать, не дублировать).

## 3. Права и guard'ы

| Операция | Условие отказа | Код |
|---|---|---|
| create | план не существует | `400 validation` `details.plan_id` |
| create | `duration_days` вне 1..3650 | `400 validation` `details.duration_days` |
| create | `price_cents` < 0 | `400 validation` `details.price_cents` |
| create | `name` длиннее 100 символов | `400 validation` `details.name` |
| create | пара (`plan_id`, `duration_days`) занята | `409 name_taken`, `details.field=duration_days` |
| update | `plan_id` отличается от текущего | `409 protected`, `details.reason="plan_id_immutable"` |
| update | `duration_days` меняется при активных подписках | `409 in_use`, `details.subscriptions=N` |
| delete | есть подписки | `409 in_use`, `details.subscriptions=N` |
| delete | есть заказы | `409 in_use`, `details.orders=N` |
| delete | обе причины | `409 in_use`, оба счётчика |

Смена `plan_id` запрещена сознательно: подписка ссылается и на продукт, и на
план, перенос продукта между планами создал бы противоречивую пару. Правильный
путь — удалить продукт и создать заново.

## 4. API

| Method | Path | Query/Body | Ответ |
|---|---|---|---|
| GET | `/admin/api/products` | `?q=&plan_id=&active=true\|false` | `{"items":[…],"total":N}` |
| POST | `/admin/api/products` | product | `201` + продукт |
| PUT | `/admin/api/products/{id}` | product | `200` + продукт |
| DELETE | `/admin/api/products/{id}` | — | `200` + удалённый продукт |

Полный список без пагинации: продуктов десятки.

DTO ответа:

```json
{
  "id": 4,
  "plan_id": 3,
  "plan_name": "premium",
  "name": "30 дней",
  "duration_days": 30,
  "price_cents": 39900,
  "currency": "RUB",
  "is_active": true,
  "subscriptions_count": 12,
  "orders_count": 31,
  "created_at": "2026-10-01T10:00:00Z",
  "updated_at": "2026-10-02T11:30:00Z"
}
```

Тело запроса: `{"plan_id":int,"name":str,"duration_days":int,"price_cents":int,"currency":str,"is_active":bool}`.
`currency` — три заглавные латинские буквы, по умолчанию `RUB`.
`price_cents` хранит minor units выбранной валюты: для `RUB` это копейки
(1 ₽ = 100), для `XTR` — целые Stars (1 Star = 1). Валюты в одном поле
не смешивают; конверсия выполняется только в `admin/src/lib/format.js`.

`subscriptions_count` / `orders_count` считаются одним запросом через
`LEFT JOIN (SELECT … GROUP BY product_id)`; N+1 не допускается.

## 5. Сервер

Файл `internal/database/products_admin.go`:

```go
type ProductOverview struct {
    Product
    PlanName          string
    SubscriptionsCount int64
    OrdersCount       int64
}

func (s *Service) ListProductOverviews(ctx context.Context) ([]ProductOverview, error)
func (s *Service) CreateProduct(ctx context.Context, p *Product) error
func (s *Service) UpdateProductAdmin(ctx context.Context, p *Product) error
func (s *Service) DeleteProduct(ctx context.Context, id uint) error
```

Sentinel-ошибки: `ErrProductInvalid`, `ErrProductNameTaken` (пара
`plan_id`+`duration_days`), `ErrProductProtected` (смена `plan_id`),
`ErrProductInUse` (несущий `Details map[string]int64` со счётчиками).

Правила реализации:

- валидация в репозитории, не только в хендлере: экран — не единственный
  возможный вызывающий;
- `DeleteProduct`: сначала посчитать подписки и заказы, затем удалить; гонка не
  критична (единственный админ), но FK-ошибку всё равно маппить в `in_use`;
- тексты ошибок — английские, коды и русские тексты живут в `format.js`;
- `UpdateProductAdmin` переиспользует `UpdateProductGuarded` для проверки
  активных подписок, затем обновляет остальные поля.

## 6. Аудит

Новые конструкторы в `internal/database/audit_log.go`:

| Событие | Константа | old/new state |
|---|---|---|
| создан | `AuditEventProductCreated = "product_created"` | `nil` → снимок |
| изменён | `AuditEventProductUpdated = "product_updated"` | снимок → снимок |
| удалён | `AuditEventProductDeleted = "product_deleted"` | снимок → `nil` |

`entity_type = EntityProduct`, `entity_id = id`. Аудит пишется после успешной
мутации, best-effort (`insertAuditLogBestEffort`), ошибка логируется `Warn`.

## 7. UI

`admin/src/views/Products.svelte`.

Таблица: `ID · НАЗВАНИЕ · ПЛАН · СРОК · ЦЕНА · СТАТУС · ПОДПИСКИ · ДЕЙСТВИЯ`.

- название пустое → показывать `«30 дней»` (срок), а не пустую ячейку;
- срок — «30 дней» / «1 день» (плюрализация в `format.js`);
- цена — `formatMoney(price_cents, currency)`, ноль → «бесплатно»;
- срок — `formatDays(duration_days)` (хелпер уже есть, не дублировать);
- статус — бейдж «в продаже» / «скрыт»;
- подписки — число; при 0 серым.

Панель фильтров: поиск по названию (клиентский), select «все планы / <план>»,
чекбокс «только активные».

Модалка создания/правки (`<dialog>`, как в `Plans.svelte`): план (select,
disabled при правке), название, срок, цена, валюта, активность. Единица ввода
зависит от валюты: `RUB` — рубли с двумя знаками (`Math.round(value * 100)` в
API), `XTR` — целые Stars без масштабирования. Подпись поля: «Цена (₽)» или
«Цена (Stars)».

Удаление: confirm; если сервер ответил `in_use`, показать текст с числами
(«нельзя удалить: 12 подписок, 31 заказ») и оставить строку.

Состояния: загрузка (skeleton или «Загрузка…»), пусто («Пока нет продуктов» +
кнопка создания), ошибка (текст кода из `format.js` + «Повторить»).

## 8. Тесты

`internal/database/products_admin_test.go` — таблица кейсов:

| Кейс | Ожидание |
|---|---|
| create валидный | строка есть, поля совпадают, аудит `product_created` |
| create дубль пары | `ErrProductNameTaken` |
| create `duration_days=0` / `3651` | `ErrProductInvalid` |
| create несуществующий план | `ErrProductInvalid` |
| update цены | цена изменилась, аудит `product_updated` со старым `price_cents` |
| update `plan_id` | `ErrProductProtected` |
| update срока при активной подписке | `ErrProductInUse` |
| delete без связей | строка удалена, аудит `product_deleted` |
| delete с подпиской | `ErrProductInUse`, `Details["subscriptions"]==1` |
| delete с заказом | `ErrProductInUse`, `Details["orders"]==1` |

`admin/products_test.go` — HTTP: `200` список с `total`, `201` создание, `400`
на `duration_days=0`, `401` без токена, `404` на чужой id, `409` на дубль и на
удаление с зависимостями, отказ на неизвестное поле в теле.

## 9. Критерии приёмки

- [x] Список показывает `subscriptions_count` и `orders_count` без N+1 (один SQL). — проверено: products_admin.go:75-89 (один SQL, подзапросы COUNT + JOIN plans); TestListProductOverviews_CountsInSingleRow.
- [x] Дубль пары `(plan_id, duration_days)` даёт `409`, а не `500`. — проверено: products_admin.go:290-292 (UNIQUE → ErrProductNameTaken), admin/products.go:272-277 (409 name_taken); TestAPI_ProductsCreateDuplicatePairReturns409.
- [x] Удаление продукта с подписками и с заказами блокируется с числами. — проверено: products_admin.go:181-232 (транзакция, счётчики → ProductInUseError), admin/products.go:283-301 (409 in_use); TestDeleteProduct_RejectsWithSubscription/WithOrder, TestAPI_ProductsDeleteWithDependenciesReturns409.
- [x] Смена плана у продукта отклоняется с `409 protected`. — проверено: products_admin.go:138-140 (ErrProductProtected), admin/products.go:278-283 (409 protected, plan_id_immutable); TestAPI_ProductsPlanChangeIsProtected.
- [x] Все мутации оставляют запись в `audit_log` с `actor_type=admin`, `actor_id=0`. — проверено: products_admin.go:119,174,234 → audit_log.go:987-1023 (ActorAdmin, ActorID "0"); TestCreateProduct_CreatesRowAndAudit, TestUpdateProductAdmin_ChangesPriceAndAuditsOldPrice, TestDeleteProduct_DeletesWithoutReferencesAndAudits.
- [x] Консоль браузера чистая после create/update/delete. — проверено вживую на собранной панели (tools/adminserve -seed, headless Chromium, 2026-11-05): create 123 ₽ → update 777 ₽ → delete, все ответы 200, tab.console() и tab.errors() пусты после каждой операции.
- [x] `go test -race ./admin/` зелёный, `golangci-lint` без замечаний. — проверено: прогнано 2026-11-05: go test -race ./admin/ ./internal/database/ ./internal/bot/ ./cmd/bot/ — ok (74.5s); golangci-lint run ./... — 0 issues.

## 10. Файлы

Создаёт:

- `internal/database/products_admin.go`
- `internal/database/products_admin_test.go`
- `admin/products.go`
- `admin/products_test.go`
- `admin/src/views/Products.svelte`

Правит одной строкой: `routes` и меню в `admin/src/App.svelte`,
`mux.HandleFunc` в `Handler()` (`admin/server.go`).
Добавляет свои конструкторы в `internal/database/audit_log.go`.

Не трогает: `admin/src/lib/*`, `admin/dist/*`, `internal/service/*`.

## 11. Вне скоупа

- массовые операции над продуктами (bulk edit, копирование тарифа);
- история изменения цен (только аудит);
- привязка продукта к нескольким планам;
- промокоды и скидки;
- пагинация и серверная сортировка.
