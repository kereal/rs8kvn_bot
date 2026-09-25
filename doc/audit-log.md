# Журнал аудита (`audit_log`)

Универсальный append-only журнал событий системы: что произошло, с какой
сущностью, кто инициировал и какими были данные до/после. Миграция
**039_create_audit_log**, код — `internal/database/audit_log.go` +
`internal/database/audit_log_repo.go`.

---

## Зачем он нужен

До появления журнала ответ на вопрос «почему у пользователя пропал доступ?»
или «кто удалил подписку?» приходилось собирать из текстовых логов, которые
ротируются и не связаны с данными. Журнал решает три задачи:

1. **Расследования.** Вся история сущности — в одном SELECT: создание,
   оплаты, смены тарифа, истечения, ревокации, удаления. Типовая цепочка
   «пропал доступ»: `payment_confirmed → chargeback_received →
   downgraded_to_free → subscription_revoked → subscription_deleted` —
   с авторами и точным временем каждого шага.
2. **Подотчётность админ-действий.** `/del` и `/setplan` записывают
   telegram_id администратора; вопрос «кто это сделал» решается без
   серверных логов.
3. **Надёжность платежей.** События подтверждения и чарджбэка пишутся
   в той же транзакции, что и изменение заказа, — журнал не может
   разойтись с фактами в БД.

---

## Схема

```sql
CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL,   -- subscription | order | broadcast | plan | node | product | system
    entity_id   TEXT,            -- строковый ID сущности; NULL для системных событий
    event       TEXT NOT NULL,   -- 'payment_confirmed', 'plan_changed', ...
    actor_type  TEXT NOT NULL,   -- user | admin | worker | payment_provider | system
    actor_id    TEXT,            -- telegram_id админа/юзера, имя воркера, 'platega'
    old_state   TEXT,            -- JSON: изменившиеся поля ДО
    new_state   TEXT,            -- JSON: изменившиеся поля ПОСЛЕ
    details     TEXT NOT NULL DEFAULT '{}',  -- JSON: свободный контекст события
    created_at  DATETIME NOT NULL
);
```

Индексы: `(entity_type, entity_id, created_at DESC)` — история сущности,
`created_at DESC` — лента событий, `event` — выборка по типу события.

### Ключевые решения

- **Append-only.** Только INSERT и SELECT. Никто и никогда не обновляет и
  не удаляет записи — enforced только дисциплиной кода (запрещающих
  триггеров нет), поэтому при ревью PR-ов, трогающих эту таблицу, это
  надо проверять вручную.
- **Нет FK.** `entity_id` — просто строка. История подписки остаётся
  доступной после её физического удаления (`admin /del` — единственный
  путь физического удаления в системе).
- **`entity_id` — TEXT, а не INTEGER.** Журнал подходит и для сущностей
  с нечисловыми идентификаторами (например `subscription_id`-строка
  у trial-подписок).
- **Нет CHECK-констрейнтов на `event`/`entity_type`.** Осознанный отход
  от практики миграций 033/036: аудит — открытый список событий, CHECK
  означал бы миграцию на каждое новое событие. Целостность имён держит
  Go-реестр (ниже): опечатка невозможна, всё типизировано.
- **old_state/new_state — только изменившиеся поля**, а не вся строка:
  компактно и достаточно для расследований. NULL (а не `'{}'`) означает
  «полей не менялось»; `null` внутри JSON — «поле сброшено».
- **Денормализованный контекст в details.** Каждое событие подписки несёт
  `telegram_id`, `username`, `subscription_id` — событие самодостаточно
  даже после удаления строки подписки.

---

## Каталог событий

Константы и конструкторы — единый источник истины:
`internal/database/audit_log.go`. Таблица ниже — справочник.

### Подписки (entity_type = subscription)

