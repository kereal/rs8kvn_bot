# 02 — Ноды и привязки к планам

Эталон структуры: [01-products.md](01-products.md). Контракт: [ADR-0002](../../adr/0002-admin-panel-contract.md) (§2 `nodes`/`plan_nodes`, §5 секреты, §7 удаление).

## 1. Цель и границы

Экран управляет конфигурацией VPN-нод: панели 3x-ui, proxman-источники, fetch-источники, и привязывает их к планам (`plan_nodes`). Привязка плана к ноде определяет, на каких нодах бот провижинирует подписки этого плана (`SyncService.ReconcilePlanNodes`).

Не входит: health-мониторинг в фоне (только ручная кнопка), правка `subscription_nodes` (read-only по контракту, экран 04), просмотр инбаундов панели, синхронизация нод вручную, статистика трафика нод.

## 2. Данные

Схема — миграции [006](../../../internal/database/migrations/006_create_sources.up.sql), [014](../../../internal/database/migrations/014_sources_to_nodes.up.sql), [020](../../../internal/database/migrations/020_convert_inbound_id_to_array.up.sql):

```sql
CREATE TABLE nodes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name VARCHAR(255) NOT NULL,
  is_active BOOLEAN DEFAULT 1,                -- после 014: переименована из active
  host VARCHAR(255),                          -- после 014: из x_ui_host; NULL допустим схемой
  api_token VARCHAR(255),                     -- после 014: из x_ui_api_token; секрет
  inbound_ids TEXT NOT NULL DEFAULT '[]',     -- после 020: JSON-массив целых (был inbound_id INTEGER)
  subscription_url VARCHAR(512) NOT NULL,     -- после 014: из sub_url
  type VARCHAR(10) NOT NULL DEFAULT '3x-ui',  -- после 014
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE plan_nodes (
  plan_id INTEGER NOT NULL,
  node_id INTEGER NOT NULL,
  PRIMARY KEY (plan_id, node_id),
  FOREIGN KEY (plan_id) REFERENCES plans(id) ON DELETE CASCADE,
  FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);
```

`subscription_nodes` ([018](../../../internal/database/migrations/018_create_subscription_nodes.up.sql), [026](../../../internal/database/migrations/026_remove_subscription_nodes_cascade.up.sql)) — FK на `nodes(id)` **без** `ON DELETE`: удаление ноды с биндингами упадёт на FK, это перехватывает guard.

Факты, на которые опирается реализация:

- типы нод — `database.NodeType`: `NodeType3xUI = "3x-ui"`, `NodeTypeProxman = "proxman"`, `NodeTypeFetch = "fetch"` (`internal/database/models.go`). Это полный набор: `SyncService` пропускает любой другой тип («unsupported node type, skipping»), валидация должна совпадать;
- `host` — адрес панели для `xui.NewClient` (3x-ui) и proxman-клиента; fetch-нода его не использует — она читает `subscription_url` как источник конфига (`internal/vpn/fetch.go`);
- `api_token` используется `xui.NewClient(host, apiToken)`; для fetch не нужен;
- `plan_nodes` каскадно удаляется вместе с планом и нодой — удаление ноды молча отвязало бы её от планов, поэтому сначала guard (см. §3);
- существующие методы `internal/database/nodes.go`: `ListNodes`, `GetNodeByID` (оборачивает `ErrNodeNotFound`), `ListEnabled`, `CreateNode`, `GetNodesByPlanID` (только активные ноды), `GetNodesByPlanName`, `LinkNodeToPlan(planName, nodeID)` — по имени плана, без sentinel-ошибок; экраном **не используется**, новые методы — в `nodes_admin.go`;
- для health готов клиент: `xui.NewClient(host, apiToken)` + `(*xui.Client).Ping(ctx)` — `GET /panel/api/server/status` (`internal/xui/client.go:280`). Сетевую логику не дублируем.

Существующих sentinel-ошибок достаточно: `ErrNodeNotFound` (`models.go`). `ErrNodeInvalid`, `ErrNodeInUse` — новые (см. §5).

## 3. Права и guard'ы

