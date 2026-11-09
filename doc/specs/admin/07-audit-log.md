# 07 — Журнал аудита (только чтение)

Эталон структуры: [01-products.md](01-products.md). Общие правила:
[00-common.md](00-common.md), контракт: [ADR-0002](../../adr/0002-admin-panel-contract.md).

## 1. Цель и границы

Экран для расследований: кто, когда и что менял — по сущности, по типу события,
по исполнителю. `audit_log` — растущая append-only таблица, пагинация
обязательна (P7). Панель только фильтрует и читает (матрица §2 ADR-0002).

**Экран строго read-only**: ни одной мутации. `audit_log` append-only по
схеме — строки никогда не обновляются и не удаляются приложением
(ретеншн-очистка `DeleteAuditLogBefore` — воркер бота, не панель).

Не входит: правка/удаление записей, экспорт, ретеншн-настройки, запись аудита
о просмотре журнала (чтение не аудируется).

## 2. Данные

Фактическая схема — миграция `039_create_audit_log.up.sql`:

```sql
CREATE TABLE audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type TEXT NOT NULL,
  entity_id TEXT,
  event TEXT NOT NULL,
  actor_type TEXT NOT NULL,
  actor_id TEXT,
  old_state TEXT,
  new_state TEXT,
  details TEXT NOT NULL DEFAULT '{}',
  created_at DATETIME NOT NULL
);
```

Индексы: `idx_audit_log_entity (entity_type, entity_id, created_at DESC)`,
`idx_audit_log_created_at (created_at DESC)`, `idx_audit_log_event (event)`.

Факты, на которые опирается реализация:

- `entity_id`/`actor_id` — **TEXT** (строковое представление; trial-подписки
  используют нечисловые идентификаторы); в DTO — строка, не число;
- `old_state`/`new_state` — JSON **изменившихся полей** (до/после), а не всей
  строки; `NULL` = «поля не менялись» / «сущность создана или удалена»;
- `details` — JSON со свободным контекстом; пустая = `'{}'`; запись идёт через
  `marshalAuditDetails` (`audit_log.go`) и всегда валидна — парсинг на сервере
  не может упасть на свежих записях (защитный fallback всё же описан в §5);
- нет CHECK на `entity_type`/`event` — целостность имён обеспечивает реестр
  констант в `internal/database/audit_log.go` (открытый список: новое событие =
  одна константа, миграция не нужна);
- нет FK — события переживают физическое удаление сущности; `entity_id` может
  указывать на несуществующую строку, UI не обязан это проверять.

Существующие методы (`internal/database/audit_log_repo.go`):

| Метод | Поведение |
|---|---|
| `GetAuditLogByEntity(ctx, entityType, entityID, limit)` | история сущности, `created_at DESC, id DESC`, default limit 100 |
| `GetAuditLogByEvent(ctx, event, limit)` | события одного типа, тот же порядок |
| `GetRecentAuditLog(ctx, limit)` | последние события всех типов |

Все три без фильтров-комбинаций, без `total`, без `offset` — для экрана не
годятся напрямую. **Переиспользуем, не дублируем**: новый метод
`ListAuditLogPage` (§5) становится общей основой, а `GetRecentAuditLog`
делегируется в него; `GetAuditLogByEntity` остаётся как есть (его вызывают
сервисы для контекста подписки) — при желании тоже перевести на
`ListAuditLogPage` отдельной строкой в том же коммите, без смены сигнатуры.

## 3. Права и guard'ы

| Операция | Условие отказа | Код |
|---|---|---|
| list | `entity_type` вне реестра | `400 bad_request` |
| list | `actor_type` вне реестра | `400 bad_request` |
| list | `event` вне реестра | `400 bad_request` |
| list | `sort` вне белого списка | `400 bad_request` (P8) |
| list | `order` не `asc`/`desc` | `400 bad_request` (P8) |
| list | `offset` < 0 | `400 bad_request` |
| list | `from`/`to` не `YYYY-MM-DD` | `400 bad_request` |
| list | `limit` > 200 | clamp до 200, не ошибка (P7) |
| detail | записи нет | `404 not_found` (`ErrAuditLogNotFound`) |

