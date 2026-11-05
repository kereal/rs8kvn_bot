# 06 — Рассылки

## 1. Цель и границы

Экран готовит и контролирует рассылки: черновик, расписание, аудитория,
прогресс доставки, отмена, повтор неудачной.

**Управление рассылками переезжает в панель целиком.** После этого экрана в
боте не остаётся ни одной точки создания рассылки: `/broadcast`, `/broadcasts`,
сессии черновика, инлайн-клавиатуры фильтров и рендер отчётов удаляются
(перечень — §10). Панель становится единственным способом завести,
запланировать, отменить и повторить рассылку.

Панель по-прежнему **не отправляет сообщения сама**: она кладёт строку в
`broadcasts` со `status="scheduled"`, а `BroadcastWorker` (тик 15 с, батч 100
получателей) забирает её через `GetRunnableBroadcasts` и рассылает. Воркер
живёт в процессе бота, потому что только у него есть Telegram-клиент, но это
**транспорт, а не управление**: он ничего не решает про аудиторию и текст, лишь
исполняет строку из БД. Дублировать отправку в `admin/` нельзя — панель и бот
работают в одном процессе, второй экземпляр воркера недопустим.

Не входит: редактирование текста уже отправленной рассылки, работа с
`recipients_state` напрямую, шаблоны сообщений, A/B-тесты, отложенные серии.

## 2. Данные

```sql
CREATE TABLE broadcasts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  filters TEXT NOT NULL DEFAULT '{}',        -- JSON BroadcastFilter
  message_text TEXT NOT NULL,                -- MarkdownV2
  status TEXT NOT NULL DEFAULT 'scheduled'
    CHECK (status IN ('scheduled','running','completed','failed','canceled')),
  planned_at DATETIME, started_at DATETIME, finished_at DATETIME,
  recipients_total INTEGER NOT NULL DEFAULT 0,
  sent_count INTEGER NOT NULL DEFAULT 0,
  blocked_count INTEGER NOT NULL DEFAULT 0,
  unreachable_count INTEGER NOT NULL DEFAULT 0,
  failed_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  retry_at DATETIME, retry_count INTEGER NOT NULL DEFAULT 0,
  delivery_report TEXT NOT NULL DEFAULT '{}',
  recipients_state TEXT NOT NULL DEFAULT '{"snapshot":false,"recipients":[]}',
  created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
);
```

`BroadcastFilter` (`internal/database/broadcasts.go`):

```json
{"plan_type":"paid|free","subscription_status":"active|revoked|all",
 "registered_after":"RFC3339","registered_before":"RFC3339",
 "inactive_days":7,"ever_paid":true}
```

Семантика (из `applyBroadcastFilter`), которую панель обязана показать честно:

- аудитория — **дедуплицированные** `telegram_id` из `subscriptions` с
  `telegram_id > 0`; пользователи без подписки в рассылку не попадают;
- `subscription_status` пустой = `active`; `revoked` и `all` — явные варианты;
- `expired` как статус фильтра отключает аудиторию полностью (`1 = 0`) —
  в UI такого варианта быть не должно;
- триальные планы исключаются всегда (`plan_id NOT IN (... trial ...)`);
- `plan_type=paid` — `product_id IS NOT NULL OR price_paid_cents > 0`;
- `inactive_days=0` — кто ни разу не обращался (`last_request IS NULL`).

Существующие методы: `CreateBroadcast`, `GetBroadcast`, `ListBroadcasts`,
`UpdateBroadcast` (**только** статус, счётчики, `finished_at`, `retry_at`,
`delivery_report`), `CancelBroadcast` (scheduled|running → canceled),
`GetRunnableBroadcasts`, `SnapshotBroadcastRecipients`,
`GetBroadcastRecipientsStats`, `broadcastAudienceQuery`/`applyBroadcastFilter`
(приватные, в том же пакете — новые методы их переиспользуют).

## 3. Права и guard'ы

| Операция | Условие отказа | Код |
|---|---|---|
| create | `name` пустой или > 255 | `400 validation` `details.name` |
| create | `message_text` пустой или > 4096 символов | `400 validation` `details.message_text` |
| create | неизвестное значение `plan_type`/`subscription_status` | `400 validation` `details.filters` |
| create | `planned_at` в прошлом более чем на 5 минут | `400 validation` `details.planned_at` |
| create | аудитория пуста | `409 in_use`, `details.audience=0` |
| update | статус ≠ `scheduled` (уже пишется или завершена) | `409 in_use`, `details.status` |
| cancel | статус не `scheduled`/`running` | `409 stale` |
| delete | статус `running` | `409 in_use` |
| retry | статус не `failed` | `409 stale` |
| deactivate-blocked | статус не `completed`/`failed` | `409 stale` |
| deactivate-blocked | нет заблокировавших или недоступных | `409 in_use`, `details.blocked=0` |