| Операция | Условие отказа | Код |
|---|---|---|
| create | `name` пустой или > 100 символов (после trim) | `400 validation` `details.name` |
| create | `subscription_url` пустой или не `http(s)://` | `400 validation` `details.subscription_url` |
| create | `type` вне `{3x-ui, proxman, fetch}` | `400 validation` `details.type` |
| create | `host` не `http(s)://` для типа `3x-ui`/`proxman` | `400 validation` `details.host` |
| create | `inbound_ids` не массив целых или содержит < 0 | `400 validation` `details.inbound_ids` |
| update | те же проверки полей, что и create | `400 validation` |
| update | смена `type` | `409 protected`, `details.reason="type_immutable"` |
| delete | есть строки `plan_nodes` | `409 in_use`, `details.plans=N` |
| delete | есть строки `subscription_nodes` | `409 in_use`, `details.subscriptions=N` |
| delete | обе причины | `409 in_use`, оба счётчика |

Дополнительные правила:

- **`api_token` write-only (P16).** Ни один маршрут не возвращает токен; в DTO только `has_api_token: bool`. В `PUT` пустая строка = «не менять»; задать пустой токен нельзя (нужно явное поле сброса — не вводим, пустой токен допустим только у fetch-нод, созданных без токена). Логи и тексты ошибок токен не содержат.
- **Смена `type` запрещена**: тип определяет клиента провижининга, существующие `subscription_nodes` этой ноды провижинены старым типом. Правильный путь — удалить ноду и создать заново.
- `inbound_ids` имеет смысл только для 3x-ui; для `proxman`/`fetch` принимается `[]` (сервер не навязывает, но UI скрывает поле).
- Health-проверка требует заданного `api_token` у 3x-ui-ноды; без токена — `400 validation` `details.api_token="not set"`. На удаление токен не влияет.
- **Привязки к планам**: `PUT /admin/api/plans/{id}/nodes` — полная замена набора, идемпотентно (повтор того же набора = no-op). Отвязать ноду, у которой есть `subscription_nodes`, — **можно**: панель меняет конфигурацию, а не состояния (ADR-0002 §2); фактическое удаление клиентов с ноды выполнит `SyncService.ReconcilePlanNodes` (REMOVE → `pending_remove`) при следующей синхронизации подписок плана [INFERENCE: точный момент запуска reconcile для уже существующих подписок плана — по коду он вызывается из `SyncPendingNodes`/`SyncSubscription`; ТЗ не требует его вызывать]. Гард «нельзя отвязать используемую ноду» в коде не подтверждён и **не вводится** — он заставил бы панель писать в state machine.
- Добавлять неактивную ноду в план можно (ADR-0002 §2 `plan_nodes`), UI помечает.

## 4. API

| Method | Path | Query/Body | Ответ | Ошибки |
|---|---|---|---|---|
| GET | `/admin/api/nodes` | — | `{"items":[NodeDTO…],"total":N}` | 401 |
| POST | `/admin/api/nodes` | nodePayload | `201` + NodeDTO | 400, 401 |
| PUT | `/admin/api/nodes/{id}` | nodePayload | `200` + NodeDTO | 400, 401, 404, 409 |
| DELETE | `/admin/api/nodes/{id}` | — | `200` + удалённый NodeDTO | 401, 404, 409 |
| GET | `/admin/api/plans/{id}/nodes` | — | `{"node_ids":[…]}` | 400, 401, 404 |
| PUT | `/admin/api/plans/{id}/nodes` | `{"node_ids":[…]}` | `200` + `{"node_ids":[…]}` | 400, 401, 404 |
| POST | `/admin/api/nodes/{id}/health` | — | `200` + `{"available":bool,"latency_ms":int,"error":str,"checked_at":rfc3339}` | 400, 401, 404 |

Полный список без пагинации (P7): нод — единицы-десятки.

DTO ответа (модель БД в JSON не отдаётся, P24):

```json
{
  "id": 2,
  "name": "main-panel",
  "type": "3x-ui",
  "host": "http://203.0.113.10:2053",
  "subscription_url": "https://sub.example.com/s/",
  "inbound_ids": [1, 3],
  "is_active": true,
  "has_api_token": true,
  "plans_count": 3,
  "subscriptions_count": 12,
  "created_at": "2026-10-01T10:00:00Z",
  "updated_at": "2026-11-02T11:30:00Z"
}
```