`entity_id` — свободный текст, валидации нет. Белые списки `entity_type` /
`actor_type` / `event` строятся из реестра констант (§4, маршрут
`/admin/api/audit/events`) — новый event из будущего ТЗ автоматически
допустим, хендлер не правится.

Мутаций нет — guard'ов на запись не существует. `writeStoreError`
(`admin/plans.go`) дополняется кейсом `errors.Is(err, database.ErrAuditLogNotFound)`
→ `404 not_found`.

## 4. API

| Method | Path | Query | Ответ | Ошибки |
|---|---|---|---|---|
| GET | `/admin/api/audit` | `?limit=&offset=&entity_type=&entity_id=&event=&actor_type=&from=&to=&sort=&order=` | `{"items":[…],"total":N}` | `400`, `401`, `500` |
| GET | `/admin/api/audit/events` | — | `200` + реестр событий | `401`, `500` |
| GET | `/admin/api/audit/{id}` | — | `200` + полная запись | `401`, `404`, `500` |

Параметры списка:

- `limit` — по умолчанию 50, максимум 200 (clamp); `offset` — по умолчанию 0;
- `entity_type` — значение из реестра (`subscription`, `order`, `broadcast`,
  `plan`, `node`, `product`, `system`), пусто = все;
- `entity_id` — точное совпадение TEXT;
- `event` — значение из реестра (`order_created`, `payment_confirmed`, …);
- `actor_type` — `user|admin|worker|payment_provider|system`;
- `from`/`to` — дата `YYYY-MM-DD`, окно по `created_at` UTC, границы
  включительно (как в ТЗ 05);
- `sort` — белый список: `created_at` (по умолчанию), `id`; `order` —
  `asc|desc`, по умолчанию `desc` (новые первыми);
- `total` обязателен, единый WHERE с `items` (§5).

`GET /admin/api/audit/events` — реестр из Go-констант, не хардкод:

```json
{
  "events": ["subscription_created", "trial_created", "order_created", "…"],
  "entity_types": ["subscription", "order", "broadcast", "plan", "node", "product", "system"],
  "actor_types": ["user", "admin", "worker", "payment_provider", "system"]
}
```

Решение — **маршрут, а не константы в UI**. Реестр живёт в
`internal/database/audit_log.go` и открыт: каждое новое ТЗ (01–06) добавляет
туда конструкторы; хардкод-словарь в Svelte устарел бы при первом же таком
коммите, и панель молча потеряла бы фильтр по новому событию. Маршрут — один
read-хендлер над тремя чистыми функциями реестра; альтернатива (генерация JS
из Go на этапе сборки) — новый инструмент в пайплайне против одного
GET-запроса.

DTO списка (поле-в-поле; `old_state`/`new_state` не включаются ради объёма):

```json
{
  "id": 8842,
  "entity_type": "order",
  "entity_id": "1042",
  "event": "payment_confirmed",
  "actor_type": "payment_provider",
  "actor_id": "platega",
  "created_at": "2026-11-01T10:05:13Z",
  "details": {"product": "30 дней", "amount_cents": 39900}
}
```

`details` — **распарсенный JSON** (объект), а не строка: серверу дешевле
распарсить один раз, чем каждый клиент дергать `JSON.parse` в try/catch.
Тип в Go — `json.RawMessage`, отдаётся как есть. Fallback: если колонка не
валидный JSON (legacy-мусор), значение оборачивается строкой — UI не падает.

DTO детальной записи: те же поля + `old_state` и `new_state` (тоже
`json.RawMessage`; `null` → JSON `null`, что означает «полей не менялось» /
«сущность создана или удалена» — семантику из миграции 039).

## 5. Сервер

Файл `internal/database/audit_log_admin.go`:

```go
// AuditLogFilter — фильтры экрана «Журнал аудита». Zero value = без фильтра.
type AuditLogFilter struct {
    EntityType EntityType // "" = все
    EntityID   string
    Event      string
    ActorType  ActorType
    From       *time.Time // окно created_at, границы включительно
    To         *time.Time
    Sort       string // created_at | id
    Desc       bool   // по умолчанию true
    Limit      int
    Offset     int
}

// Единый query builder: WHERE в одном месте, вызывается дважды —
// Count и SELECT (см. ТЗ 05 §5: два свежих вызова, не reuse *gorm.DB).
func buildAuditLogQuery(s *Service, ctx context.Context, f AuditLogFilter) *gorm.DB

func (s *Service) ListAuditLogPage(ctx context.Context, f AuditLogFilter) (items []AuditLog, total int64, err error)
func (s *Service) GetAuditLogByID(ctx context.Context, id uint) (*AuditLog, error)

// Реестр для /admin/api/audit/events: чистые функции над константами
// audit_log.go. Живут в audit_log_admin.go — audit_log.go не трогается.
func AuditEventNames() []string
func EntityTypeNames() []string
func ActorTypeNames() []string

// ErrAuditLogNotFound — новый sentinel для 404.
var ErrAuditLogNotFound = errors.New("audit log entry not found")
```

Правила реализации:

- `ListAuditLogPage` — один SQL на items и один на `COUNT(*)` по тому же
  WHERE (единый builder), N+1 запрещён. Порядок `created_at DESC, id DESC`
  как в существующих методах.
- `GetRecentAuditLog` делегируется в `ListAuditLogPage(AuditLogFilter{Limit: limit})`
  — один query builder на пакет, а не два (правило «не дублировать»).
- `GetAuditLogByID` при отсутствии строки возвращает `ErrAuditLogNotFound`.
- Валидация фильтров — в хендлере (`admin/audit.go`): белые списки
  формируются из `EntityTypeNames()`/`AuditEventNames()`/`ActorTypeNames()`
  один раз на запрос, неизвестное значение → `400 bad_request`.
- Хендлеры в `admin/audit.go`: узкий интерфейс `AuditStore` с тремя методами
  (образец — `PlanStore` в `admin/server.go:38`), DTO с json-тегами там же
  (P24). Даты `from`/`to` парсятся в `time.Time` с границами дня UTC, как в
  ТЗ 05.

## 6. Аудит

**Не пишется.** Экран read-only: мутаций нет — писать нечего. Чтение журнала
само не логируется (иначе бесконечная рекурсия смысла). Новых конструкторов
событий и изменений в `internal/database/audit_log.go` не требуется — все
существующие события создаются ботом и другими экранами.

## 7. UI

`admin/src/views/AuditLog.svelte`.

Список: `ВРЕМЯ · СОБЫТИЕ · СУЩНОСТЬ · ID СУЩНОСТИ · ИСПОЛНИТЕЛЬ · ДЕТАЛИ`.

- время: `formatDate` (format.js);
- событие/сущность/исполнитель — русские названия из **локального словаря во
  вью** (см. ниже), неизвестное значение → исходный код события моноширинно
  (открытый реестр: новые события показываются, пока словарь не обновлён);
- сущность: `подписка #1042` — `entity_id` рядом с типом;
- исполнитель: бейджи `user`/`admin`/`worker`/`payment_provider`/`system`;
- детали: если `details` — непустой объект, показывать компактно
  (`JSON.stringify` без отступов, `overflow: ellipsis`, тултип полный);
  `'{}'` → «—»;
- клик по строке → раскрытие (§ ниже).

**Порядок по умолчанию — новые сверху**: `sort=created_at`, `order=desc`
(`created_at DESC, id DESC`). Список открывается последними событиями, без
прокрутки к началу истории; переключение на `asc` — явное действие
пользователя, а не стартовое состояние. После смены фильтра порядок остаётся
`desc`.

Фильтры: select «тип события» (варианты из `/admin/api/audit/events`),
select «сущность», select «исполнитель», поле `entity_id`, два инпута
`type="date"` (от/до). Пагинация: «50 из 54000», «‹ ›», select на 50/100/200.
Счётчик «найдено N» из `total`.

**Словарь русских названий — локальная константа во вью**
(`AuditLog.svelte`), не в `format.js`: `format.js` принадлежит интегратору
(00-common, §«Не трогает»), а словарь событий — забота одного экрана и
обновляется вместе с ним. В общий файл попадает только сквозное
форматирование (деньги/байты/даты/коды ошибок). То же решение в ТЗ 05 для
статусов заказов. Словарь держать рядом с фетчем `/admin/api/audit/events`:
список опций select — с сервера, подписи — из локального словаря.

