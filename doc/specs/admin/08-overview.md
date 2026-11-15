# ТЗ админ-панели: экран «Состояние»

Первый экран после входа: отвечает на один вопрос — жива ли система и не встала
ли очередь синхронизации. Ничего не меняет, только читает агрегаты.

## 1. Цель и границы

Показать за один взгляд:

- какая сборка запущена (версия, коммит, время сборки, аптайм);
- где лежит база и сколько она занимает;
- сколько подписок по статусам;
- **сколько привязок подписка↔узел стоит в очереди и как давно** — это главный
  сигнал: `pending_remove`, висящий сутками, блокирует удаление подписки;
- сколько заказов и рассылок в каком статусе;
- когда последний раз кто-то запрашивал `/sub/:id` (признак, что субсервер
  вообще получает трафик).

Вне скоупа: графики, история метрик, кнопки управления воркерами, рестарт
сервисов. Метрики живут в Prometheus (`/metrics`), экран их не заменяет.

## 2. Данные

Агрегаты считаются по существующим колонкам, новых таблиц и счётчиков нет:

| Что | Источник |
|---|---|
| подписки по статусам | `subscriptions.status` |
| заказы по статусам | `orders.status` |
| рассылки по статусам | `broadcasts.status` |
| привязки по статусам | `subscription_nodes.status` (`active` / `pending_add` / `pending_update` / `pending_remove`) |
| возраст очереди | `MIN(subscription_nodes.updated_at)` среди не-`active` |
| последний запрос подписки | `MAX(subscriptions.last_request)` |
| события журнала за сутки | `audit_log.created_at >= now-24h` |
| ноды | `COUNT(nodes)` |
| путь и размер БД | `database.Service.FilePath()` + `os.Stat` |

Реализация — `internal/database/overview.go`, метод `GetOverviewSnapshot`.
`MIN(updated_at)` читается не агрегатом, а `ORDER BY updated_at LIMIT 1`:
драйвер SQLite отдаёт агрегат строкой, и в `*time.Time` он не сканируется.

## 3. Права и guard'ы

Только чтение. Эндпоинт под общим bearer-токеном, как и весь `/admin/api`.
Мутаций нет, поэтому нет и guard'ов по статусам.

## 4. API

| Method | Path | Ответ |
|---|---|---|
| GET | `/admin/api/overview` | агрегаты по блокам, см. ниже |

```json
{
  "build": {"version": "dev", "commit": "unknown", "build_time": "unknown", "uptime_seconds": 0},
  "database": {"path": "/app/data/rs8kvn.db", "size_bytes": 1048576},
  "subscriptions": {"total": 65, "by_status": {"active": 60, "expired": 5}},
  "nodes": {"total": 4},
  "bindings": {
    "by_status": {"active": 120, "pending_remove": 2},
    "pending_total": 2,
    "oldest_pending_at": "2026-11-05T17:00:00Z"
  },
  "orders": {"total": 12, "by_status": {"paid": 12}},
  "broadcasts": {"total": 1, "by_status": {"scheduled": 1}},
  "audit": {"events_24h": 40},
  "last_sub_request": "2026-11-05T18:00:00Z"
}
```

`by_status` содержит только встретившиеся статусы; пустых ключей нет.
`oldest_pending_at` и `last_sub_request` — `null`, когда очередь пуста или
запросов ещё не было (не «нулевое время», которое рендерилось бы как 1970).
`build` берётся из `-ldflags` через `cmd/bot/main.go` → `SetBuildInfo`, то есть
совпадает с тем, что вшито в бинарник.

## 7. UI

`admin/src/views/Overview.svelte`, маршрут `#/overview`, он же экран по
умолчанию при пустом хеше. Навигация — первой строкой в сайдбаре.

Макет — KPI-панель (с 2026-11-15), всё так же без библиотек компонентов:

