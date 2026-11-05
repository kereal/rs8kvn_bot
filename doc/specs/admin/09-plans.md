# ТЗ админ-панели: экран «Планы»

Каталог тарифов: 9-й экран панели, самый первый по времени (собран до того, как
появился общий список ТЗ в `00-common.md`). Здесь он задним числом приведён к
шаблону и снабжён критериями приёмки с доказательствами.

## 1. Цель и границы

Экран отвечает на два вопроса: какие тарифы существуют и сколько продуктов и
подписок на них ссылается (это определяет, можно ли тариф удалить).

Делает:

- список планов с лимитами и счётчиками использования;
- создание, правку `name`/`devices_limit`/`traffic_limit`/`is_active`, удаление.

Не делает:

- не меняет привязки «план ↔ нода» — это экран «Ноды» (`02-nodes.md`);
- не пересчитывает существующие подписки: правка лимитов плана не трогает
  провижининг, ноды и клиентов панели 3x-ui — новый лимит действует на
  подписку только после следующего применения плана (`AdminSetPlan`);
- не управляет ценами и длительностью — цена живёт в продуктах (`01-products.md`),
  `plans.price` и `plans.duration` удалены миграциями 016 и 019;
- не включает/не выключает воркеры синхронизации.

## 2. Данные

Текущая схема `plans` (после 007, 016, 019, 024):

| Колонка | Тип | Смысл |
|---|---|---|
| `id` | INTEGER PK | — |
| `name` | VARCHAR(50), UNIQUE (`idx_plans_name`) | имена `free` и `trial` — нагрузочные |
| `is_active` | BOOLEAN NOT NULL DEFAULT true | добавлена миграцией 024 |
| `devices_limit` | INTEGER DEFAULT 1 | **хранится и отображается, ядром не ограничивается** (см. §11) |
| `traffic_limit` | INTEGER NOT NULL, байты | `0` = безлимит; миграция 040 поставила `free` = 20 GiB |
| `created_at` / `updated_at` | DATETIME | `autoCreateTime` / `autoUpdateTime` |

Модель — `internal/database/models.go:142`.

Счётчики `products_count` / `subscriptions_count` считаются подзапросами в том же
SELECT, что и список (`ListPlanOverviews`): отдельный запрос на строку дал бы
N+1 при обходе списка.

### Нагрузочные имена

`GetPlanByName` (`internal/database/plans.go:13`) фильтрует `is_active = true`
для всех имён, кроме `free`. Отсюда два следствия, закодированные в guard'ах:

- `trial` нельзя деактивировать — деактивированный trial-план перестал бы
  находиться по имени, и выдача пробных подписок сломалась бы;
- `free` деактивировать можно — он резолвится и будучи неактивным.

## 3. Права и guard'ы

| Операция | Условие отказа | Sentinel | HTTP |
|---|---|---|---|
| create | имя пустое / > 50 символов / отрицательные лимиты | `ErrPlanInvalid` | 400 |
| create | имя уже занято (в т.ч. после trim) | `ErrPlanNameTaken` | 409 `name_taken` |
| update | плана нет | `ErrPlanNotFound` | 404 |
| update | переименование `free` или `trial` | `ErrPlanProtected` | 409 `protected` |
| update | `trial` выключают (`is_active=false`) | `ErrPlanProtected` | 409 `protected` |
| update | валидация полей | `ErrPlanInvalid` | 400 |
| delete | плана нет | `ErrPlanNotFound` | 404 |
| delete | имя `free` или `trial` | `ErrPlanProtected` | 409 `protected` |
| delete | есть продукты или подписки | `ErrPlanInUse` | 409 `in_use` |

Администратор один, ролей нет: доступ — общий bearer-токен (`ADMIN_TOKEN`),
как и на всех экранах (ADR-0002 §3).

Удаление физическое (P21). Каскад по `products.plan_id` — причина guard'а
`ErrPlanInUse`: молчаливое удаление стёрло бы карточки каталога вместе с
историей заказов.

