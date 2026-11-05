# Рассылки (broadcast)

Рассылки живут в админ-панели: создание, фильтры аудитории, расписание и
история — экран «Рассылки» (`admin/src/views/Broadcasts.svelte`,
`BroadcastDetail.svelte`). Ботовский wizard `/broadcast` удалён вместе с
половиной файлов `internal/bot/broadcast_*.go` — спецификация экрана и таблица
передачи ответственности: [specs/admin/06-broadcasts.md](specs/admin/06-broadcasts.md).

В боте остались только доставка и её данные:

| Компонент | Файл |
|---|---|
| Воркер доставки | `internal/bot/broadcast_worker.go` |
| Числовые константы (интервалы, ретраи, батчи) | `internal/bot/broadcast_constants.go` |
| Классификация ошибок Telegram | `internal/bot/broadcast_errors.go` |
| Хранение и состояние | `internal/database/broadcasts.go`, `broadcasts_admin.go`, `broadcast_recipients.go` |
| HTTP-слой панели | `admin/broadcasts.go` |

## Жизненный цикл

```
scheduled ──claim──> running ──┬──> completed   (все получатели исчерпаны)
   ▲                           ├──> failed      (infrastructure/persistence)
   │                           └──> canceled    (админ, POST .../cancel)
   └── retry_at (exponential backoff 5 с → 15 мин)
```

- кампания создаётся в статусе `scheduled`; `planned_at` пустой = «отправить при
  ближайшем тике», иначе — не раньше указанного времени;
- воркер выбирает кампании `status IN (scheduled, running) AND (retry_at IS NULL
  OR retry_at <= now) AND (planned_at IS NULL OR planned_at <= now)`, затем
  атомарно переводит `scheduled → running` (`ClaimBroadcast`, `started_at`), —
  поэтому рестарт бота продолжает работу, а не запускает её заново;
- `running` возвращается в выборку, пока в состоянии получателей остаются
  незавершённые.

## Состояние получателей

Снапшот аудитории фиксируется один раз при старте кампании и живёт JSON-состоянием
внутри строки `broadcasts` (полей `recipients_total` + JSON получателей), а не в
отдельной таблице:

- `recipients_total` — размер замороженного снапшота; пишется в той же транзакции,
  что и снапшот, поэтому панель показывает реальный размер аудитории всю кампанию,
  а не 0 до её конца;
- на получателя: статус, счётчик попыток, индекс следующей части текста,
  текст последней ошибки (обрезается до 500 символов);
- статусы: `pending` → `sending` → `sent` | `blocked` (заблокировал бота) |
  `unreachable` | `failed`;
- `sending` — аренда: если воркер упал, `RecoverStaleBroadcastRecipients`
  возвращает в `pending` записи, не обновлявшиеся дольше 2 минут;
- клейм батча сериализуется транзакцией SQLite (один писатель), поэтому
  параллельные тики не могут отправить одно сообщение дважды.

## Доставка

| Параметр | Значение |
|---|---|
| Тик воркера | 15 с (`broadcastWorkerInterval`) |
| Батч получателей | 100 (`broadcastBatchSize`) |
| Параллельность отправок | 10 (`broadcastConcurrency`) |
| Таймаут кампании | 5 мин (`broadcastTimeout`) |
| Попытки на сообщение | 1 + 2 повтора (`broadcastRetries`), пауза linear `300 мс × (попытка+1)` |
| Flood 429 | до 5 ожиданий `retry_after` (`broadcastFloodMaxWaits`), потолок 90 с, без `retry_after` — 5 с |
| Разбиение текста | по 4096 символов (`config.MaxTelegramMessageLen`), MarkdownV2 экранируется |
| Ретрай кампании | exponential backoff 5 с → 15 мин |

Длинный текст отправляется частями, и прогресс по частям сохраняется на диск:
после падения бот продолжает с той части, на которой остановился, а не начинает
получателя заново.

Итоги и переходы статусов пишутся в аудит: `broadcast_created`, `broadcast_updated`,
`broadcast_retried`, `broadcast_deleted` (действия панели) и `broadcast_started`
(с реальным размером аудитории), `broadcast_completed`, `broadcast_failed`,
`broadcast_canceled` (жизненный цикл).

## Деактивация заблокировавших

После завершения кампании админ может погасить доступ тем, кто заблокировал бота
(`blocked`) или недоступен (`unreachable`):

`POST /admin/api/broadcasts/{id}/deactivate-blocked` →
`SubscriptionService.DeactivateBlockedBroadcastRecipients` (кнопка «Отключить
заблокировавших» в карточке рассылки).

- Подписка переводится в `revoked` — сублинк отдаёт 404, пользователь выпадает
  из будущих выборок (по умолчанию фильтр `subscription_status = 'active'`).
  Строка в БД и история сохраняются, физического удаления нет.
- Сразу после отзыва — депровижининг тем же путём, что и `/del`: привязки
  `subscription_nodes` → `pending_remove`, затем `SyncSubscription` снимает
  клиента с панели. Без этого уже импортированный профиль продолжал бы работать
  при 404 на ссылке.
- БД-фаза выполняется для всех получателей, обращения к панелям в одном прогоне
  ограничены `maxImmediateSyncs = 25` (иначе запрос админа висел бы на медленной
  панели пропорционально числу заблокировавших); остаток снимает фоновый
  `SyncPendingNodes`.
- Частичные сбои не маскируются: метод возвращает
  `DeactivationResult{Targets, Revoked, Failed}` и агрегированную ошибку
  (`errors.Join`), экран показывает «выключено N из M, ошибок K».
- `subscription_revoked` пишется сразу после смены статуса и до внешних вызовов:
  отзыв — состоявшийся факт, даже если депровизионинг упадёт.
- Если заблокировавший вернётся и напишет боту, подписка оживает
  (`reanimateRevokedSubscription`) с записью в аудит (`subscription_reanimated`).

## Фильтры аудитории

`broadcasts.filters` — JSON (`database.BroadcastFilter`, `ParseBroadcastFilter`):
`plan_type` (все/paid/free), `subscription_status` (`active` по умолчанию /
`revoked` / `all`), `registered_after` / `registered_before`, `inactive_days`
(0 = никогда не обращались, N>0 = неактивны N дней), `ever_paid`. Счётчик
аудитории до создания кампании отдаёт `GET /admin/api/broadcasts/audience`, а
подписи выбранных фильтров собирает `BroadcastFilter.String`.
