# 04 — Подписки (чтение + три операции)

## 1. Цель и границы

Экран для разбора обращений: найти подписку, посмотреть её состояние, при
необходимости продлить/сменить план, отозвать доступ или удалить строку.

Три операции — **через сервисный слой**, панель никогда не пишет в таблицу
`subscriptions` напрямую (ADR-0002 §6):

| Операция | Вызов |
|---|---|
| Продлить / сменить план | `SubscriptionService.AdminSetPlan(ctx, subID, planID, days)` — существует |
| Отозвать без удаления | `SubscriptionService.RevokeSubscription(ctx, subID)` — **новый** |
| Удалить | `SubscriptionService.DeleteByID(ctx, subID)` — существует (revoke + deprovision + delete) |

Не входит: создание подписок, привязка триала, перевыпуск ключа, ручная правка
`devices`/`ips`, изменение `status` напрямую.

## 2. Данные

```sql
CREATE TABLE subscriptions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  telegram_id BIGINT NOT NULL,
  username VARCHAR(255),
  client_id VARCHAR(255),
  subscription_id VARCHAR(255),
  status VARCHAR(50) NOT NULL DEFAULT 'active'
    CHECK (status IN ('active','expired','paused','canceled','revoked')),
  created_at DATETIME, updated_at DATETIME,
  invite_code VARCHAR(16), referred_by BIGINT,
  plan_id INTEGER REFERENCES plans(id),
  devices TEXT NOT NULL DEFAULT '[]',
  ips TEXT NOT NULL DEFAULT '[]',
  expires_at DATETIME,
  product_id INTEGER REFERENCES products(id),
  started_at DATETIME,
  price_paid_cents INTEGER NOT NULL DEFAULT 0,
  currency CHAR(3),
  last_request DATETIME,
  reminders_sent INTEGER NOT NULL DEFAULT 0,
  traffic_reminders_sent INTEGER NOT NULL DEFAULT 0
);
```

Связанные: `plans(name, traffic_limit, devices_limit)`, `products(duration_days)`,
`subscription_nodes(subscription_id, node_id, status)`.

Факты реализации, важные для экрана:

- `devices` и `ips` — JSON-массивы в TEXT, пустой `[]`;
- `subscription_id` — публичный идентификатор подписки (используется в `/sub/{id}`);
- `revoked` — не только флаг: подписка отдаёт 404 на субсервере, а VPN-клиенты
  депровижинятся (`markRevokedSub` + `deprovisionSub` в `subscription.go`);
- `AdminSetPlan` уже пишет аудит `plan_changed` и вызывает
  `ApplyPlanToSubscription` + `SyncSubscription`.

## 3. Права и guard'ы

Чтение — без ограничений. Мутации:

| Операция | Условие отказа | Код |
|---|---|---|
| set-plan | подписки нет | `404 not_found` |
| set-plan | план не существует | `400 validation` `details.plan_id` |
| set-plan | `days` > 3650 | `400 validation` `details.days` |
| set-plan | `days` < 0 | `400 validation` `details.days` |
| revoke | подписки нет | `404 not_found` |
| revoke | уже `revoked` | `409 stale`, `details.status` |
| delete | подписки нет | `404 not_found` |
| reset-traffic | подписка `revoked` или `canceled` | `409 stale` |
| reset-traffic | нет активных узлов | `409 in_use`, `details.nodes=0` |

Подтверждение в UI обязательно для revoke и delete: обе операции ломают доступ
пользователя, delete — необратим.

## 4. API

| Method | Path | Query/Body | Ответ |
|---|---|---|---|
| GET | `/admin/api/subscriptions` | `?limit=&offset=&status=&plan_id=&q=&sort=&order=&expiring_in=` | `{"items":[…],"total":N}` |
| GET | `/admin/api/subscriptions/{id}` | — | подписка + узлы |
| POST | `/admin/api/subscriptions/{id}/plan` | `{"plan_id":int,"days":int}` | `200` + подписка |
| POST | `/admin/api/subscriptions/{id}/revoke` | — | `200` + подписка |
| POST | `/admin/api/subscriptions/{id}/reset-traffic` | — | `200` + `{"reset_nodes":N}` |
| POST | `/admin/api/subscriptions/{id}/sync` | — | `200` + `{"subscription":{...},"warning":"..."}` |
| DELETE | `/admin/api/subscriptions/{id}` | — | `200` + `{"id":N,"deleted":true}` |

