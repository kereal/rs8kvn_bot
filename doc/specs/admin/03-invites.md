# 03 — Инвайты

Эталон структуры: [01-products.md](01-products.md). Контракт: [ADR-0002](../../adr/0002-admin-panel-contract.md) (§2 `invites`, §7 удаление).

## 1. Цель и границы

Экран показывает реферальные коды (`invites`) и их эффективность: сколько пользователей пришло по коду и сколько из них превратилось в подписки. Админ может создать код от имени пользователя и удалить неиспользованный.

Не входит: начисление реферальных наград (бот), правка `referrer_tg_id` у существующего кода, статистика подписок по коду глубже двух счётчиков, деактивация использованных кодов (нет поля в схеме — см. §3 и §11).

## 2. Данные

Схема — миграции [002](../../internal/database/migrations/002_add_invites_and_trials.up.sql), [003](../../internal/database/migrations/003_add_referral_columns.up.sql), [004](../../internal/database/migrations/004_add_unique_referrer_tg_id.up.sql), [005](../../internal/database/migrations/005_cleanup_duplicate_invites.up.sql):

```sql
CREATE TABLE invites (
  code VARCHAR(16) PRIMARY KEY,
  referrer_tg_id BIGINT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_invites_referrer ON invites(referrer_tg_id);
-- 004/005: дубли вычищены, затем:
CREATE UNIQUE INDEX idx_invites_referrer_unique ON invites(referrer_tg_id);
```

Связанные колонки подписок (003): `subscriptions.invite_code VARCHAR(16)` и `subscriptions.referred_by BIGINT`, обе с индексами.

Факты, на которые опирается реализация:

- **у одного `referrer_tg_id` максимум один код** — уникальный индекс из 004; «несколько кодов от одного человека» невозможно;
- **поле активности/отзыва в схеме нет** — только PK, `referrer_tg_id`, `created_at`; «отозвать» = физически удалить (см. §3);
- «использован» определяется по данным подписок, не инвайта: код считался использованным, если существует `subscriptions.invite_code = code` (факт создания по коду; 003 — колонка ведётся ботом, индекс есть). `GetReferralCount` считает по `referred_by` (`internal/database/invites.go:81`) — это число **привязанных** рефералов, оно может отличаться от числа подписок с `invite_code` (trial без привязки пишет `invite_code`, но не `referred_by`) [INFERENCE: расхождение счётчиков — следствие двух независимых колонок; ТЗ показывает обе, не выбирая «истинную»];
- существующие методы `internal/database/invites.go`: `GetInviteByReferrer`, `GetOrCreateInvite(referrerTGID, code)` (upsert `ON CONFLICT(referrer_tg_id) DO NOTHING`), `GetInviteByCode` (оборачивает `ErrInviteNotFound`), `GetReferralCount(referrerTGID)`, `GetAllReferralCounts() (map[int64]int64, error)`;
- алгоритм генерации кода ботом: `utils.GenerateInviteCode()` (`internal/utils/uuid.go:58`) — 8 символов `[0-9a-z]`, `crypto/rand` c rejection sampling; экран **повторно использует** эту функцию (`internal/utils` — не сервисный слой, импорт разрешён);
- бот валидирует код из deep-link через `GetInviteByCode` — код, созданный панелью с нарушением `[a-z0-9]{6,16}`, ломал бы генерацию ссылки (`t.me/{bot}?start=share_{code}`, `%s/i/{code}`), поэтому валидация обязательна;
- `ErrInviteNotFound` уже есть в `models.go`; `ErrInviteInvalid`, `ErrInviteNameTaken` — новые (см. §5).

## 3. Права и guard'ы

| Операция | Условие отказа | Код |
|---|---|---|
| create | `code` не матчит `^[a-z0-9]{6,16}$` (когда задан вручную) | `400 validation` `details.code` |
| create | `referrer_tg_id` ≤ 0 | `400 validation` `details.referrer_tg_id` |
| create | `code` уже существует | `409 name_taken`, `details.field="code"` |
| create | у `referrer_tg_id` уже есть код | `409 name_taken`, `details.field="referrer_tg_id"` |
| delete | есть подписки с `invite_code = code` | `409 in_use`, `details.subscriptions=N` |

Дополнительные правила:

- **Использованный инвайт не удаляется** (ADR-0002 §2). Помечать нечем: поля активности в схеме нет, миграция вне скоупа экрана → использованный код защищён guard'ом `409 in_use` (P7: физическое удаление только строк без зависимостей). Противоречие с формулировкой ADR «только помечается» зафиксировано в §11: без миграции («добавить `revoked_at`») помечать нечем, guard — честная минимальная замена.
- `referrer_tg_id` иммутабелен: правка кода/владельца разорвала бы связь с `subscriptions.referred_by` и ботовыми ссылками. Смена = удалить и создать.
- Создание кода от имени пользователя, у которого код уже есть, — ошибка: вернуть существующий было бы «тихим сюрпризом» (админ думал, что создал новый). Отдельный код для того же `referrer_tg_id` невозможен по уникальному индексу 004.
- Коды нечувствительны к регистру только по факту валидации: принимаются строго lowercase, `ABC123` → `400 validation` (не auto-lowercase: админ должен видеть, что ввёл).

## 4. API

| Method | Path | Query/Body | Ответ | Ошибки |
|---|---|---|---|---|
| GET | `/admin/api/invites` | — | `{"items":[InviteDTO…],"total":N}` | 401 |
| POST | `/admin/api/invites` | invitePayload | `201` + InviteDTO | 400, 401, 409 |
| DELETE | `/admin/api/invites/{code}` | — | `200` + удалённый InviteDTO | 401, 404, 409 |

Полный список без пагинации (P7): инвайты — сотни максимум; поиск и сортировка клиентские.

DTO ответа:

```json
{
  "code": "a7x9k2m1",
  "referrer_tg_id": 123456789,
  "referred_count": 4,
  "subscriptions_count": 5,
  "created_at": "2026-10-01T10:00:00Z"
}
```

- `referred_count` — `COUNT(subscriptions WHERE referred_by = referrer_tg_id)`, через один запрос с `GetAllReferralCounts` (map, без N+1);
- `subscriptions_count` — `COUNT(subscriptions WHERE invite_code = code)` — тем же запросом: `LEFT JOIN` двух агрегатов на `invites`, как в `ListPlanOverviews`;
- оба счётчика нужны и UI (колонки), и guard'у удаления; `subscriptions_count` > 0 ⇔ инвайт использован.

Тело create: `{"code":str,"referrer_tg_id":int}`. Пустой `code` = сгенерировать сервером (`utils.GenerateInviteCode()`, 8 символов `[0-9a-z]`); непустой — ручной ввод с валидацией §3. `updated_at` нет — колонки в схеме нет, поле в DTO не выдумывается.

Коллизия сгенерированного кода с существующим: PK-конфликт при INSERT → повтор генерации до 5 попыток, далее `500 internal` (вероятность пренебрежима; поведение зафиксировано тестом на мок-последовательности не требуется, проверяется только ручной ввод).

## 5. Сервер

Файл `internal/database/invites_admin.go`:

```go
// InviteOverview — инвайт со счётчиками использования для списка и guard'а.
type InviteOverview struct {
    Invite
    ReferredCount      int64
    SubscriptionsCount int64
}

func (s *Service) ListInviteOverviews(ctx context.Context) ([]InviteOverview, error)
func (s *Service) CreateInviteAdmin(ctx context.Context, invite *Invite) error
func (s *Service) DeleteInvite(ctx context.Context, code string) (Invite, error)
```

Sentinel-ошибки (`models.go` рядом с `ErrInviteNotFound`):

- `ErrInviteInvalid` — валидация кода/referrer (400);
- `ErrInviteNameTaken` — занятый код или уникальность `referrer_tg_id` (409); несущий `Details map[string]string{"field": "code"|"referrer_tg_id"}`;
- `ErrInviteInUse` — есть подписки по коду (409), несущий `Details map[string]int64{"subscriptions": N}`.

Правила реализации:

- валидация в репозитории (`validateInviteCode`: regexp `^[a-z0-9]{6,16}$`; `referrer_tg_id > 0`), не только в хендлере;
- `CreateInviteAdmin`: два предварительных SELECT (`code` занят → `ErrInviteNameTaken{field:"code"}`; `referrer` занят → `{field:"referrer_tg_id"}`), затем `INSERT`; PK/FK-ошибку SQLite из-за гонки маппить в тот же `ErrInviteNameTaken` (классификация по тексту ошибки — образец `classifyPlanWriteError`);
- `DeleteInvite`: `SELECT COUNT(*) FROM subscriptions WHERE invite_code = ?` → при > 0 `ErrInviteInUse`, иначе `DELETE` и возврат удалённой строки; несуществующий код → `ErrInviteNotFound`;
- генерация кода — в хендлере `admin/invites.go` (импорт `utils.GenerateInviteCode`): репозиторий получает уже готовый код, не решает «генерировать или нет»;
- существующие методы `invites.go` (ботовые) не трогаются и экраном не используются — кроме отсутствующих аналогов списка.