- `plans_count` — строки `plan_nodes`; `subscriptions_count` — строки `subscription_nodes` всех статусов (pending — тоже зависимость для удаления). Оба — одним SQL с коррелированными подзапросами, как `ListPlanOverviews` (N+1 запрещён).
- `has_api_token` = `api_token != ""`. Токен в JSON не попадает никогда.

Тело create/PUT: `{"name":str,"type":str,"host":str,"subscription_url":str,"inbound_ids":[int],"api_token":str,"is_active":bool}`. Неизвестные поля → `400` (`DisallowUnknownFields`, P4). В `PUT` пустой `api_token` = «не менять» (P10) — если `has_api_token` был `false`, останется `false`.

DELETE возвращает удалённый DTO с `plans_count=0`, `subscriptions_count=0` и `has_api_token` (без самого токена) — UI показывает, что именно удалил.

## 5. Сервер

Файл `internal/database/nodes_admin.go`:

```go
// NodeOverview — нода со счётчиками зависимостей для списка и guard'ов.
type NodeOverview struct {
    Node
    PlansCount         int64
    SubscriptionsCount int64
}

func (s *Service) ListNodeOverviews(ctx context.Context) ([]NodeOverview, error)
func (s *Service) UpdateNodeAdmin(ctx context.Context, node *Node) error
func (s *Service) DeleteNode(ctx context.Context, id uint) (Node, error)
func (s *Service) ReplacePlanNodes(ctx context.Context, planID uint, nodeIDs []uint) ([]uint, error)
func (s *Service) GetPlanNodeIDs(ctx context.Context, planID uint) ([]uint, error)
```

Sentinel-ошибки (`models.go` рядом с существующими):

- `ErrNodeInvalid` — валидация полей (400);
- `ErrNodeTypeProtected` — смена `type` (409 protected);
- `ErrNodeInUse` — несущий `Details map[string]int64` (`"plans"`, `"subscriptions"`) для delete (409 in_use);
- `ErrPlanNotFound` — уже есть, переиспользуется для привязок (404).

Правила реализации:

- валидация (`validateNodeFields`, по образцу `validatePlanFields`) — в репозитории, не только в хендлере: экран — не единственный вызывающий; парсинг `inbound_ids` из TEXT-JSON — в репозитории (JSON unmarshal в `[]int`, ошибка → `ErrNodeInvalid`);
- `UpdateNodeAdmin`: читает текущую строку (`ErrNodeNotFound`), сверяет `type` (`ErrNodeTypeProtected`), валидирует, при пустом `api_token` сохраняет прежний, затем `Save`; `updated_at` ведёт GORM;
- `DeleteNode`: два `SELECT COUNT(*)` (`plan_nodes`, `subscription_nodes`) → при ненулевых счётчиках `ErrNodeInUse{Details}`, иначе `DELETE`; гонка не критична (один админ), FK-ошибку всё равно маппить в `ErrNodeInUse`; возвращает удалённую строку для ответа и аудита;
- `ReplacePlanNodes`: план существует? (`ErrPlanNotFound`). Все `nodeIDs` существуют? (несуществующий → `ErrNodeInvalid` с `details.node_ids`). Дальше в одной транзакции: `DELETE FROM plan_nodes WHERE plan_id=?`, затем `INSERT` недостающих пар; повтор того же набора не меняет строки (идемпотентность проверяется тестом). Возвращает итоговый набор отсортированных `node_ids`;
- `GetPlanNodeIDs` — простой SELECT для GET-маршрута (возвращает `[]uint`,
  имя без «Count», потому что это список, а не счётчик);
- `LinkNodeToPlan` в `nodes.go` не трогается (его потребители — бот); экран использует только `nodes_admin.go`.

Файл `admin/nodes.go` — узкий интерфейс:

```go
type NodeStore interface {
    ListNodeOverviews(ctx context.Context) ([]database.NodeOverview, error)
    CreateNode(ctx context.Context, node *database.Node) error
    UpdateNodeAdmin(ctx context.Context, node *database.Node) error
    DeleteNode(ctx context.Context, id uint) (database.Node, error)
    ReplacePlanNodes(ctx context.Context, planID uint, nodeIDs []uint) ([]uint, error)
    GetPlanNodeIDs(ctx context.Context, planID uint) ([]uint, error)
}
```

`CreateNode` уже существует и удовлетворяет интерфейсу (переиспользуется, не дублируется). Маппинг sentinel'ов — ветки в общем `writeStoreError` (`admin/plans.go`): `ErrNodeNotFound`→404, `ErrNodeInvalid`→400, `ErrNodeTypeProtected`→409 `protected`, `ErrNodeInUse`→409 `in_use` (числа зависимостей — в `details`).

Health-хендлер (`POST /admin/api/nodes/{id}/health`) живёт в `admin/nodes.go`:

- репозиторий возвращает ноду целиком (с токеном) — DTO это не нарушает, токен в ответ не идёт;
- тип `3x-ui`: `xui.NewClient(node.Host, node.APIToken)` → `Ping(ctx)` с `context.WithTimeout(10 * time.Second)` (`config.XUIRequestTimeout`); сетевой код — только вызов готового `Ping`, дублирования нет;
- `latency_ms` меряется вокруг `Ping`; ошибка — текст без токена (`available:false`);
- типы `proxman`/`fetch`: готового пинга нет → `400 validation` `details.type="health not supported for this node type"` (обоснование в §11);
- `xui.NewClient` может вернуть ошибку (кривой host) → `200` + `{"available":false,"error":…}`: результат диагностики — данные, а не ошибка протокола.

Маршруты в `Handler()` (`admin/server.go`, по одной строке на маршрут):

```go
mux.HandleFunc("GET /admin/api/nodes", s.withAuth(s.handleListNodes))
mux.HandleFunc("POST /admin/api/nodes", s.withAuth(s.handleCreateNode))
mux.HandleFunc("PUT /admin/api/nodes/{id}", s.withAuth(s.handleUpdateNode))
mux.HandleFunc("DELETE /admin/api/nodes/{id}", s.withAuth(s.handleDeleteNode))
mux.HandleFunc("POST /admin/api/nodes/{id}/health", s.withAuth(s.handleNodeHealth))
mux.HandleFunc("GET /admin/api/plans/{id}/nodes", s.withAuth(s.handleGetPlanNodes))
mux.HandleFunc("PUT /admin/api/plans/{id}/nodes", s.withAuth(s.handlePutPlanNodes))
```

Маршруты привязок висят на `/admin/api/plans/{id}/nodes` — конфликтов с существующими plan-маршрутами нет (Go 1.22 ServeMux различает по полному шаблону).

## 6. Аудит

Новые конструкторы в `internal/database/audit_log.go` (entity-типы — открытый реестр, миграция не нужна; `EntityNode` уже есть):

| Событие | Константа | entity_type | old/new state |
|---|---|---|---|
| создана | `AuditEventNodeCreated = "node_created"` | `node` | `nil` → снимок |
| изменена | `AuditEventNodeUpdated = "node_updated"` | `node` | снимок → снимок |
| удалена | `AuditEventNodeDeleted = "node_deleted"` | `node` | снимок → `nil` |
| привязки плана изменены | `AuditEventPlanNodesChanged = "plan_nodes_changed"` | `plan` | `{"node_ids":[…]}` → `{"node_ids":[…]}` |

- снимок ноды — имя, тип, host, `subscription_url`, `inbound_ids`, `is_active`, `has_api_token`; сам токен в снимок не попадает никогда;
- `plan_nodes_changed`: `entity_id = plan_id`, в `details` — только `added`/`removed` (списки id), чтобы дифф читался без сравнения массивов;
- аудит — после успешной мутации, best-effort (`insertAuditLogBestEffort`), ошибка логируется `Warn`.

## 7. UI

`admin/src/views/Nodes.svelte`.

Таблица: `ID · ИМЯ · ТИП · ХОСТ · СТАТУС · ПЛАНЫ · ПОДПИСКИ · ТОКЕН · ДЕЙСТВИЯ`.