`/sync` принудительно прогоняет реконсайлер по одной подписке: толкает в
панели всё, что стоит в очереди (`pending_add`/`pending_update`/
`pending_remove`). Ответ — свежая деталь в том же DTO, что отдаёт `GET`.
`warning` появляется, когда после прогона привязки остались не-`active`
(`SyncSubscription` глотает пер-узловые ошибки по контракту best-effort) или
когда сверка упала целиком. События в журнал не пишется: это тот же
реконсайлер, что и у фонового воркера, домен он не меняет.

`limit` по умолчанию 50, максимум 200. `sort` — только `id`, `created_at`,
`expires_at`, `last_request`; по умолчанию `id desc`.

`q` — поиск по `telegram_id` (точное совпадение, если строка из цифр),
`username` (LIKE `%q%`), `subscription_id` (точное).

`status` — один из пяти статусов или пусто; `expiring_in` — часы (например,
`24` = истекает в ближайшие сутки).

DTO списка (без тяжёлых полей):

```json
{
  "id": 42,
  "telegram_id": 123456789,
  "username": "kereal",
  "subscription_id": "a1b2c3",
  "status": "active",
  "plan_id": 3,
  "plan_name": "premium",
  "expires_at": "2026-12-05T00:00:00Z",
  "last_request": "2026-11-04T18:20:00Z",
  "nodes_active": 2,
  "nodes_total": 2,
  "created_at": "2026-10-01T10:00:00Z",
  "updated_at": "2026-11-01T10:00:00Z"
}
```

Деталь добавляет: `devices`, `ips`, `product_id`, `duration_days`,
`price_paid_cents`, `currency`, `started_at`, `reminders_sent`,
`traffic_reminders_sent`, `referred_by`, `invite_code`, `sub_url`
(`/sub/{subscription_id}` — только путь, без домена) и массив узлов
`[{"node_id":1,"node_name":"nl-1","status":"active"}]`.

Счётчики `nodes_active`/`nodes_total` и `total` считаются тем же WHERE, что и
`items` (единый query builder).

## 5. Сервер

Файл `internal/database/subscriptions_admin.go`:

```go
type SubscriptionOverview struct {
    ID             uint
    TelegramID     int64
    Username       string
    SubscriptionID string
    Status         string
    PlanID         *uint
    PlanName       string
    ExpiresAt      *time.Time
    LastRequest    *time.Time
    NodesActive    int64
    NodesTotal     int64
    CreatedAt      time.Time
    UpdatedAt      time.Time
}

type SubscriptionFilter struct {
    Status     string
    PlanID     *uint
    Query      string
    ExpiringIn time.Duration
    Limit      int
    Offset     int
    Sort       string
    Desc       bool
}

func (s *Service) ListSubscriptionOverviews(ctx context.Context, f SubscriptionFilter) ([]SubscriptionOverview, int64, error)
func (s *Service) GetSubscriptionDetail(ctx context.Context, id uint) (*SubscriptionOverview, []SubscriptionNodeView, error)
```

`ListSubscriptionOverviews` собирает данные одним запросом с `LEFT JOIN plans`
и подзапросом по `subscription_nodes` (group by subscription_id) — N+1
запрещён. `total` — `COUNT(*)` по тому же WHERE.

Новый метод сервиса (`internal/service/subscription.go`):

```go
// RevokeSubscription отзывает подписку без удаления строки: статус revoked,
// VPN-клиенты уходят в pending_remove и синхронизируются best-effort.
// Идемпотентности нет: повторный вызов на revoked возвращает ErrAlreadyRevoked.
func (s *SubscriptionService) RevokeSubscription(ctx context.Context, id uint) (*database.Subscription, error)
```