## 4. API

| Method | Path | Body | Ответ |
|---|---|---|---|
| GET | `/admin/api/plans` | — | `{"items": [...]}`, порядок по `id` |
| POST | `/admin/api/plans` | `{"name","devices_limit","traffic_limit","is_active"}` | 201 `{"id": N}` |
| PUT | `/admin/api/plans/{id}` | то же | 200 `{"ok":true}` |
| DELETE | `/admin/api/plans/{id}` | — | 200 `{"ok":true}` |

Элемент списка:

```json
{
  "id": 3, "name": "premium", "is_active": true,
  "devices_limit": 3, "traffic_limit": 1073741824,
  "products_count": 1, "subscriptions_count": 4,
  "created_at": "2026-11-05T12:00:00Z", "updated_at": "2026-11-05T12:00:00Z"
}
```

Тело декодируется `DisallowUnknownFields` + проверкой хвоста: опечатка в имени
поля (`device_limit`) даёт 400, а не «сохранилось как было». `{id}` парсится
как `uint` и `0` отвергается (400 `bad_request`). Неверный метод на существующем
пути отдаёт 405 от `http.ServeMux` (шаблоны с методом, `admin/server.go:147`).

## 5. Сервер

`internal/database/plans_admin.go`:

| Метод | Поведение |
|---|---|
| `ListPlanOverviews(ctx) ([]PlanOverview, error)` | список + счётчики, `ORDER BY plans.id` |
| `CreatePlan(ctx, *Plan) error` | trim имени, валидация, вставка, аудит |
| `UpdatePlan(ctx, *Plan) error` | в транзакции: загрузка текущей строки, guard'ы, `Updates`; аудит после коммита |
| `DeletePlan(ctx, id) error` | в транзакции: guard'ы, подсчёт зависимостей, `DELETE`; аудит после коммита |

Валидация живёт в репозитории (`validatePlanFields`, строка > 50 символов и
отрицательные лимиты), а не только в HTTP-обработчике: панель — не единственный
возможный вызывающий. Уникальность имени обеспечивает индекс, а не предварительный
`SELECT`: `classifyPlanWriteError` превращает `UNIQUE constraint failed: plans.name`
в `ErrPlanNameTaken` (гонка двух create не оставляет 500).

Аудит пишется **после** коммита транзакции: `insertAuditLogBestEffort` внутри
tx-замыкания дедлочится на SQLite (AGENTS.md, раздел про audit_log).

## 6. Аудит

| Событие | Конструктор | `old_state` / `new_state` |
|---|---|---|
| `plan_created` | `AuditPlanCreated` | — / снапшот |
| `plan_updated` | `AuditPlanUpdated` | только изменившиеся поля (diff) |
| `plan_deleted` | `AuditPlanDeleted` | снапшот / — |

Актор — `actor_type=admin`, `actor_id="0"` (0 = панель, Telegram ID всегда > 0,
ADR-0002 P20). Снапшот: `name`, `devices_limit`, `traffic_limit`, `is_active`
(счётчики не входят — производные данные). Отклонённая мутация события не
пишет: журнал фиксирует факт изменения, а не попытку.

## 7. UI

`admin/src/views/Plans.svelte`: поиск по названию, список с сортировкой по
ID/названию/трафику/устройствам/продуктам/подпискам, кнопка «Новый план».

Форма (модальное окно «Новый план» / «Изменить план»): название (заблокировано
для `free`/`trial` с подсказкой «Имя базового плана менять нельзя — по нему ищет
код бота»), лимит трафика в гигабайтах с подсказкой «Хранится в байтах: …»,
лимит устройств, чекбокс активности с подсказкой про trial.

Удаление: кнопка заблокирована, если план защищён или на него ссылаются продукты
и подписки, а `title` объясняет причину; иначе — `confirm()` с именем и
предупреждением о необратимости. Четыре состояния списка: загрузка, пусто,
ошибка, данные.