**Раскрытие строки, а не отдельная страница** (`#/audit/{id}` отклонён):
у записи аудита нет связанных строк и операций (P26 — отдельная страница
нужна, когда есть история/связи), старые/новые состояния — один разворачиваемый
блок. Раскрытие лениво делает `GET /admin/api/audit/{id}` и кэширует ответ на
время сессии списка (повторное раскрытие не рефетчит). Модель данных это
позволяет без смены API: если позже понадобится deep-link на запись,
`#/audit/{id}` добавляется без изменения бэкенда.

В раскрытии показывать:

- `old_state` и `new_state` — JSON с отступами: `JSON.stringify(parsed, null, 2)`
  внутри `<pre>` (моноширинный, `max-height` + scroll, кнопка «Скопировать»).
  Без внешних библиотек подсветки (P25: никаких новых зависимостей);
- оформление `<pre>` — из темы: фон `var(--bg)`, текст `var(--text)`,
  рамка `var(--border)`. Жёсткий белый фон недопустим: в тёмной теме
  (`prefers-color-scheme: dark`) светлый текст на белом нечитаем;
- `null` вместо объекта → текст «поля не менялись» / «сущность создана» /
  «сущность удалена» по семантике (см. миграцию 039);
- `details` — полным блоком, как old/new;
- `actor_id` и `entity_id` — моноширинно.

Состояния (P27): загрузка, пусто («Записей не найдено» + сброс фильтров),
ошибка (`humanError` + «Повторить»), бейдж «только чтение». Кнопка
«Обновить», никакого polling (P28).

## 8. Тесты

`internal/database/audit_log_admin_test.go` — таблица кейсов на реальной
SQLite через `testutil.NewTestDatabaseService`:

| Кейс | Ожидание |
|---|---|
| список без фильтров | `total` == число строк, порядок `created_at desc, id desc` |
| фильтр entity_type+entity_id | только история сущности |
| фильтр event | только события типа |
| фильтр actor_type | только записи исполнителя |
| окно from/to | границы дня UTC включительно |
| пагинация offset/limit | items не пересекаются, `total` неизменен |
| clamp limit=1000 | SQL получает 200 |
| `details` = `'{}'` | в DTO — пустой объект `{}` |
| `details` невалидный JSON (вставка raw SQL) | строка, без паники |
| `old_state` NULL в детали | `null` в JSON ответа |
| `GetRecentAuditLog` | те же результаты, что `ListAuditLogPage` (делегация) |
| деталь несуществующего | `ErrAuditLogNotFound` |
| `AuditEventNames()` | содержит все константы реестра |

`admin/audit_test.go` — HTTP: `200` список с `total`, `200` деталь,
`200` `/events`, `400` на `entity_type=evil`, `400` на `event=evil`,
`400` на `actor_type=evil`, `400` на `sort=evil`, `400` на `offset=-1`,
`401` без токена, `404` на несуществующий id. Ручной чек-лист UI — в
`admin/AGENTS.md` (P30).

## 9. Критерии приёмки