Реализация — ровно те же примитивы, что в `DeleteByID`:
`markRevokedSub` → `logAudit(AuditSubscriptionRevoked)` → `deprovisionSub` →
инвалидация кэша → `RefreshActiveSubscriptionsMetric`. **Не дублировать** тело
`revokeAndDeprovisionThenDelete`: выделить общий приватный
`revokeAndDeprovision(ctx, sub)` и вызвать его из обоих методов.

`ErrAlreadyRevoked` — новый sentinel в `internal/service`, маппится в
`409 stale`.

Сброс трафика (`internal/service/sync.go`):

```go
// ResetSubscriptionTraffic обнуляет счётчик трафика на всех активных узлах
// подписки. Best-effort по узлам: ошибки агрегируются, успешные узлы
// учитываются в счётчике. Возвращает число узлов, где сброс удался.
func (s *SyncService) ResetSubscriptionTraffic(ctx context.Context, subscriptionID uint) (int, error)
```

Итерация по `subscription_nodes` со `status = active`, для каждого —
`s.clients.Provision(nodeID)` и `client.ResetTraffic(ctx, provision)`;
`buildProvision` переиспользуется, новая сетевая логика не пишется.
Отдельного публичного `ResetTraffic` у сервиса сейчас нет — это единственный
новый метод.

Хендлеры `admin/subscriptions.go` вызывают сервис, никакого SQL к
`subscriptions` в пакете `admin` нет.

## 6. Аудит

| Операция | Событие | Кто пишет |
|---|---|---|
| set-plan | `plan_changed` | уже пишет `AdminSetPlan` |
| revoke | `subscription_revoked` | новый `RevokeSubscription` |
| delete | `subscription_deleted` | уже пишет `DeleteByID` |
| reset-traffic | **новое** `traffic_reset` (`entity_type=subscription`) | хендлер панели после успеха |

Для `traffic_reset`: конструктор `AuditTrafficReset(sub *Subscription, nodes int, actorID int64)`.
`actor_type=admin`, `actor_id=0` — панель (передаётся через
`service.WithAuditAdmin(ctx, 0)`).

Reset-traffic пишется всегда, даже если часть узлов упала: `details` содержит
`{"nodes_ok":N,"nodes_failed":M}`.

## 7. UI

`admin/src/views/Subscriptions.svelte` (список) и `admin/src/views/SubscriptionDetail.svelte`
(`#/subscriptions/{id}`, id приходит пропом `let { id } = $props()` — роутер
общий, см. `00-common.md`). Запросы — через `api.get/post/put/del`; суммы и
даты — через `formatMoney`/`formatDate`.

Список: `ID · ПОЛЬЗОВАТЕЛЬ · ПЛАН · СТАТУС · ИСТЕКАЕТ · ПОСЛЕДНИЙ ЗАПРОС · УЗЛЫ · ДЕЙСТВИЯ`.

- пользователь: `@username` + `telegram_id` моноширинно; при отсутствии — только id;
- статус — бейдж: active зелёный, expired серый, paused жёлтый, canceled/revoked красный;
- истекает: `formatDate`; для `expired` и пустого — «—»; подсветка красным, если < 24 ч;
- узлы: `2/2`; если есть `pending_remove` — `2/2 · −1` с тултипом;
- пусто над таблицей — «истекает в ближайшие N ч», если задан фильтр.

Фильтры: поиск (debounce 300 мс, серверный), select статуса, select плана,
чекбокс «истекают за 24 ч». Пагинация: «50 из 1240», кнопки «‹ ›», select на
50/100/200.

Деталь (`#/subscriptions/{id}`): карточка «Пользователь» (telegram_id, username,
referred_by, invite_code), карточка «Подписка» (plan, product, created/started/expires,
price_paid, reminders), карточка «Состояние» (status, devices, ips, last_request,
sub_url с кнопкой «Скопировать»), таблица узлов (node, status, ошибка из
`pending_*` поля — если есть), панель действий.

Действия — кнопки с подтверждением:

1. «Продлить / сменить план» — модалка: select плана (текущий выбран), поле
   «дней» (пусто = сохранить текущий срок), подсказка «0 = сохранить срок,
   пусто = 30 дней для плана без срока»;