- тип — бейдж `3x-ui` / `proxman` / `fetch`;
- статус — бейдж «активна» / «выключена»;
- токен — «задан» / «не задан» (`has_api_token`); конкретное значение не показывается никогда;
- планы — число; подписки — число, при 0 серым; при >0 кнопка удаления disabled с подсказкой (guard предзаем с сервера всё равно есть).

Панель фильтров: поиск по имени/хосту (клиентский), select «все типы / <тип>», чекбокс «только активные». Сортировка по клику на заголовок (id, name, created_at).

Модалка создания/правки (`<dialog>`, стиль `Plans.svelte`): имя, тип (select, disabled при правке — guard §3), host, subscription_url, inbound ids (через запятую, только для 3x-ui), токен (password-поле с placeholder «не менять» при правке; пустое при правке = не менять), активность.

Привязки к планам: на этом экране — модалка «Планы ноды» (чекбоксы всех планов) или секция под таблицей; альтернативная точка входа — на экране «Планы» (01-products) не вводится, её добавит интегратор при слиянии. PUT идемпотентен, кнопка «Сохранить» всегда активна.

Кнопка «Проверить» (health) — только у нод типа 3x-ui с заданным токеном; у остальных disabled с подсказкой. Результат — inline-бейдж в строке: «доступна · 84 мс» / «недоступна · <текст ошибки>», до следующей перезагрузки списка.

Удаление: confirm; при `in_use` — текст с числами («нельзя удалить: 3 плана, 12 подписок»), строка остаётся.

Состояния: загрузка, пусто («Пока нет нод» + кнопка создания), ошибка (`humanError` + «Повторить»).

Запросы — через общие хелперы `api.get` / `api.post` / `api.put` / `api.del`
(`src/lib/api.js`). Именованные обёртки в `api.js` не добавляются: файл общий
для всех экранов и разрабатывается на разных ветках (см. `00-common.md`).

## 8. Тесты

`internal/database/nodes_admin_test.go` — на реальной SQLite через `newTestService` (таблица кейсов):

| Кейс | Ожидание |
|---|---|
| create валидная 3x-ui | строка есть, `inbound_ids` записан, аудит `node_created` |
| create fetch-нода без токена и host | строка есть |
| create пустое имя / имя > 100 | `ErrNodeInvalid` |
| create `subscription_url=""` / `ftp://…` | `ErrNodeInvalid` |
| create `type="marzban"` | `ErrNodeInvalid` |
| create 3x-ui без host / `host="203.0.113.10"` без схемы | `ErrNodeInvalid` |
| create `inbound_ids="[\"a\"]"` (битый JSON) / `[-1]` | `ErrNodeInvalid` |
| update имени/лимитов | поля изменились, аудит `node_updated` со старым снимком |
| update с пустым `api_token` | токен в БД не изменился |
| update с новым `api_token` | токен заменён |
| update `type` | `ErrNodeTypeProtected` |
| delete без зависимостей | строка удалена, возвращён DTO, аудит `node_deleted` |
| delete с `plan_nodes` | `ErrNodeInUse`, `Details["plans"]==1` |
| delete с `subscription_nodes` | `ErrNodeInUse`, `Details["subscriptions"]==1` |
| delete с обеими зависимостями | оба счётчика в `Details` |
| ReplacePlanNodes тем же набором | повторный вызов не меняет строки, результат равен входу |
| ReplacePlanNodes с несуществующим node_id | `ErrNodeInvalid` |
| ReplacePlanNodes несуществующего плана | `ErrPlanNotFound` |
| ReplacePlanNodes: было [1,2], стало [2,3] | итог `[2,3]`, строки 1 удалена, 3 добавлена |

`admin/nodes_test.go` — HTTP: `200` список с `total` и `has_api_token` (и отсутствием `api_token` в JSON), `201` создание, `400` на неизвестное поле и на `type="marzban"`, `401` без токена, `404` на чужой id, `409` на смену `type` и на удаление с зависимостями, `200` у `PUT /plans/{id}/nodes` с идемпотентным повтором, `200` у health c `available:false` при недоступном host (тест поднимает `httptest.Server` с 500, реальный `Ping`, без стабов сети).

