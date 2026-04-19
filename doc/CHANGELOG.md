# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Centralized cache invalidation via `SubscriptionService.InvalidateSubscription()` (P1-2)
- Background orphan reconciler for XUI clients (P1-3): periodic scan & cleanup of DB entries whose clients are missing in XUI
- Context-aware singleflight for subscription proxy (P1-4): `SingleFlight.Do(ctx, key, fn)` respects context cancellation, preventing goroutine leaks on shutdown

### Changed
- **Architecture: Handler decomposition (P1-1):** Split monolithic `Handler` (331 lines) into `CommandHandler`, `CallbackHandler`, `SubscriptionHandler`. `Handler` now acts as a facade. Removed dead files: `internal/bot/commands.go`, `internal/bot/message.go`, `internal/bot/subscription.go`, `internal/bot/callbacks.go`, `internal/bot/admin_handler.go`. Full backward compatibility.
- Fixed data race in `HandleBroadcast` by switching to `int64` + `sync/atomic` (P1-1)
- Implemented `StartCacheCleanup` (was a no-op) (P1-1)
- Removed unused `loadReferralCacheIfNeeded` (P1-1)

### Fixed
- Race condition in admin broadcast that could duplicate cancellation messages and corrupt counters under concurrency
- Cache invalidation inconsistencies: all invalidations now flow through `SubscriptionService.InvalidateSubscription`
- Potential nil map write in `checkAdminSendRateLimit` for handlers constructed without `NewHandler` (lazy init added)
- Goroutine leak in subscription proxy when request context is cancelled (P1-4): now uses context-aware singleflight that releases waiters immediately on cancellation

### Security
- Broadcast cancellation message is now sent exactly once, preventing duplicate messages due to concurrent goroutine exits