2. «Сбросить трафик» — confirm: «Счётчик трафика обнулится на N узлах»;
3. «Отозвать» — confirm с вводом последних 4 символов `subscription_id` (защита
   от случайного клика);
4. «Удалить» — confirm с тем же вводом + текст «строка исчезнет безвозвратно».

Состояния: загрузка, пусто («Подписок не найдено» + сброс фильтров), ошибка
(код из `format.js` + «Повторить»). После успешной операции — обновление данных
и `Notice`.

## 8. Тесты

`internal/database/subscriptions_admin_test.go`:

| Кейс | Ожидание |
|---|---|
| список без фильтров | `total` == число строк, порядок `id desc` |
| фильтр по статусу | только активные, `total` совпадает с items |
| поиск по telegram_id | одна строка |
| поиск по username (частичный) | LIKE находит |
| `expiring_in=24h` | только те, у кого `expires_at` в окне |
| пагинация offset/limit | items не пересекаются, `total` неизменен |
| plan_id NULL | `plan_name` пустая, без паники |
| счётчики узлов | `2/3` при двух active и трёх строках |

`internal/service` (подписки):

| Кейс | Ожидание |
|---|---|
| revoke активной | status revoked, аудит `subscription_revoked`, `subscription_nodes` → `pending_remove` |
| revoke уже revoked | `ErrAlreadyRevoked` |
| revoke несуществующей | `database.ErrSubscriptionNotFound` |
| delete после revoke | строка удалена, аудит `subscription_deleted` |
| AdminSetPlan days > 3650 | ошибка, строка не изменена |

`admin/subscriptions_test.go` — HTTP: `200` список, `404` на неизвестный id,
`400` на `days=abc`, `409` на повторный revoke, `401` без токена,
`DELETE` возвращает `{"deleted":true}`.

## 9. Критерии приёмки

- [ ] Панель не содержит ни одного `UPDATE subscriptions` / `UPDATE subscription_nodes` (проверяется грепом в ревью).
- [ ] Отзыв депровижинит VPN: после revoke субсервер отдаёт 404, а узлы получают `pending_remove`, затем `SyncPendingNodes` доводит до конца.
- [ ] `RevokeSubscription` и `DeleteByID` не дублируют код: обе вызывают общий приватный метод.
- [ ] Список из 5000 подписок отдаётся одним SQL на items и одним на total (без N+1).
- [ ] Удаление и отзыв требуют ввода хвоста `subscription_id`.
- [ ] Сброс трафика сообщает число узлов и показывает частичный сбой.
- [ ] Обе операции с состоянием оставляют аудит с `actor_type=admin`.
- [ ] `/sync` отвечает свежей деталью и ставит `warning`, если после прогона
      привязки остались не-`active`; без сервиса — `503`.

## 10. Файлы

Создаёт:

- `internal/database/subscriptions_admin.go` + `_test.go`
- `admin/subscriptions.go`, `admin/subscriptions_sync.go` + `_test.go`
- `admin/src/views/Subscriptions.svelte`, `admin/src/views/SubscriptionDetail.svelte`

Изменяет (не только одной строкой):

- `internal/service/subscription.go` — `RevokeSubscription`, выделение
  `revokeAndDeprovision`, sentinel `ErrAlreadyRevoked`;
- `internal/service/sync.go` — `ResetSubscriptionTraffic`;
- `internal/database/audit_log.go` — `AuditTrafficReset` + константа.

Правит одной строкой: `routes` и меню в `App.svelte`, маршруты в `Handler()`.
Не трогает: `admin/src/lib/*`, `admin/dist/*`, состояние-машины воркеров.

**Это единственное ТЗ, которое меняет сервисный слой.** Сервисная часть
делается отдельным коммитом до экрана.

## 11. Вне скоупа

- создание подписок и триалов из панели;
- правка `devices`/`ips` вручную;
- перевыпуск subscription_id;
- ручной пересчёт трафика (только сброс счётчика);
- массовые операции над подписками;
- отправка сообщений пользователю из панели.
