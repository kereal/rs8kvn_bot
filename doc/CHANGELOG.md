# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [2.4.0] - 2026-06-03 — feature/sources-table

### Added
- **Plan/Source data model**: новые таблицы `plans`, `sources`, `plan_sources` (M:N). `subscriptions.plan_id` foreign key заменяет `is_trial` boolean. Триал-источники резолвятся динамически через `sources JOIN plan_sources JOIN plans WHERE plans.name='trial'`
- `db.GetSourcesByPlanName(ctx, "trial"|"free")` — единый API для получения источников по плану
- `interfaces.DatabaseService.GetSourcesByPlanName`, `GetPlanByName`, `GetPlanByID`, `IsSourcesEmpty`, `ListSources`
- `service.GetInviteByCode`, `service.GetOrCreateInvite`, `service.PlanTrafficLimitGB` (читает traffic limit из plan, не из конфига)
- `xui.ErrClientNotFound` sentinel — `errors.Is(err, xui.ErrClientNotFound)` для reconcile
- `service.ReconcileOrphanedClients` — фоновая очистка сирот (использует sentinel)
- `service.CleanupExpiredTrials` — удаление просроченных trial с авто-удалением XUI клиентов

### Changed
- **BREAKING schema**: `subscriptions.is_trial`, `inbound_id`, `traffic_limit`, `subscription_url`, `deleted_at` удалены (migrations 010–011). Trial определяется через `plan.name = 'trial'`
- **Source columns**: переименованы в `x_ui_host`, `x_ui_api_token`, `x_ui_inbound_id` (snake_case aligned with GORM tags)
- **Source.Trial field removed**: больше нет `Source.Trial bool` — связь через `plan_sources` join
- **`TRAFFIC_LIMIT_GB` env var удалена**: traffic limit хранится в `plans.traffic_limit` (DB), читается через `PlanTrafficLimitGB`
- **`SubscriptionResetDay`** теперь `config.SubscriptionResetDay` constant (был hardcoded `1`)
- **`service.Create(ctx, chatID, username, inviteCode string)`** — новый параметр `inviteCode`
- **`db.CreateSubscription(ctx, sub, inviteCode string)`** — атомарное резолвление invite + persist `sub.InviteCode`/`sub.ReferredBy` в одной транзакции (revoke old + insert new + resolve invite)
- **`BindTrial` теперь обновляет ВСЕ trial-источники** (раньше break после первого успеха)
- **`CreateTrial` агрегирует ошибки** через `errors.Join`; partial success логируется как `Warn("partial failures")` (раньше терялись)
- **`BindTrialSubscription` revoke'ит другие active subs** того же `telegram_id` в той же транзакции (защита от double-active race)
- **Soft delete удалён**: `gorm.DeletedAt` заменён на `status='revoked'`
- **Tests** comprehensive audit: 67 test files / 1079 funcs / 29565 lines → ~20-25% меньше после deduplication; coverage `service` 72.5%→92%, `database` 69.5%→82%, `metrics` 0%→30%; smoke tests вынесены в `//go:build smoke`; e2e tests убрали `chdirMu`/`findProjectRoot`/`os.Chdir` (embed.FS не зависит от cwd); `t.Cleanup` в `setupTestDB`

### Fixed
- **Trial bind не инкрементировал referrer cache** до 1 часа (`/start trial_X`): `handleBindTrial` не вызывал `h.IncrementReferralCount(referrer)`. Теперь вызывается на success path внутри `if sub.ReferredBy > 0`. БД остаётся source of truth, кэш синхронизирован немедленно
- **Trial bind двойная активация**: `Create` для `telegram_id=A` + concurrent `BindTrial` для trial-row этого же `telegram_id` могли оставить две active subs. `BindTrialSubscription` теперь revoke'ит все другие active subs для `telegram_id` в той же транзакции
- **Partial XUI failures** в trial: 1 из 3 источников падал — пользователь получал trial, но один из источников не работал без лога на уровне Error. Теперь `errors.Join` + `Warn` + `Inc` метрика
- **BindTrial обновлял только первый trial-источник** — остальные оставались со старым email "trial_<subID>" и лимитами trial-плана. `ReconcileOrphanedClients` мог позже удалить бинденный sub
- **`ReferredBy`/`InviteCode` не персистились** в обычных подписках: `service.Create` не передавал `inviteCode` в `db.CreateSubscription`. Теперь `inviteCode` параметр + atomic resolve внутри транзакции
- **`GetAllReferralCounts` неправильно агрегировал** из-за пропущенной persist: исправлено вместе с fix выше
- **`subscription.go:UpdateSubscription`** теперь сохраняет `referred_by` (раньше терял referrer при update)
- **`xui/client.go`** retry classification: DNS errors (не-reтриабельные) больше не ретраятся
- **`config.validate()`** теперь валидирует `Sources`: `XUIHost`, `XUIAPIToken`, `XUIInboundID` для каждого источника
- **Sources миграция**: колонки приведены к snake_case (`x_ui_host`, `x_ui_api_token`, `x_ui_inbound_id`), GORM tags выровнены
- **SQLite version check**: миграции фейлятся на старых SQLite без `DELETE ... RETURNING` (cleanup_expired_trials требует 3.35+)