Создание разрешено только как черновик или запланированная рассылка; отправка
стартует сама, когда наступает `planned_at` (или сразу, если `planned_at`
пустой) и воркер делает очередной тик.

### Передача ответственности с бота (обязательно в том же PR)

Экран и удаление ботовских сценариев делаются **одним коммитом**: промежуточное
состояние, где рассылки нельзя завести вообще, недопустимо.

Удаляется из `internal/bot/`:

| Файл | Что это |
|---|---|
| `broadcast_flow.go` (719) | `/broadcast`: черновик → предпросмотр → подтверждение |
| `broadcast_filters.go` (184) | инлайн-клавиатуры фильтров и расписания |
| `broadcast_callbacks.go` (21) | обработка callback'ов рассылки |
| `broadcast_report.go` (237) | `/broadcasts` и рендер отчёта в чат |
| `admin_broadcast_test.go` (767) | тесты удалённого флоу |
| `broadcast_admin_flow_test.go` (580) | тесты удалённого флоу |

Правится в `internal/bot/handler.go`: убрать команды `broadcast` и `broadcasts`
из списка, поля `broadcastSessions`/`broadcastMu`, ветки в `HandleCommand`,
перехват «сообщение = текст черновика», `broadcastSessionActive` и связанные
хелперы. Пункт меню для админа убирается там же, где строится клавиатура
админ-меню.

Правится `internal/bot/callback.go`: удаляются ветки `broadcast_cancel`,
`broadcast_cancel_<id>`, `broadcast_retry_<id>`, `broadcast_details_<id>`,
`broadcast_deactivate_prompt_<id>`, `broadcast_deactivate_exec_<id>`.
**Ни одна функция при этом не теряется:**

| Что умел бот | Куда переходит |
|---|---|
| отмена | `POST /admin/api/broadcasts/{id}/cancel` → `CancelBroadcast` |
| повтор `failed` | `POST /admin/api/broadcasts/{id}/retry` → `ResetBroadcastRetry` |
| отчёт | детальная страница `#/broadcasts/{id}` (§7) |
| деактивация заблокировавших | `POST /admin/api/broadcasts/{id}/deactivate-blocked` → `SubscriptionService.DeactivateBlockedBroadcastRecipients` |

Последняя — не косметика: бот выключал подписки тех, кто заблокировал его
(`status=revoked` + снятие VPN-доступа) по итогам рассылки. В панели это
кнопка на детальной странице с подтверждением и точными числами
(`blocked_count`, `unreachable_count`) из ответа сервиса
`{targets, revoked, failed}`; частичный сбой показывается как предупреждение,
а не как успех.

**Остаётся:** `broadcast_worker.go` (транспорт), `broadcast_constants.go`,
`broadcast_errors.go` — их используют воркер и `receivedBroadcast`-путь.

Правится в `tests/e2e/admin_test.go`: сценарии `/broadcast`
(`runBroadcastFlow` и три кейса вокруг него) удаляются — они тестируют
удалённый флоу; проверка «не-админ не может `/broadcast`» тоже удаляется
вместе с командой. Тест восстановления рассылки после рестарта
(`broadcastRecipientLease`) остаётся: он проверяет воркер, а не чат-флоу.

Новые e2e-сценарии для панели не пишутся: панель проверяется через
`admin/broadcasts_test.go` и ручной чек-лист.

## 4. API

| Method | Path | Query/Body | Ответ |
|---|---|---|---|
| GET | `/admin/api/broadcasts` | `?limit=&offset=&status=` | `{"items":[…],"total":N}` |
| GET | `/admin/api/broadcasts/{id}` | — | деталь |
| POST | `/admin/api/broadcasts` | broadcast | `201` + деталь |
| PUT | `/admin/api/broadcasts/{id}` | broadcast | `200` + деталь |
| POST | `/admin/api/broadcasts/{id}/cancel` | — | `200` + деталь |
| POST | `/admin/api/broadcasts/{id}/retry` | — | `200` + деталь |
| POST | `/admin/api/broadcasts/{id}/deactivate-blocked` | — | `200` + `{"targets":N,"revoked":N,"failed":N}` |
| DELETE | `/admin/api/broadcasts/{id}` | — | `200` + `{"id":N,"deleted":true}` |
| GET | `/admin/api/broadcasts/audience` | `?plan_type=&subscription_status=&inactive_days=&ever_paid=&registered_after=&registered_before=` | `{"count":N}` |

