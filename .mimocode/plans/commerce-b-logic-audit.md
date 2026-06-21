# Аудит реализации бизнес-логики commerce-b-logic.md

Дата аудита: 2026-06-21  
Проект: `rs8kvn_bot`  
Спецификация: `.mimocode/plans/commerce-b-logic.md`  
Ограничение: тесты не проверялись, как было разрешено.

## 1. Краткий вывод

Большая часть ядра коммерческой логики уже реализована. В проекте есть
Subscription, Product, Order, `subscription_nodes`, синхронизация, retry и
идемпотентность добавления/удаления клиентов.

Критические незавершенные места:

1. `proxman` не исключен из назначения и runtime-инициализации.
2. Платный payment flow не завершен: `requestPayment` сейчас stub с ошибкой.
3. Срок для смены плана и Free→Premium считается неверно.
4. В модели `Plan` отсутствует `is_active`.
5. Административные операции из раздела 14 не реализованы полностью.

## 2. Покрытие бизнес-процессов

### Реализовано

- Subscription создается всегда через `SubscriptionService.Create` или
  `GetOrCreateSubscription`.
- Один пользователь имеет одну Subscription по `telegram_id`.
- Free plan назначается при первом запуске.
- `client_id` и `subscription_id` генерируются один раз и не меняются.
- Product хранит `plan_id`, `duration_days` и `is_active`.
- Order хранит статусы `pending`, `paid`, `expired`, `canceled` и поля
  внешнего провайдера.
- `subscription_nodes` хранит состояние синхронизации: `active`,
  `pending_add`, `pending_remove`, `retry_count`, `retry_at`, `last_error`.
- Sync diff учитывает повторное появление ноды в target: `pending_remove`
  возвращается в `pending_add`.
- `already exists` при добавлении считается успехом.
- `not found` при удалении считается успехом.
- Multi-inbound реализован через `inbound_ids`.

### Реализовано частично

- `username` может меняться в модели, но повторная регистрация не обновляет
  существующий `username`.
- Цена 0 активирует продукт напрямую, но платная цена зависит от внешнего
  callback, которого нет.
- Продление того же плана считает срок правильно, но лишний раз пересчитывает
  ноды.
- Админка имеет базовые команды `/del`, `/broadcast`, `/send` и статистику,
  но нет полноценного CRUD планов, продуктов и нод.

### Не реализовано или реализовано неверно

- `Plan.is_active` отсутствует в модели и миграции.
- `proxman` не отфильтрован из назначения нод и runtime-набора.
- Смена плана должна сбрасывать срок в `now + duration_days`, но код сохраняет
  остаток старого срока.
- Callback внешнего payment provider не реализован.
- Административные ручные операции из спецификации не реализованы.

## 3. Детальный аудит

### 3.1. Регистрация

Реализация:

- `SubscriptionService.Create` резолвит Free plan, генерирует `client_id` и
  `subscription_id`, создает Subscription и вызывает `ensureSubscriptionNodes`:
  `internal/service/subscription.go:95-133`.
- `GetOrCreateSubscription` ищет подписку по `telegram_id` и создает Free,
  если ее нет: `internal/service/subscription.go:665-710`.
- `ensureSubscriptionNodes` создает `pending_add` для нод плана:
  `internal/service/subscription.go:714-765`.

Риск:

- `GetNodesByPlanID` не фильтрует `nodes.type='3x-ui'`:
  `internal/database/nodes.go:141-153`.
- Из-за этого proxman может попасть в `subscription_nodes`.

### 3.2. Повторная регистрация

Реализация:

- Дубликат Subscription не создается.
- Существующие `client_id` и `subscription_id` не меняются.

Расхождение:

- Существующий `username` не обновляется, хотя спецификация допускает изменение
  username.

### 3.3. Покупка продукта и цена 0

Реализация:

- `OrderService.ActivateProduct` создает Order со статусом `pending`.
- Если `product.PriceCents == 0`, Order сразу отмечается активированным и
  `paid`, Subscription обновляется, ноды пересчитываются и синхронизируются:
  `internal/service/order.go:35-105`.
- Если `product.PriceCents > 0`, вызывается `requestPayment`, который сейчас
  всегда возвращает ошибку: `internal/service/order.go:102-105`.

Соответствие уточнению:

- Цена 0 действительно пропускает оплату.

Расхождения:

- Платный провайдер не интегрирован.
- Callback/финальная активация по Order не найдена.
- `UpdateOrderStatus` не проверяет `RowsAffected`:
  `internal/database/orders.go:46-53`.

### 3.4. Продление и смена плана

Реализация:

- `RenewSubscription` ставит `PlanID=product.PlanID`, считает срок от
  `max(now, current expires_at) + duration`, создает Order `paid`, обновляет
  Subscription и вызывает `RecalculateNodes`:
  `internal/service/subscription.go:767-826`.

Соответствие:

- Продление активного Premium считается от текущего `expires_at`.
- Продление после истечения считается от `now`.

Расхождения:

- Смена плана должна использовать `now + duration_days`.
- Free→Premium тоже должен получать срок от `now`, а не от остатка старого
  срока.
- Продление того же плана избыточно вызывает `RecalculateNodes`.

### 3.5. Истечение

Реализация:

- `GetExpiredPaidSubscriptions` выбирает активные платные подписки с
  `expires_at <= now`: `internal/database/subscriptions.go:294-306`.
- `ExpireSubscription` переводит подписку на Free:
  `internal/database/subscriptions.go:277-291`.
- `SubscriptionExpireWorker` запускается раз в час:
  `internal/scheduler/subscription_expire_worker.go:45-71`.
- После перевода на Free вызываются `RecalculateNodes` и `SyncSubscription`:
  `internal/service/subscription.go:828-854`.

Риск:

- До ближайшего hourly run подписка может оставаться на платном плане после
  формального истечения.

### 3.6. Формирование состава нод

Реализация:

- `RecalculateNodes` сравнивает target-ноды плана и текущие
  `subscription_nodes`: `internal/service/sync.go:58-136`.
- Active/pending_add для target остаются.
- Новые target получают `pending_add`.
- Active вне target получает `pending_remove`.
- Stale `pending_add` вне target удаляется.

Риск:

- Target-ноды берутся из `GetNodesByPlanID` без фильтра `type='3x-ui'`.

### 3.7. Синхронизация, retry и идемпотентность

Реализация:

- `SyncSubscription` берет pending-ноды подписки и вызывает `syncNodes`:
  `internal/service/sync.go:157-175`.
- `processPendingAdd` создает клиента на VPN-ноде:
  `internal/service/sync.go:206-249`.
- `processPendingRemove` удаляет клиента:
  `internal/service/sync.go:251-283`.
- Ошибки увеличивают `retry_count`, ставят `retry_at` и `last_error`:
  `internal/service/sync.go:285-318`.
- Worker выбирает pending с `retry_at IS NULL OR retry_at <= now` каждые
  5 минут: `internal/scheduler/subscription_sync_worker.go:23-49`.

Соответствие:

- Источник истины — БД.
- Ошибки синхронизации не ломают бизнес-процесс.
- Идемпотентность добавления и удаления реализована.

### 3.8. Proxman

Реализация:

- Типы нод есть: `3x-ui` и `proxman`: `internal/database/models.go:62-67`.
- `vpn.NewClient` поддерживает только `3x-ui`; proxman возвращает ошибку:
  `internal/vpn/client.go:38-50`.
- `buildRuntimeNodeClients` включает все active-ноды без фильтра типа:
  `cmd/bot/main.go:73-115`.

Расхождение:

- Спецификация требует исключить proxman из назначения и не создавать для него
  `subscription_nodes`.

Места без фильтра типа:

- `GetNodesByPlanName`: `internal/database/nodes.go:22-35`.
- `GetNodesByPlanID`: `internal/database/nodes.go:141-153`.
- `ListEnabled`: `internal/database/nodes.go:130-138`.
- `GetWithPlanAndNodes`: `internal/database/subscriptions.go:220-249`.
- `ensureSubscriptionNodes`: `internal/service/subscription.go:714-765`.
- `buildRuntimeNodeClients`: `cmd/bot/main.go:73-115`.

Последствие:

- proxman может попасть в `subscription_nodes`.
- proxman может попасть в выдачу доступа.
- активная proxman-нода может сломать старт приложения.

### 3.9. Multi-inbound

Реализация:

- `Node.InboundIDs` хранится как JSON-текст: `internal/database/models.go:74-78`.
- `ParseInboundIDs`, `SetInboundIDs`, `ResolveInboundIDs` реализованы:
  `internal/database/models.go:314-351`.
- Runtime-клиенты получают `node.ResolveInboundIDs()`:
  `cmd/bot/main.go:102-108`.

Соответствие:

- Требование нескольких `inbound_ids` реализовано.

### 3.10. Административные операции

Реализация:

- Есть базовые админские команды: `/del`, `/broadcast`, `/send`, последние
  регистрации и статистика.
- `/del` удаляет Subscription и клиента из панели:
  `internal/bot/admin.go:84-167`.

Расхождение:

- Нет полноценных операций для:
  - изменения плана пользователя;
  - изменения `expires_at`;
  - изменения состава плана с пересчетом подписок;
  - отключения Node без удаления клиентов;
  - CRUD планов, продуктов и нод.