| Событие | Кто | Когда |
|---|---|---|
| `subscription_created` | user | Создана привязанная free-подписка |
| `trial_created` | user | Выдана анонимная trial-подписка |
| `trial_bound` | user | Trial привязан к Telegram-аккаунту |
| `subscription_reanimated` | user | revoked-подписка оживлена при повторном /start |
| `subscription_renewed` | payment_provider | Оплата подтверждена, срок продлён |
| `plan_changed` | admin | `/setplan` (old/new: plan_id, expires_at, status) |
| `subscription_expired` | worker | Истёк срок (expire-воркер) |
| `downgraded_to_free` | worker | Понижение до free (причина в details: `expiry` / `chargeback`) |
| `subscription_revoked` | worker | Orphan-ревокия: нет живых node-биндингов |
| `subscription_deleted` | admin | Физическое удаление `/del` (снапшот строки в old_state) |
| `trial_deleted` | worker | Удаление истёкшей анонимной trial |

### Уведомления (entity_type = subscription)

| Событие | Когда |
|---|---|
| `reminder_sent` | Отправлено напоминание об истечении (окно в details: `3d`/`1d`/`3h`) |
| `traffic_notified` | Трафик-уведомление (kind: `ninety`/`exhausted`/`reset`) |

### Оплата (entity_type = order)

| Событие | Кто | Когда |
|---|---|---|
| `order_created` | user | Создан pending-заказ, выдана платёжная ссылка |
| `payment_confirmed` | payment_provider | Колбэк подтверждён, заказ и подписка обновлены (атомарно в tx) |
| `order_expired` | worker | Неоплаченный заказ истёк |
| `order_canceled` | payment_provider | Заказ отменён (user-cancel или провайдер) |
| `chargeback_received` | payment_provider | Чарджбэк; details.downgraded = была ли подписка понижена |
| `payment_creation_uncertain` | system | Неопределённый исход запроса к провайдеру, нужна ручная сверка |

### Рассылки (entity_type = broadcast)

`broadcast_started` / `broadcast_completed` / `broadcast_failed` /
`broadcast_canceled` (actor_id — telegram_id админа). Сводная статистика
(sent/blocked/unreachable/failed) — в details завершающих событий.

### Технические агрегаты (entity_type = system)

| Событие | Когда |
|---|---|
| `sync_run_degraded` | Прогон `SyncPendingNodes` завершился с частичными отказами (сводка за прогон) |
| `orphaned_clients_revoked` | Сводка orphan-ревокий за прогон |

Принцип: технические события — **только сводки за прогон**. Per-item
ретраи воркеров, touches при запросах `/sub` (devices/ips/last_request)
и пустые трафик-сканы в журнал **не пишутся** — иначе журнал распухнет
и потеряет читаемость.

---

## Как писать события

### Правила

1. **Константа + конструктор — в `database/audit_log.go`.** Не собирайте
   `AuditLog` вручную в сервисах. Конструктор фиксирует entity, actor и
   состав JSON в одном месте. Новое событие = одна константа + один
   конструктор; **миграция не нужна**.
2. **Best-effort по умолчанию.** Ошибка журнала не должна ломать
   бизнес-операцию — в сервисах используйте обёртку `logAudit`:

   ```go
   s.logAudit(ctx, database.AuditSubscriptionCreated(sub, inviteCode))
   ```

3. **Исключение — платежные транзакции.** В `ConfirmOrderPaidCAS` и
   `CancelPaidOrderAndDowngradeCAS` событие пишется через
   `InsertAuditLogInTx(tx, ...)` **в той же транзакции**: откат транзакции
   откатывает и событие. Журнал отражает только зафиксированные факты.
4. **actor_id админа — через context.** Bot-слой оборачивает контекст
   до вызова сервиса:

   ```go
   ctx = service.WithAuditAdmin(ctx, chatID) // в HandleDel / HandleSetPlan
   ```

   Сервис читает его в `AuditPlanChanged`/`AuditSubscriptionDeleted`.
5. **Снапшот «до» — до мутаций.** Для diff-событий снимайте состояние
   перед изменением полей:

   ```go
   before := database.SubscriptionAuditState(sub) // снимок ДО
   // ... мутации sub ...
   s.logAudit(ctx, database.AuditPlanChanged(sub, before, days, adminID))
   ```