`limit` по умолчанию 50, максимум 200, сортировка `id desc` (история растёт —
это единственная «растущая» таблица, добавленная сверх списка ADR §3).

DTO списка:

```json
{
  "id": 12,
  "name": "Чёрная пятница",
  "status": "running",
  "planned_at": "2026-11-05T09:00:00Z",
  "started_at": "2026-11-05T09:00:14Z",
  "finished_at": null,
  "recipients_total": 3120,
  "sent_count": 1400,
  "blocked_count": 12,
  "unreachable_count": 3,
  "failed_count": 1,
  "last_error": "",
  "retry_at": null,
  "created_at": "2026-11-04T18:00:00Z"
}
```

Деталь добавляет `filters` (распарсенный объект), `message_text`,
`filters_text` (человекочитаемое `BroadcastFilter.String()`), `retry_count`,
`snapshot_taken` (из `recipients_state.snapshot`), `errors_preview` — первые
20 записей `delivery_report.errors` вида `{"telegram_id":N,"error":"…"}`.

Полный `delivery_report` не отдаётся: массивы доставляемых/заблокированных
могут весить мегабайты, а в UI нужны только счётчики и ошибки.

Тело запроса создания/правки:

```json
{"name":"Чёрная пятница","message_text":"…","filters":{"plan_type":"paid","subscription_status":"active"},"planned_at":"2026-11-05T09:00:00Z"}
```

`planned_at: null` = «отправить при ближайшем тике».

## 5. Сервер

Файл `internal/database/broadcasts_admin.go`:

```go
type BroadcastOverview struct {
    Broadcast
    SnapshotTaken bool
    ErrorCount    int64
}

func (s *Service) ListBroadcastOverviews(ctx context.Context, status string, limit, offset int) ([]BroadcastOverview, int64, error)
func (s *Service) CountBroadcastAudience(ctx context.Context, f BroadcastFilter) (int64, error)
func (s *Service) UpdateBroadcastDraft(ctx context.Context, b *Broadcast) error
func (s *Service) DeleteBroadcast(ctx context.Context, id uint) error
func (s *Service) ResetBroadcastRetry(ctx context.Context, id uint, now time.Time) error
```

Поведение:

- `CountBroadcastAudience` = `broadcastAudienceQuery(ctx, f).Distinct("telegram_id").Count()`;
  никакой отдельной SQL-копии фильтра;
- `UpdateBroadcastDraft` пишет `name`, `filters`, `message_text`, `planned_at`,
  `updated_at` **только** при `status = 'scheduled'`; иначе `ErrBroadcastLocked`.
  `UpdateBroadcast` для этого не годится — он селектит другой набор колонок;
- `DeleteBroadcast` удаляет строку при статусе ≠ `running`, иначе `ErrBroadcastLocked`;
  отсутствующая строка → `ErrBroadcastNotFound`;
- `ResetBroadcastRetry` допускает только `failed` → `status='scheduled'`,
  `retry_at=NULL`, `last_error` сохраняется; иначе `ErrBroadcastLocked`.

Sentinel-ошибки: `ErrBroadcastInvalid`, `ErrBroadcastLocked`,
`ErrBroadcastAudienceEmpty`, `ErrBroadcastNotFound` (существует).

Хендлеры `admin/broadcasts.go` вызывают только эти методы. Отправки нет.

## 6. Аудит

| Операция | Событие |
|---|---|
| создание | `broadcast_created` (`entity_type=broadcast`, `entity_id=id`) |
| правка черновика | `broadcast_updated` (old/new: `name`, `filters`, `planned_at`; `message_text` — **хэш и длина**, не текст) |
| отмена | `broadcast_canceled` |
| повтор | `broadcast_retried` (`details.retry_count`) |
| удаление | `broadcast_deleted` |

`message_text` не пишется в аудит целиком: это пользовательский контент, он уже
лежит в таблице. Хранится `{"len":1234,"sha256":"…"}`.

Конструкторы — в `internal/database/audit_log.go`, `actor_type=admin`,
`actor_id=0`.

## 7. UI

