# 10 — Очередь синхронизации (только чтение)

Эталон структуры: [01-products.md](01-products.md). Общие правила:
[00-common.md](00-common.md), контракт: [ADR-0002](../../adr/0002-admin-panel-contract.md).

## 1. Цель и границы

Показать конкретные пары подписка↔нода, которые фоновый воркер ещё не
применил: `pending_add`, `pending_update`, `pending_remove`. «Состояние» даёт
только количество и возраст самой старой записи — этого мало, чтобы понять,
какая подписка и какая нода застряли и с какой ошибкой.

Вне скоупа: автоматический опрос нод, пауза/перезапуск воркера, изменение
интервала ретраев, прямая правка `subscription_nodes`. Ручной прогон — это
существующий `POST /admin/api/subscriptions/{id}/sync` (ТЗ 04), очередь только
ссылается на него.

## 2. Данные

Одна строка — одна запись `subscription_nodes` со статусом `pending_*`:

| Поле ответа | Источник |
|---|---|
| `subscription_id` | `subscription_nodes.subscription_id` (внутренний id) |
| `telegram_id` | `subscriptions.telegram_id` (JOIN) |
| `subscription_id_public` | `subscriptions.subscription_id` (публичный id подписки) |
| `node_id` | `subscription_nodes.node_id` |
| `node_name` | `nodes.name` (LEFT JOIN: нода может быть удалена) |
| `status` | `subscription_nodes.status` |
| `retry_count` | `subscription_nodes.retry_count` |
| `retry_at` | `subscription_nodes.retry_at` |
| `last_error` | `subscription_nodes.last_error`, обрезан до 300 символов |
| `updated_at` | `subscription_nodes.updated_at` — возраст записи |

`active` в очередь не входит: это уже применённая привязка.

## 3. Права и guard'ы

| Операция | Условие отказа | Код |
|---|---|---|
| list | `status` вне `pending_add\|pending_update\|pending_remove` | `400 bad_request` |
| list | `sort` вне белого списка (`updated_at`, `retry_at`, `subscription_id`) | `400 bad_request` |
| list | `order` не `asc`/`desc`, `limit` не положителен, `offset` < 0 | `400 bad_request` |
| list | `limit` > 200 | clamp до 200 |

Мутаций нет. `last_error` — свободный текст внешнего API: он обрезается по
длине и не содержит токенов нод (панель их не пишет в это поле).

## 4. API

| Method | Path | Query | Ответ | Ошибки |
|---|---|---|---|---|
| GET | `/admin/api/sync-queue` | `?status=&limit=&offset=&sort=&order=` | `{"items":[…],"total":N}` | `400`, `401`, `500` |

`total` и `items` строятся одним `WHERE` (`buildSyncQueueQuery` вызывается
дважды — для COUNT и SELECT). По умолчанию строки идут по возрасту: самые
старые первыми (`updated_at ASC`), потому что застрявшая синхронизация — это
старая запись.

Строка (поле-в-поле):

```json
{
  "subscription_id": 42,
  "telegram_id": 123456789,
  "subscription_id_public": "a1b2c3",
  "node_id": 3,
  "node_name": "de-fra-1",
  "status": "pending_add",
  "retry_count": 3,
  "retry_at": "2026-11-28T12:00:00Z",
  "last_error": "node unreachable",
  "updated_at": "2026-11-26T08:00:00Z"
}
```

Пустой `last_error` нормализуется в `null`.

## 5. Сервер

`internal/database/sync_queue_admin.go`: `SyncQueueFilter`, `SyncQueueRow`,
`ListSyncQueue` (count + select). `admin/sync_queue.go`: `SyncQueueStore`,
`handleListSyncQueue`, DTO. Маршрут регистрируется в `admin/server.go`.

## 6. Аудит

**Не пишется.** Экран read-only. Ручной прогон синхронизации пишет события
самого сервиса синхронизации.

## 7. UI

`admin/src/views/SyncQueue.svelte`, маршрут `#/sync-queue`, пункт сайдбара
«Очередь синхронизации». Таблица: подписка (ссылка на карточку), пользователь,
нода, статус-бейдж, попытки, повтор, возраст («3 дня назад»), ошибка (обрезана
с `title`), кнопка «Пересинхронизировать» — она вызывает
`POST /admin/api/subscriptions/{id}/sync` и после успеха перезагружает список.
Фильтр по статусу, пагинация 50/100/200, «Обновлено в HH:MM».

На «Состоянии» очередь доступна из KPI-плитки «Очередь узлов» и из карточки
«Привязки»; при непустой очереди — предупреждение с возрастом старейшей записи
и кнопкой «Открыть очередь».

## 8. Тесты

| Кейс | Ожидание |
|---|---|
| очередь по умолчанию | только `pending_*`, старые сверху, `total` = 2 |
| фильтр статуса | сужает и `items`, и `total` (единый WHERE) |
| пагинация | страницы не пересекаются, `total` неизменен |
| поля строки | JOIN-имена, публичный id, retry, обрезанная ошибка |
| `status=active` | `400` |
| без токена | `401` |

## 9. Критерии приёмки

- [x] Очередь видна на «Состоянии» и отдельным списком.
- [x] Видно подписку, ноду, число попыток и ошибку.
- [x] Строка ведёт на существующий `POST /admin/api/subscriptions/{id}/sync`.
- [x] HTTP-тесты на фильтры и пагинацию.
- [x] Ни одного `UPDATE subscription_nodes` в панели.

## 10. Файлы

Создаёт: `internal/database/sync_queue_admin.go` + `_test.go`,
`admin/sync_queue.go` + `_test.go`, `admin/src/views/SyncQueue.svelte`.

Правит: `admin/server.go` (маршрут и поле), `admin/src/App.svelte` (роут и
меню), `admin/src/views/Overview.svelte` (ссылки), `admin/src/lib/types.js`,
`admin/contract_test.go` (реестр маршрутов).

Не трогает: `internal/service/sync*.go`, `subscription_nodes` (запись),
`admin/dist/*` (собирается отдельно).