## 9. Критерии приёмки

- [x] `api_token` отсутствует в любом JSON-ответе и в текстах ошибок; есть `has_api_token`; пустая строка в `PUT` не затирает токен (тест). — проверено: admin/nodes.go:32-57 (DTO без токена), admin/nodes_test.go:49,69-70,190-199, TestUpdateNodeAdmin_KeepsTokenOnEmpty.
- [x] `type` вне `{3x-ui, proxman, fetch}` и смена `type` отклоняются, а не пишутся в БД (тесты). — проверено: internal/database/nodes_admin.go:409-424,:197-198; TestUpdateNodeAdmin_RejectsTypeChange, admin/nodes_test.go:103-107,179-182.
- [x] Удаление ноды с привязками блокируется `409 in_use` с числами планов и подписок (тесты на оба счётчика). — проверено: nodes_admin.go:244-258; admin/nodes_test.go:240-243, TestDeleteNode_Guards (plans+subscriptions).
- [x] Список нод содержит `plans_count`/`subscriptions_count` без N+1 (один SQL). — проверено: nodes_admin.go:29-33 (один SELECT, коррелированные подзапросы); TestListNodeOverviews_CountsWithoutNPlusOne.
- [x] `PUT /plans/{id}/nodes` идемпотентен и атомарен (транзакция; тест на повтор). — проверено: nodes_admin.go:295 (Transaction), :126-130 (только дифф); TestReplacePlanNodes (повтор — no-op, без аудита).
- [x] Отвязка используемой ноды разрешена и оставляет `subscription_nodes` нетронутыми (панель не пишет в состояния). — проверено: TestReplacePlanNodes_UnlinkNodeWithSubscriptions; nodes_admin.go:120-131 (subscription_nodes не трогается).
- [x] Health вызывает `xui.Client.Ping`, не открывает второй HTTP-клиент с другой логикой; для proxman/fetch отвечает `400 validation`. — проверено: admin/nodes.go:234-250; admin/nodes_test.go:368-373,383-396 (для proxman — тот же код-путь, отдельного теста нет).
- [x] Все мутации пишут аудит `actor_type=admin`, `actor_id=0`; токен ни в одном снимке. — проверено: audit_log.go:924-949 (ActorAdmin,"0"), :904-921 (снимок с has_api_token); nodes_admin.go:171,223,276.
- [x] `go test -race ./admin/` зелёный, `golangci-lint` без замечаний. — проверено: прогнано 2026-11-05: go test -race ./admin/ ./internal/database/ ./internal/bot/ ./cmd/bot/ — ok (74.5s); golangci-lint run ./... — 0 issues.

## 10. Файлы

Создаёт:

- `internal/database/nodes_admin.go`
- `internal/database/nodes_admin_test.go`
- `admin/nodes.go`
- `admin/nodes_test.go`
- `admin/src/views/Nodes.svelte`

Правит одной строкой: `routes` и меню в `admin/src/App.svelte`, `mux.HandleFunc` в `Handler()` (`admin/server.go`), ветки sentinel'ов в `writeStoreError` (`admin/plans.go`), конструкторы аудита в `internal/database/audit_log.go`, sentinel-ошибки в `internal/database/models.go`.

Не трогает: `internal/database/nodes.go` (методы бота), `admin/src/lib/*`, `admin/dist/*`, `internal/service/*`, `internal/xui/*`, `internal/vpn/*`.

## 11. Вне скоупа

- health для `proxman`/`fetch`: у proxman-клиента нет диагностического метода, fetch не имеет панели; вводить пинг в `internal/vpn` — сетевая логика вне экрана (отдельный сервисный коммит, если понадобится);
- фоновый health-мониторинг и хранение результата проверки в БД;
- CRUD `subscription_nodes` — read-only по контракту (экран 04);
- просмотр/выбор инбаундов из живой панели (только ручной ввод id);
- пагинация и серверная сортировка;
- создание ноды сразу с привязками одним запросом (привязки — отдельный маршрут).