1. Строка KPI-плиток (`.kpis`, 4 плитки):
   «Выручка · N дней» (крупно, с селектом периода в углу), «Активные подписки»,
   «Очередь узлов» (`warn`, когда `pending_total > 0`), «События аудита · 24 ч»
   (подпись — последний `/sub`; мелкая строка-подсказка про трафик субсервера).
2. При `pending_total > 0` — `.notice.nav-warn` под KPI-строкой: возраст самой
   старой привязки и подсказка, что фоновый воркер повторяет сам, а
   «Пересинхронизировать» на подписке гонит прогон немедленно.
3. Две колонки (`.columns`, левая шире — графики):
   «Выручка по дням» (гистограмма 130px + итог и «оплачено/ср. чек/отменено-
   истекло» под ней) и «Истекают за 30 дней» (гистограмма 70px, цвет `warn`).
4. Правая колонка — статусные карточки: «Подписки», «Заказы», «Рассылки»
   (всего + бейджи + кнопка), «Привязки» (бейджи по статусам),
   «Система» (версия с коммитом, собрано, аптайм, файл БД и размер — аптайм
   по-русски («3 ч 12 мин»), форматирование руками, без библиотек).

Состояния: «Загружаем…», `.notice.notice-error` с текстом ошибки, кнопка
«Обновить» в заголовке.

## 8. Тесты

| Кейс | Ожидание |
|---|---|
| `GetOverviewSnapshot` на данных с `pending_remove` | `pending_total=1`, `oldest_pending_at` равен замороженному `updated_at` |
| пустая база | `oldest_pending_at = null`, `last_sub_request = null` |
| `GET /admin/api/overview` без токена | `401 unauthorized` |
| ошибка стора | `500 internal` |
| контракт | набор ключей зафиксирован в `admin/contract_test.go` |

## 9. Критерии приёмки

- [x] Экран по умолчанию — «Состояние», не «Планы». — проверено: admin/src/App.svelte:26 (первый роут overview), :47 и :38 (fallback на overview для неизвестного хэша).
- [x] Очередь `pending_*` видна без захода в подписку и без логов. — проверено: admin/src/views/Overview.svelte:200-256 (KPI-плитка «Очередь узлов» + предупреждение под строкой KPI); данные из GET /admin/api/overview (admin/overview.go:81-85); TestGetOverviewSnapshot_Counts.
- [x] Возраст очереди считается от `updated_at`, а не от времени ответа. — проверено: internal/database/overview.go:143-176 (oldestPendingBinding читает строку с Order updated_at ASC).
- [x] Нулевые значения не превращаются в 1970 год. — проверено: overview.go:172-174,190-194 возвращают nil при отсутствии строк; JSON отдаёт null; formatDate(null)→'—' (admin/src/lib/format.js:20-22).
- [x] Аптайм и версия совпадают с `docker inspect` образа — проверено на уровне механизма: `Dockerfile:29-35` (ldflags) → `main.version/commit/buildTime` → `server.SetBuildInfo` (`cmd/bot/main.go:291`) → JSON `build.{version,commit,build_time}` (`admin/overview.go:33-77`, ключи закреплены `admin/contract_test.go:313`); сборка с этими ldflags печатает штамп по `./rs8kvn_bot --version` (прогнано 2026-11-05: `v2.8.0-test commit=deadbeef`). Совпадение с `docker inspect` (время старта контейнера) остаётся ручной проверкой на собранном образе, потому что образ в этой сессии не поднимался.

## 10. Файлы

Создаёт: `internal/database/overview.go` + `_test.go`, `admin/overview.go` +
`_test.go`, `admin/src/views/Overview.svelte`.

Правит: `routes`/меню в `App.svelte`, маршрут в `Handler()`, `SetBuildInfo` в
`cmd/bot/main.go`, поля `Server`.

Не трогает: `admin/dist/*` (собирается отдельно), состояние воркеров,
`internal/service/*`.

## 11. Вне скоупа

- история очереди и графики;
- управление воркерами (пауза, рестарт, изменение интервала);
- health нод в этом экране (остаётся кнопкой на экране «Ноды»);
- алерты.