`admin/src/views/Broadcasts.svelte` (список), `admin/src/views/BroadcastDetail.svelte`
(`#/broadcasts/{id}`, id приходит пропом — роутер общий, см. `00-common.md`),
форма — в `Broadcasts.svelte` как модалка (≤ 12 полей). Запросы — через
`api.get/post/put/del`.

Список: `ID · НАЗВАНИЕ · СТАТУС · ПРОГРЕСС · ПЛАН · НАЧАЛО · ОШИБКИ · ДЕЙСТВИЯ`.

- прогресс — полоска `sent/total` + текст `1400 / 3120`; для `scheduled` — «ждёт 09:00»;
- `failed` — красный бейдж + `last_error` в тултипе;
- ошибки — `failed_count`; 0 серым;
- действия: «Открыть», «Отменить» (только scheduled/running), «Повторить» (только failed), «Удалить» (не running).

Форма (создание/правка черновика):

1. название;
2. фильтры: `plan_type` (все / платные / бесплатные), `subscription_status`
   (активные / отозванные / все), «не заходили, дней» (число, 0 = ни разу),
   «платили хотя бы раз» (чекбокс с трёхсостоянием: не важно / да / нет),
   даты регистрации `после`/`до`;
3. предпросмотр аудитории: `«Получателей: 3120»`, обновляется с debounce 400 мс,
   при 0 — предупреждение и заблокированная кнопка «Сохранить»;
4. текст сообщения: `textarea`, счётчик `1234 / 4096`, подсказка «длинный текст
   уйдёт несколькими сообщениями» (бот режет по 4096);
5. расписание: «сразу после сохранения» или дата-время (локаль, отправляется UTC).

Подтверждение сохранения — обязательный диалог с точным числом получателей:
«Отправить 3120 пользователям?» (при `planned_at` — «Отправить 3120
пользователям 05.11 в 12:00?»). Без подтверждения с числом — не отправлять
запрос.

Деталь: карточка «Прогресс» (полоска, счётчики доставлено/заблокировано/
недоступно/ошибок), карточка «Аудитория» (`filters_text` + число),
карточка «Текст» (monospace, без рендера MarkdownV2), таблица первых 20 ошибок
(`telegram_id`, причина), панель действий.

Состояния: загрузка, пусто («Рассылок ещё не было»), ошибка, «только чтение»
для `completed`/`canceled`.

## 8. Тесты

`internal/database/broadcasts_admin_test.go`:

| Кейс | Ожидание |
|---|---|
| count аудитории с пустым фильтром | равен числу активных неоплаченных бесплатных подписок, триал исключён |
| count с `plan_type=paid` | только с `product_id`/`price_paid_cents` |
| count с `subscription_status=expired` | 0 (фильтр отключает аудиторию) |
| count дедуплицирует | два активных подписки одного telegram_id дают 1 |
| update draft scheduled | поля изменились, `message_text` новый |
| update running | `ErrBroadcastLocked` |
| delete running | `ErrBroadcastLocked` |
| delete completed | строка удалена |
| reset retry на failed | `status=scheduled`, `retry_at=NULL` |
| reset retry на completed | `ErrBroadcastLocked` |
| пагинация | items не пересекаются, `total` стабилен |

`admin/broadcasts_test.go` — HTTP: `201` создание с аудиторией,
деактивация заблокировавших на `completed` возвращает счётчики и вызывает
сервис, на `running` — `409 stale`, `400` на пустой
текст и на `filters={"plan_type":"vip"}`, `409` при пустой аудитории, `409`
на правку running, `200` отмена, `401` без токена, отсутствие
`delivery_report` в ответе списка.

## 9. Критерии приёмки