Файл `admin/invites.go` — узкий интерфейс:

```go
type InviteStore interface {
    ListInviteOverviews(ctx context.Context) ([]database.InviteOverview, error)
    CreateInviteAdmin(ctx context.Context, invite *database.Invite) error
    DeleteInvite(ctx context.Context, code string) (database.Invite, error)
}
```

Маппинг sentinel'ов — ветки в `writeStoreError` (`admin/plans.go`): `ErrInviteNotFound`→404, `ErrInviteInvalid`→400, `ErrInviteNameTaken`→409 `name_taken`, `ErrInviteInUse`→409 `in_use`.

Маршруты в `Handler()` (`admin/server.go`):

```go
mux.HandleFunc("GET /admin/api/invites", s.withAuth(s.handleListInvites))
mux.HandleFunc("POST /admin/api/invites", s.withAuth(s.handleCreateInvite))
mux.HandleFunc("DELETE /admin/api/invites/{code}", s.withAuth(s.handleDeleteInvite))
```

`{code}` — PathValue без преобразования в uint (PK строковый); пустой/некорректный путь → 404 маршрутом.

## 6. Аудит

Новые конструкторы в `internal/database/audit_log.go` (нужна и константа типа сущности — `EntityInvite` сейчас отсутствует в реестре; CHECK-констрейнтов на `entity_type` нет, миграция не нужна):

| Событие | Константа | entity_type | old/new state |
|---|---|---|---|
| создан | `AuditEventInviteCreated = "invite_created"` | `invite` | `nil` → снимок |
| удалён | `AuditEventInviteDeleted = "invite_deleted"` | `invite` | снимок → `nil` |

- `EntityInvite EntityType = "invite"` — новая константа рядом с `EntityNode`;
- снимок: `code`, `referrer_tg_id`, `created_at` (счётчики в снимок не включаются — это производные данные, а не состояние строки);
- событие `invite_created` пишется и при генерации сервером, и при ручном вводе — источник кода в `details.origin` (`"generated"` | `"manual"`);
- аудит — после успешной мутации, best-effort, ошибка логируется `Warn`.

## 7. UI

`admin/src/views/Invites.svelte`.

Таблица: `КОД · ТЕЛЕГРАМ ID · СОЗДАН · ПРИГЛАШЕНО · ПОДПИСОК · ДЕЙСТВИЯ`.

- код — моноширинным, с кнопкой копирования (`navigator.clipboard`);
- приглашено — `referred_count`; подписок — `subscriptions_count`; при 0 серым; `subscriptions_count > 0` ⇔ бейдж «использован» рядом с кодом;
- created_at — `formatDate`.

Панель фильтров (клиентские): поиск по коду, поиск по Telegram ID (одно поле: чисто число → сравнение с `referrer_tg_id`, иначе — подстрока кода), сортировка по клику на заголовок (created_at — по умолчанию desc, code).

Модалка создания (`<dialog>`): Telegram ID (число, обязательное), код (опциональный, placeholder «сгенерируется автоматически — 8 символов»); валидация `[a-z0-9]{6,16}` на клиенте дублирует серверную, ошибки поля — из `details` ответа.

Удаление: confirm; у использованного кнопка disabled с подсказкой «код уже использован»; при сработавшем guard'е `in_use` — текст с числом («нельзя удалить: 5 подписок по этому коду»).

Состояния: загрузка, пусто («Пока нет инвайтов» + кнопка создания), ошибка (`humanError` + «Повторить»). Запросы — через общие `api.get` / `api.post` / `api.del` (`src/lib/api.js`); именованные обёртки в общий файл не добавляются (см. `00-common.md`).

## 8. Тесты

`internal/database/invites_admin_test.go` — на реальной SQLite:

| Кейс | Ожидание |
|---|---|
| create вручную валидный | строка есть, аудит `invite_created`, `details.origin="manual"` |
| create с пустым кодом | код из 8 символов `[0-9a-z]`, аудит с `origin="generated"` |
| create `code="A7X9K2M1"` (upper) | `ErrInviteInvalid` |
| create `code="ab1"` / 17 символов / с дефисом | `ErrInviteInvalid` |
| create `referrer_tg_id=0` / отрицательный | `ErrInviteInvalid` |
| create дубль кода | `ErrInviteNameTaken`, `Details["field"]=="code"` |
| create второй код тому же referrer | `ErrInviteNameTaken`, `Details["field"]=="referrer_tg_id"` |
| ListInviteOverviews | счётчики `referred_count`/`subscriptions_count` верны для двух инвайтов (один с подписками, один без), без N+1 |
| delete неиспользованный | строка удалена, возвращён DTO, аудит `invite_deleted` |
| delete использованный | `ErrInviteInUse`, `Details["subscriptions"]==N` |
| delete несуществующий | `ErrInviteNotFound` |
| subscriptions только с `invite_code` (trial без привязки) | `subscriptions_count=1`, `referred_count=0` |