## 8. Тесты

| Кейс | Где |
|---|---|
| список с счётчиками и порядком по `id` | `TestAPI_PlansListReturnsUsageCounts` |
| create → update → перечитывание из списка, 404 на чужой id | `TestAPI_PlanCreateUpdateRoundTrip` |
| пустое имя, отрицательный лимит, дубль, неизвестное поле, битый JSON, `{id}=abc`, 405 на коллекцию | `TestAPI_PlanValidationFailuresAreLoud` (7 подкейсов) |
| `in_use` с числом зависимостей, `protected` для trial, успешное удаление, повторное 404 | `TestAPI_PlanDeleteGuardsAndSuccess` |
| trim имени, дубль на уровне репозитория, отрицательные лимиты | `TestCreatePlan_TrimsNameAndValidatesFields`, `TestCreatePlan_RejectsDuplicateName` |
| guard'ы `free`/`trial` при update | `TestUpdatePlan_GuardsDefaultPlans` |
| счётчики в `ListPlanOverviews` | `TestListPlanOverviews_CountsProductsAndSubscriptions` |
| аудит: created/updated/deleted, актор admin/0, diff, отсутствие события при отказе, переживание удаления строки | `TestPlanAuditEvents` |

## 9. Критерии приёмки

- [x] Список отдаёт счётчики использования и порядок по `id` — `TestAPI_PlansListReturnsUsageCounts`.
- [x] Создание возвращает 201 с `id`, правка — 200 `{"ok":true}`, изменения видны в следующем списке — `TestAPI_PlanCreateUpdateRoundTrip`.
- [x] Валидация и конфликты отдают 400/409 с кодом (`bad_request`, `name_taken`), неизвестное поле тела отвергается — `TestAPI_PlanValidationFailuresAreLoud`, `admin/plans.go:188`.
- [x] Удаление невозможно при зависимостях и для защищённых имён; сообщение содержит число зависимостей — `TestAPI_PlanDeleteGuardsAndSuccess`, `internal/database/plans_admin.go:172`.
- [x] Каждая успешная мутация пишет событие `plan_created`/`plan_updated`/`plan_deleted` с `actor_type=admin`, `actor_id="0"`; при отказе события нет; удалённый план остаётся в журнале — `TestPlanAuditEvents`.
- [x] События планов попадают в реестр `/admin/api/audit/events` — `AuditEventNames` (`internal/database/audit_log_admin.go:188`), `TestAuditRegistriesMatchConstants`.
- [x] UI блокирует правку имени защищённых планов, показывает причину недоступности удаления и не предлагает удалить используемый план — `admin/src/views/Plans.svelte:26,135,210,232`.

## 10. Файлы

- `internal/database/plans_admin.go` — репозиторий экрана (+ аудит-эмиссии);
- `internal/database/plans_admin_test.go`, `admin/plans_test.go` — тесты;
- `internal/database/audit_log.go` — константы и конструкторы событий планов;
- `internal/database/audit_log_admin.go` — регистрация событий в реестре;
- `admin/plans.go` — HTTP-обработчики; `admin/src/views/Plans.svelte` — экран.

## 11. Вне скоупа и известные ограничения

- `devices_limit` не ограничивается ядром: значение хранится и отображается,
  но ни одна подсистема его не читает (проверено поиском по `DevicesLimit` вне
  панели и репозитория). Подсказка в форме так и говорит — это честное
  описание, а не баг экрана;
- `traffic_limit` влияет только на уведомления о квоте (`traffic_limit > 0`);
  «жёсткого» обрыва по лимиту панель не настраивает;
- правка плана не запускает `ReconcilePlanNodes` и не переприменяет план к
  существующим подпискам — для этого есть отдельное действие на экране
  подписок (`04-subscriptions.md`);
- пагинации нет: планов на порядок меньше, чем подписок (ADR-0002, «Принятые
  ограничения»).
