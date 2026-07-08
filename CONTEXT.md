# CONTEXT.md — Глоссарий домена rs8kvn_bot

> Источник истины для имён предметной области. Архитектурные обзоры (`/improve-codebase-architecture`,
> `/codebase-design`) и петля grilling обязаны называть модули, швы и адаптеры в терминах этого файла,
> а не по именам файлов/функций. Если углубляемый модуль назван понятием, которого здесь нет — добавь
> термин; если формулировка размыта — уточни её прямо здесь.
>
> ADR: в репозитории пока нет `docs/adr/`. Решения, которые не стоит переоткрывать будущим обзорам,
> фиксируются там (см. `/grilling`).

## Продукт

Telegram-бот для раздачи VPN-подписок (VLESS+Reality+Vision) через панели 3x-ui и proxman.
Пользователь получает ссылку подписки `/sub/{subID}`, агрегирующую конфиги с нескольких нод.

## Сущности домена

- **Subscription** — подписка пользователя: связь Telegram-ID с клиентом на нодах, лимитами и сроком.
  Состояния: `active` / `revoked` / `expired`. Поля `Devices` и `Ips` — JSON-массивы для аналитики клиентов.
- **Plan** — тарифный план (Free, Trial, платные). Определяет лимиты трафика/устройств и срок.
- **Product** — платный продукт, привязанный к Plan (имя, цена, `DurationDays`).
- **Order** — событие покупки с трекингом оплаты. Состояния: `pending` / `paid` / `expired` / `canceled`.
- **Node** — VPN-сервер-источник конфигов для `/sub/{subID}`. Типы (`NodeType`): `3x-ui`, `proxman`, `fetch`.
  Поле `inbound_ids` (JSON) — мульти-inbound на одной ноде.
- **SubscriptionNode** — связь «подписка ↔ нода» плюс 4-ступенчатый автомат синхронизации
  (`pending_add` / `pending_remove` / `active` / `error`), с retry-счётчиком. Единица депровизионирования.
- **Trial** — пробная подписка через план Trial + `trial_requests` (rate-limit по IP). Жизненный цикл:
  создание заявки → привязка к Telegram-ID (`BindTrialSubscription`, race-safe по `telegram_id=0`).
- **Invite / Referral** — реферальная система: один код на реферера (`UNIQUE`-ограничение),
  подсчёт рефералов в `ReferralCache`, инвалидация кэша при привязке.

## Потоки (flows)

- **Subscription intake** — создание подписки: резолв Plan по имени (без хардкода ID) → провизия VPN
  асинхронно через Sync → отдача ссылки. Двухфазное удаление: `revoked` → депровизионировать → физически удалить.
- **Subscription aggregation** — `subserver` собирает конфиги с активных нод параллельно (bounded concurrency),
  детектит формат (JSON/Clash/Base64/Plain), агрегирует трафик и `subscription-userinfo`, кэширует по `subID`.
- **SyncMachine** — фоновая сверка состояния подписок на нодах: `Reconcile` (структурная фаза, синхронно-обязательна),
  `SyncPendingNodes` / `SyncSubscription` (best-effort, retry с эксп. откатом), `ReconcileOrphanedClients`.
- **Order lifecycle** — `Create` → `Activate` (привязка/продление подписки) → `Expire`.

## Инфраструктурные понятия (тоже часть домена)

- **VPN Client** (`internal/vpn`) — абстракция провизии на ноде; адаптеры: `ThreeXUIClient`, `ProxmanClient`,
  `FetchClient` (read-only). Фабрика `NewClient` по `NodeType`. Классификация ошибок (already-exists / not-found)
  сейчас завязана на текст панели 3x-ui — кандидат на вынос за шов (см. архитектурный обзор, Кандидат 4).
- **XUIClient** (`internal/xui`) — REST-клиент панели 3x-ui: Bearer-auth, retry с jitter, circuit breaker
  (определён, но **не подключён** к пути клиента — resilience только на `RetryWithBackoff`).
- **SubserverCache / SubscriptionCache / ReferralCache** — слои кэша (TTL/LRU). Инвалидируются при
  создании/удалении/привязке подписки и смене конфига.

## Открытые вопросы (кандидаты на углубление)

- Узкий шов `DatabaseService` вместо божественного интерфейса (обзор, Кандидат 1).
- Единый владелец двухфазного жизненного цикла удаления (обзор, Кандидат 2).
- Вынос представления трафика из `SubscriptionService` (обзор, Кандидат 3).