- [x] Ни один маршрут панели не отправляет сообщения в Telegram (проверяется отсутствием `tgbotapi` в `admin/`). — проверено: в admin/ нет tgbotapi (греп 0); создание пишет только строку — admin/broadcasts.go:339-345, admin/broadcasts_test.go:17-19.
- [x] Созданная рассылка реально уходит: после сохранения черновика воркер берёт её в течение ~15 с (проверка вручную на тестовом боте). — проверено: e2e TestE2E_BroadcastDraftDeliveredAfterCreate (tests/e2e/admin_test.go) — черновик scheduled уходит получателю через прод-воркер StartBroadcastWorker ровно один раз, recipients_total=1, событие broadcast_started несёт реальный размер аудитории; интервал тика 15 с — internal/bot/broadcast_constants.go:32. Реальная сетевая доставка в Telegram в e2e подменена фейком и остаётся ручной проверкой на тестовом боте.
- [x] Число получателей в диалоге совпадает с `recipients_total` после старта. — проверено: SnapshotBroadcastRecipients публикует recipients_total в той же транзакции (internal/database/broadcast_recipients.go), TestSnapshotBroadcastRecipients_PublishesTotalWhileRunning; диалог и снапшот делят предикат — internal/database/broadcasts_admin.go:104-110.
- [x] Правка и удаление заблокированы для `running` и для завершённых (кроме удаления завершённых). — проверено: admin/broadcasts.go:223-227,323; ErrBroadcastLocked — internal/database/broadcasts_admin.go:150-153; TestAPI_BroadcastsUpdate_Running, TestAPI_BroadcastsDelete.
- [x] `message_text` не попадает в аудит (только длина и хэш). — проверено: audit_log.go:770-789 (только len + sha256), diff лишь изменённых полей :806-809.
- [x] Ошибки доставки видны в детали (первые 20) и их число — в списке. — проверено: broadcastErrorsPreview=20 (admin/broadcasts.go:46, срез :466-483), failed_count в списке (:73,452); TestAPI_BroadcastsDetail_ErrorsPreview, TestAPI_BroadcastsList_NoDeliveryReport.
- [x] Отмена переводит `running` в `canceled`, и воркер прекращает отправку на следующем батче. — проверено: CancelBroadcast — internal/database/broadcast_recipients.go:318-333; панель admin/broadcasts.go:270-286; воркер проверяет статус перед батчем (internal/bot/broadcast_worker.go:159-166) и снимает контекст :529-542; TestBroadcastRecipientClaimStopsAfterCancel, TestAPI_BroadcastsCancel.
- [x] В боте нет команд `/broadcast` и `/broadcasts`, нет сессий черновика и инлайн-клавиатур рассылки. — проверено: греп /broadcast|bfilter_|broadcast_cancel|broadcast_details|broadcast_retry по internal/bot = 0; нет сессий черновика (internal/bot/handler.go:731-733); command.go без broadcast.
- [x] `go test ./internal/bot/ ./tests/e2e/` зелёный после удаления флоу (остались только тесты воркера). — проверено: прогнано 2026-11-05: go test ./internal/bot/ ./tests/e2e/ -count=1 — ok (2.8s / 7.6s), плюс -race ./internal/bot/ — ok; тесты флоу команд удалены, остались тесты воркера.
- [x] В процессе ровно один `BroadcastWorker` (панель не запускает второй). — проверено: единственный старт — cmd/bot/main.go:686 (StartBroadcastWorker), создание один раз — internal/bot/handler.go:127; в admin/ нет StartBroadcastWorker/NewBroadcastWorker.
- [x] Деактивация заблокировавших доступна из панели и повторяет поведение бота (revoked + deprovision), включая частичный сбой. — проверено: seam BroadcastDeactivator (admin/broadcasts.go:36-40, admin/server.go:133-138); сервис revoked+deprovision+частичный сбой — internal/service/subscription_deactivate.go; TestDeactivateBlockedBroadcastRecipients_RevokesAndDeprovisions/_ReportsPartialFailure/_RetriesFailedDeprovision, HTTP TestAPI_BroadcastsDeactivate_Completed/_PartialFailure/_Guards.

## 10. Файлы

Создаёт:

- `internal/database/broadcasts_admin.go` + `_test.go`
- `admin/broadcasts.go` + `_test.go`
- `admin/src/views/Broadcasts.svelte`, `admin/src/views/BroadcastDetail.svelte`

Изменяет: `internal/database/audit_log.go` (константы и конструкторы
`broadcast_*`).

Правит одной строкой: `routes` и меню в `App.svelte`, маршруты в `Handler()`.

Изменяет `internal/bot/` в объёме §3.1 (удаление авторского флоу) и
`tests/e2e/admin_test.go` (удаление `runBroadcastFlow` и его кейсов).

Не трогает: `broadcast_worker.go`, `broadcast_constants.go`,
`broadcast_errors.go`, `admin/src/lib/*`, `admin/dist/*`.

## 11. Вне скоупа

- отправка сообщений из панели (только очередь БД);
- редактирование текста после старта рассылки;
- шаблоны и персонализация (`{name}` и т.п.);
- работа со списком получателей (просмотр `recipients_state`, повтор отдельным
  адресатам);
- планировщик серий и отложенных цепочек;
- предпросмотр MarkdownV2-рендеринга (риск расхождения с Telegram — не делаем).