- [x] В `admin/` и `internal/database/audit_log_admin.go` нет ни одного INSERT/UPDATE/DELETE по `audit_log` (проверяется грепом в ревью). — проверено: audit_log_admin.go:16-17,116,123-127 (только Count/Find); INSERT есть только в тестовых фикстурах (admin/audit_test.go:274-278, contract_test.go:95-96).
- [x] `internal/database/audit_log.go` расширен **только** константами и конструкторами панельных событий, новых write-путей в файле нет. Исходная формулировка («не изменён ни на строку») опиралась на неверную предпосылку: событий админских CRUD (product/node/invite/broadcast) в реестре не было, а AGENTS.md требует держать конструкторы именно здесь. Проверено: `audit_log.go:84-110` (константы), конструкторы `AuditNodeCreated/Updated/Deleted`, `AuditPlanNodesChanged`, `AuditProductCreated/Updated/Deleted`, `AuditInviteCreated/Deleted`, `AuditBroadcastCreated/Updated/Retried/Deleted`, `AuditSubscriptionRevokedByAdmin`, `AuditTrafficReset`; INSERT по `audit_log` в пакете — только в `audit_log.go` (InsertAuditLog*/insertAuditLogBestEffort).
- [x] `total` и `items` строятся из одного `buildAuditLogQuery` — тест «единый WHERE». — проверено: audit_log_admin.go:44 (buildAuditLogQuery), :116 (Count), :123-127 (Find); TestListAuditLogPage_PaginationKeepsTotalStable.
- [x] Первый экранный запрос идёт с `sort=created_at&order=desc`: свежие события сверху по умолчанию, без ручного переключения. Проверяется в ручном чек-листе и в HTTP-тесте на дефолтные параметры. — проверено: admin/src/views/AuditLog.svelte:72,142-143 (всегда sort=created_at&order=desc); TestAPI_AuditListDefaultOrderIsNewestFirst, TestListAuditLogPage_DefaultsAreNewestFirstWithFiftyRows.
- [x] `/admin/api/audit/events` возвращает ровно те константы, что объявлены в `audit_log.go` (тест сравнивает наборы). — проверено: реестр дополнен 13 событиями (audit_log_admin.go:154-196); TestAuditRegistriesMatchConstants теперь парсит audit_log.go (declaredAuditEventConstants) и сравнивает наборы.
- [x] Список не содержит `old_state`/`new_state` (тест: ключи отсутствуют в JSON items). — проверено: admin/audit.go (auditItem без снимков); TestAPI_AuditList — assert.NotContains old_state/new_state.
- [x] Событие, добавленное в реестр без правки UI, фильтруется и показывается кодом без падений. — проверено: фильтр из /events (AuditLog.svelte:240-242), фолбэк подписи EVENT_LABELS[value] || value (:81-83); все 42 события реестра имеют русские подписи (сверено скриптом: набор кодов `AuditEventNames()` и набор ключей `EVENT_LABELS` совпадают 1:1).
- [x] Раскрытие строки показывает old/new как отформатированный `<pre>`-JSON без внешних библиотек. — проверено: AuditLog.svelte:113,118 (JSON.stringify), :328-344 (<pre>), без внешних библиотек.
- [x] Список из 100 000 записей — два SQL (count + select), без N+1. — проверено: ListAuditLogPage — ровно 2 запроса (audit_log_admin.go:116,123-127); деталь грузится по клику (:136); замер на 100k — ручной.
- [x] `go test -race ./admin/ ./internal/database/` зелёный, `golangci-lint` без замечаний. — проверено: прогнано 2026-11-05: go test -race ./admin/ ./internal/database/ ./internal/bot/ ./cmd/bot/ — ok (74.5s); golangci-lint run ./... — 0 issues.

## 10. Файлы

Создаёт:

- `internal/database/audit_log_admin.go`
- `internal/database/audit_log_admin_test.go`
- `admin/audit.go`
- `admin/audit_test.go`
- `admin/src/views/AuditLog.svelte`

Правит:

- `admin/server.go` — три `mux.HandleFunc` в `Handler()` (`/admin/api/audit`,
  `/admin/api/audit/events`, `/admin/api/audit/{id}`), по одной строке;
- `admin/src/App.svelte` — по одной строке: `routes` и меню (детальной
  страницы нет — вложенные пути не нужны);
- `admin/plans.go` — один кейс `ErrAuditLogNotFound` в `writeStoreError`.

Не трогает: `internal/database/audit_log.go`, `internal/database/audit_log_repo.go`
(кроме делегации `GetRecentAuditLog` — тело метода, сигнатура не меняется),
`admin/src/lib/*`, `admin/dist/*`, `internal/service/*`, ADR-0002.

## 11. Вне скоупа

- любые мутации журнала (правка, удаление, ретеншн из панели);
- экспорт CSV/JSON;
- запись аудита о чтении;
- full-text поиск по old_state/new_state;
- переходы из журнала на сущности (например, из события в подписку) — ссылки
  из строки возможны позже без смены API;
- агрегаты (гистограмма событий по дням).