## 4. Приоритетные рекомендации

### P0 — критично

1. Изолировать proxman от назначения и runtime.
2. Завершить внешний payment flow.
3. Исправить расчет срока для смены плана и Free→Premium.
4. Добавить `plans.is_active`.

### P1 — важно для надежности

1. Обновлять `username` в `GetOrCreateSubscription`.
2. Обновлять `updated_at` в `UpdateSubscription`.
3. Проверять `RowsAffected` в `UpdateOrderStatus`.
4. Не вызывать `RecalculateNodes` при продлении того же плана.
5. Явно документировать SLA expire worker.

### P2 — администрирование и наблюдаемость

1. Реализовать админские операции из раздела 14.
2. Добавить метрики pending-операций, ошибок sync, payment failures и
   просроченных подписок.
3. Добавить cleanup/migration для удаления proxman из `subscription_nodes`,
   если такие записи уже появились.

## 5. Техническое задание на доработку

### Задача 1. Полностью исключить proxman

Файлы:

- `internal/database/nodes.go`
- `internal/database/subscriptions.go`
- `internal/service/subscription.go`
- `cmd/bot/main.go`

Требования:

- В запросы нод плана добавить `nodes.type = '3x-ui'`.
- Runtime-клиенты создавать только для 3x-ui.
- `subscription_nodes` не должен содержать proxman.
- Для существующих proxman-записей нужен cleanup или миграция.

Критерии приемки:

- Старт приложения не падает при active proxman.
- Новая регистрация не создает `pending_add` для proxman.
- `GetWithPlanAndNodes` не возвращает proxman.
- `RecalculateNodes` не возвращает proxman в target.

### Задача 2. Завершить payment flow

Файлы:

- `internal/service/order.go`
- `internal/database/orders.go`
- внешний payment adapter/interface
- webhook/http handler для callback

Требования:

- Для `PriceCents > 0` Order остается `pending` до подтверждения провайдера.
- Payment provider data сохраняется в Order.
- Callback проверяет подпись/безопасность провайдера.
- Callback атомарно переводит Order в `paid`, обновляет Subscription и
  запускает синхронизацию.
- Повторный callback идемпотентен.

Критерии приемки:

- Цена 0 активируется сразу.
- Цена >0 не активирует подписку до callback.
- Успешный callback создает `paid` Order и активирует продукт.
- Повторная отправка callback не ломает подписку.

### Задача 3. Исправить продление и смену плана

Файлы:

- `internal/service/subscription.go`
- `internal/service/order.go`

Требования:

- Разделить сценарии:
  - продление того же плана;
  - покупка после истечения;
  - смена плана;
  - Free→Premium.
- Для смены плана использовать `now + duration_days`.
- Для смены плана обязательно пересчитывать ноды.
- Для продления того же плана можно пропускать `RecalculateNodes`.

Критерии приемки:

- Premium→Premium с активным сроком продлевается от `expires_at`.
- Premium→Premium после истечения продлевается от `now`.
- Free→Premium получает срок `now + duration_days`.
- Premium→VIP получает срок `now + duration_days` и новый набор нод.

### Задача 4. Добавить `plans.is_active`

Файлы:

- `internal/database/models.go`
- новая migration
- запросы выбора планов

Требования:

- Добавить поле `is_active` в модель Plan.
- Добавить миграцию.
- Использовать флаг в админских и продуктовых выборках.
- Free plan должен оставаться доступным.

Критерии приемки:

- Неактивный план не выдается новым продуктам/покупкам.
- Существующие подписки не ломаются.
- Миграция обратимо применима.

### Задача 5. Реализовать административные операции

Файлы:

- `internal/bot/admin.go`
- service/database layer для планов, продуктов, нод и подписок

Требования:

- Изменение плана пользователя должно использовать стандартный пересчет нод.
- Изменение `expires_at` должно сохранять целостность подписки.
- Изменение состава плана должно пересчитывать ноды для всех подписок плана.
- Отключение Node не должно удалять существующие подключения.

Критерии приемки:

- Админ может изменить план пользователя.
- Админ может изменить срок.
- Админ может отключить Node без удаления клиентов.
- После изменения состава плана формируются корректные pending-операции.

## 6. Итоговая оценка

Ядро бизнес-логики реализовано достаточно полно. Основная незавершенность
сосредоточена в трех критичных областях: proxman не исключен, платный payment
flow не завершен, а смена плана/Free→Premium считает срок неверно. После
исправления этих пунктов проект будет заметно ближе к спецификации
`.mimocode/plans/commerce-b-logic.md`.