`admin/invites_test.go` — HTTP: `200` список с `total` и счётчиками, `201` создание (manual и generated), `400` на неизвестное поле и на `code="BAD"`, `401` без токена, `404` на чужой код, `409` на дубли кода/referrer и на удаление использованного.

## 9. Критерии приёмки

- [x] Сгенерированный код совпадает по формату с ботовым (8 символов `[0-9a-z]`) — код из панели работает в `t.me/{bot}?start=share_{code}` без правок бота. — проверено: internal/utils/uuid.go:58-91 (8 симв. [0-9a-z]), admin/invites.go:67-71; admin/invites_test.go:103.
- [x] Код не проходит валидацию `[a-z0-9]{6,16}` → `400 validation`, а не запись в БД (тест). — проверено: invites_admin.go:80-82 → ErrInviteInvalid → admin/plans.go:154-155 (400 validation); TestCreateInviteAdmin_RejectsBadCode.
- [x] Дубль кода и второй код тому же referrer дают `409 name_taken` с разными `details.field` (тесты). — проверено: invites_admin.go:90-107; TestAPI_InviteCreateGuardsMapToConflict, TestCreateInviteAdmin_DuplicateCode, ..._SecondCodeForSameReferrer.
- [x] Удаление использованного инвайта блокируется `409 in_use` с числом подписок (тест). — проверено: invites_admin.go:141-152 (InviteInUseError{subscriptions}); admin/invites_test.go:170-178, TestDeleteInvite_UsedInviteIsBlocked.
- [x] Список считает `referred_count` и `subscriptions_count` одним SQL без N+1. — проверено: invites_admin.go:39-69 (один SQL, два LEFT JOIN); TestListInviteOverviews_CountsBothMetrics.
- [x] Ни один маршрут не отдаёт что-либо кроме DTO (модель БД в JSON не утекает). — проверено: inviteItem (admin/invites.go:46-61,94-98,115-119); admin/contract_test.go:185-191.
- [x] Мутации пишут аудит `actor_type=admin`, `actor_id=0` с `origin` для created. — проверено: audit_log.go:1046-1066 (ActorAdmin,"0",origin для created); invites_admin.go:115,156; TestCreateInviteAdmin_ManualCodeWritesAudit.
- [x] `go test -race ./admin/` зелёный, `golangci-lint` без замечаний. — проверено: прогнано 2026-11-05: go test -race ./admin/ ./internal/database/ ./internal/bot/ ./cmd/bot/ — ok (74.5s); golangci-lint run ./... — 0 issues.

## 10. Файлы

Создаёт:

- `internal/database/invites_admin.go`
- `internal/database/invites_admin_test.go`
- `admin/invites.go`
- `admin/invites_test.go`
- `admin/src/views/Invites.svelte`

Правит одной строкой: `routes` и меню в `admin/src/App.svelte`, `mux.HandleFunc` в `Handler()` (`admin/server.go`), ветки sentinel'ов в `writeStoreError` (`admin/plans.go`), sentinel-ошибки в `internal/database/models.go`, конструкторы аудита и `EntityInvite` в `internal/database/audit_log.go`.

Не трогает: `internal/database/invites.go` (ботовые методы), `internal/utils/*` (`GenerateInviteCode` — только импорт), `admin/src/lib/*`, `admin/dist/*`, `internal/service/*`.

## 11. Вне скоупа

- миграция «пометка отзыва» (`revoked_at` / `is_active` у invites): поле в схеме отсутствует, экран работает guard'ом `409 in_use`; если понадобится честное «отозвать без удаления» — отдельная миграция и ревизия ADR-0002 §2 (`invites`: «только помечается») — **вне скоупа этого экрана**;
- правка существующего инвайта (`PUT` отсутствует намеренно);
- реферальная статистика (воронка, награды, суммы) — данные подписок, экран 04;
- массовая генерация кодов, импорт кодов списком;
- пагинация, серверный поиск и сортировка (P7: полный список, клиентская фильтрация).