### Security
- **Referral count cache** теперь синхронизирован с БД немедленно после trial bind — referrer видит актуальное число сразу

### Notes
- Требуется SQLite ≥ 3.35 для `DELETE ... RETURNING` (cleanup_expired_trials). Проверка в startup.
- Миграции 000–011: legacy `is_trial`/`traffic_limit`/`inbound_id`/`subscription_url`/`deleted_at` удалены окончательно. Downgrade до pre-2.4.0 невозможен без восстановления БД из бэкапа.
- Soft delete (`gorm.DeletedAt`) заменён на `status='revoked'`. `Unscoped()` вызовы удалены.
- Smoke tests изолированы в `//go:build smoke` (не запускаются по умолчанию, экономят 3.2s TestMain бинарного билда). Для запуска: `go test -tags=smoke ./tests/smoke/`
- `feature/sources-table` ветка требует ревью перед merge в main: 112 файлов, +18179/-16940.

## [2.3.0] - 2026-05-24

### Added
- Centralized cache invalidation via `SubscriptionService.InvalidateSubscription()` (P1-2)
- Background orphan reconciler for XUI clients (P1-3): periodic scan & cleanup of DB entries whose clients are missing in XUI
- Context-aware singleflight for Subscription server (P1-4): `SingleFlight.Do(ctx, key, fn)` respects context cancellation, preventing goroutine leaks on shutdown

### Changed
- **Architecture: Handler decomposition (P1-1):** Split monolithic `Handler` (331 lines) into `CommandHandler`, `CallbackHandler`, `SubscriptionHandler`. `Handler` now acts as a facade. Removed dead files: `internal/bot/commands.go`, `internal/bot/message.go`, `internal/bot/subscription.go`, `internal/bot/callbacks.go`, `internal/bot/admin_handler.go`. Full backward compatibility.
- Fixed data race in `HandleBroadcast` by switching to `int64` + `sync/atomic` (P1-1)
- Implemented `StartCacheCleanup` (was a no-op) (P1-1)
- Removed unused `loadReferralCacheIfNeeded` (P1-1)
- Улучшено форматирование ссылок на пользователей (numeric usernames, кликабельные никнеймы в таблицах последних регистраций)

### Fixed
- Race condition in admin broadcast that could duplicate cancellation messages and corrupt counters under concurrency
- Cache invalidation inconsistencies: all invalidations now flow through `SubscriptionService.InvalidateSubscription`
- Potential nil map write in `checkAdminSendRateLimit` for handlers constructed without `NewHandler` (lazy init added)
- Goroutine leak in Subscription server when request context is cancelled (P1-4): now uses context-aware singleflight that releases waiters immediately on cancellation
- Множество исправлений после рефактора: ошибки в delegate handlers, rollback в BindTrial, singleflight cleanup, broadcast semaphore и GetTelegramIDsBatch, метрики (нормализация путей и команд), контексты в rate limit, nil checks и т.д.
- Исправлена грамматика в donate-сообщении

### Security
- Broadcast cancellation message is now sent exactly once, preventing duplicate messages due to concurrent goroutine exits

### Notes
- Полный аудит dev перед релизом (24.05.2026): build + vet чистые, 1450+ тестов прошли, 0 TODO/FIXME в коде, golangci-lint без ошибок.
- Рекомендуемый следующий шаг: PR dev → main, затем `git tag v2.3.0`.