### Добавление нового события (чек-лист)

1. Константа `AuditEventMyNewEvent = "my_new_event"` в `database/audit_log.go`
   (lower_snake_case, прошедшее время).
2. Конструктор `AuditMyNewEvent(...)`, использующий `NewAuditLog` или
   прямой литерал `AuditLog` (как существующие).
3. Вызов через `logAudit` (или `InsertAuditLogInTx`, если событие должно
   быть атомарно с tx).
4. Строка в таблице каталога этого документа.
5. Если фейку testutil нужна проверка — событие попадает в
   `mockDB.AuditLogs`, искать `AuditLogsByEvent(event)`.
6. Тест: один позитивный сценарий в стиле `internal/database/audit_log_test.go`.

---

## Как читать журнал

### Go API

```go
// История сущности (новые первыми).
events, err := db.GetAuditLogByEntity(ctx, database.EntitySubscription, "42", 100)

// Последние события заданного типа.
events, err := db.GetAuditLogByEvent(ctx, database.AuditEventChargebackReceived, 50)

// Общая лента.
events, err := db.GetRecentAuditLog(ctx, 100)
```

### SQL

История одной подписки:

```sql
SELECT created_at, event, actor_type, actor_id, old_state, new_state, details
FROM audit_log
WHERE entity_type = 'subscription' AND entity_id = '42'
ORDER BY created_at DESC;
```

Последние чарджбэки:

```sql
SELECT created_at, details
FROM audit_log
WHERE event = 'chargeback_received'
ORDER BY created_at DESC LIMIT 20;
```

Действия администраторов за сутки:

```sql
SELECT created_at, event, actor_id, entity_type, entity_id, details
FROM audit_log
WHERE actor_type = 'admin' AND created_at > datetime('now', '-1 day')
ORDER BY created_at DESC;
```

### Чтение JSON-полей

SQLite умеет извлекать значения прямо из JSON:

```sql
-- Все понижения после чарджбэка
SELECT created_at, entity_id, details
FROM audit_log
WHERE event = 'chargeback_received'
  AND json_extract(details, '$.downgraded') = 1;

-- Израсходованный лимит: старый и новый срок подписки
SELECT created_at,
       json_extract(old_state, '$.expires_at') AS was,
       json_extract(new_state, '$.expires_at') AS became
FROM audit_log
WHERE event = 'plan_changed'
ORDER BY created_at DESC;
```

---

## Инварианты (проверяются тестами)

- История сущности доступна после физического удаления её строки
  (`TestAuditLog_SurvivesSubscriptionDeletion`).
- Откат платежной транзакции не оставляет события в журнале
  (`TestConfirmOrderPaidCAS_RollsBackAuditWithTransaction`).
- nil-запись отклоняется репозиторием (`TestAuditLog_RepositoryRejectsNil`).
- JSON-контракт marshal/diff (`TestAuditLog_MarshalSemantics`):
  пустые map → NULL, `diffState` возвращает только изменившиеся поля.

Тесты: `internal/database/audit_log_test.go`, `internal/database/audit_payment_test.go`.

---

## Рост таблицы

Оценка при текущем составе событий: единицы записей на пользователя в
месяц (создание, оплата, 1–3 напоминания). Основной вклад дают
напоминания и трафик-уведомления, но они ограничены битмасками
(`reminders_sent`, `traffic_reminders_sent`) — каждое срабатывает не
более одного раза за цикл. Опасных «пишем на каждый запрос» источников
в журнале нет. Если объём всё же станет проблемой, порядок действий:

1. Считать строки по типам: `SELECT event, COUNT(*) FROM audit_log GROUP BY event;`
2. Самые объёмные типы вынести в ротацию (например архивировать события
   старше N месяцев в отдельную таблицу/файл) — SELECT-путь журнала это
   позволяет, так как старые события почти никогда не нужны горячо.
3. Не добавлять per-item технические события — это единственный реальный
   риск переполнения.
