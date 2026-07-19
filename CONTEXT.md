# CONTEXT.md — Глоссарий домена rs8kvn_bot

> Источник истины для имён предметной области. Архитектурные обзоры (`/improve-codebase-architecture`,
> `/codebase-design`) и петля grilling обязаны называть модули, швы и адаптеры в терминах этого файла,
> а не по именам файлов/функций. Если углубляемый модуль назван понятием, которого здесь нет — добавь
> термин; если формулировка размыта — уточни её прямо здесь.
>
> ADR: решения, которые не стоит переоткрывать будущим обзорам, фиксируются в `docs/adr/`
> (см. `0001-narrow-database-service-seam.md`). Новые кандидаты отмечай в разделе «Открытые вопросы».

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
  (`pending_add` / `pending_remove` / `pending_update` / `active`), с retry-счётчиком. Единица депровизионирования.
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

- **VPN Client** (`internal/vpn`) — доменная абстракция провизии VPN на ноде. Пер-нодные адаптеры
  (3x-ui, proxman, read-only fetch) предоставляют создание/обновление/удаление и синхронизацию жизненного цикла
  клиентов на ноде. Классификация ошибок (already-exists / not-found) живёт на шве, вне адаптера.
  Чтение трафика клиента пока не является первоклассной возможностью этой абстракции (см. открытый Кандидат 2).
- **XUIClient** (`internal/xui`) — доменный адаптер к панели 3x-ui: аутентификация, повторы с jitter и
  circuit breaker для устойчивости к сбоям панели (подключение breaker — см. открытый Кандидат 1).
- **SubserverCache / SubscriptionCache / ReferralCache** — слои кэша (TTL/LRU). Инвалидируются при
  создании/удалении/привязке подписки и смене конфига.

## Открытые вопросы (кандидаты на углубление)

### Реализовано (обзор 2026-07-08/09, ветка `dev`)
- Кандидат 1 — узкий шов `DatabaseService` → пер-срезовые интерфейсы (`subserver`/`web`/`scheduler`), ADR-0001.
- Кандидат 2 — единый владелец двухфазного удаления (`revokeAndDeprovisionThenDelete`).
- Кандидат 3 — вынос представления трафика в `internal/service/subscription_traffic.go`.
- Кандидат 4 — классификация ошибок VPN за швом `Client` (3x-ui + proxman).

### Открыто (обзор 2026-07-09)
- Кандидат 1 — подключить или удалить осиротевший circuit breaker XUI (`internal/xui/breaker.go`, 153 строки, 0 вызывающих в prod).
- Кандидат 2 — объединить trial/reconcile/traffic через шов `vpn.Client`, убрать параллельную карту `xuiClients` (латентный баг: proxman trial-ноды молча падают).
- Кандидат 3 — общий HTTP-транспорт для `xui`/`proxman` адаптеров (`httpx`: Bearer + лимит размера + retry); у proxman сейчас нет ни того, ни другого.
- Кандидат 4 — один `buildProvision(sub, plan)` в SyncService (дублируется в `processPendingAdd`/`processPendingUpdate`).
- Кандидат 5 — `reconcileAndSyncNodes` для fan-out при смене плана (дублируется в `ActivateProduct`/`Renew`/`Expire`).
- Кандидат 6 — `subserver.Service` как pass-through над `Cache` (углубить или убрать).
- Кандидат 7 — timeout пролог применять один раз на диспетчеризации, а не копировать в каждый handler.
