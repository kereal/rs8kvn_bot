# Project Improvements & Development Roadmap

**Generated:** 2026-03-31  
**Version:** v2.0.2  
**Overall Coverage:** ~80%

---

## Current State Assessment

After thorough review of every file in the project, here's the honest assessment:

**What's already good:**
- ✅ Goroutine leak issues fixed (backup scheduler, trial cleanup use `select { case <-ctx.Done() }`)
- ✅ Graceful shutdown properly implemented (semaphore, wg.Wait, timeout)
- ✅ Panic recovery with stack traces in Sentry
- ✅ BotConfig extracted, SubscriptionService layer started
- ✅ Circuit breaker for 3x-ui API
- ✅ Rate limiting per user
- ✅ Database migrations system
- ✅ ~80% test coverage

**What needs attention:** See priorities below.

---

## P0 — Critical Fixes (Do Now)

### 1. Hardcoded Donation Link & Contact
**File:** `internal/bot/handler.go:218-224`

The T-Bank donation URL and personal Telegram `@kereal` are hardcoded. Should be env-configurable:
```
DONATE_URL=REDACTED_DONATE_URL
CONTACT_TELEGRAM=kereal
```

### 2. Dead Code: `referrals` Map
**File:** `internal/bot/handler.go:44-45`

`referrals map[int64]int64` and `referralsMu` are declared but never read or written. Either implement referral tracking or remove.

### 3. No-Op Stub Methods
**File:** `internal/bot/handler.go:300-305`

`StoreConversation()` and `GetUserContext()` are empty stubs. Remove or implement.

### 4. Trial Creation Lacks Atomic Rollback
**File:** `internal/web/web.go:269-288`

If `CreateTrialSubscription` fails, xui client is deleted — but if that delete ALSO fails, an orphan client remains. Should use the same rollback-with-retry pattern as `SubscriptionService.Create()`.

### 5. Subscription Creation Lock Is Fragile
**File:** `internal/bot/subscription.go:50-62`

Manual lock/unlock pattern with `inProgress` map. If any code path panics between lock/unlock, the entry stays forever. Use `defer` properly or `sync.Map`.

---

## P1 — High Priority Refactoring

### 6. Extract Message Texts into Centralized Config
**Files:** `internal/bot/handler.go`, `subscription.go`, `admin.go`, `web/web.go`

All user-facing strings are hardcoded Russian text scattered across files. Benefits of extracting:
- Change text without recompilation (YAML/JSON config)
- Prepare for future i18n
- Reduce duplication ("❌ Временная ошибка. Попробуйте позже." appears in multiple files)
- A/B test messages

**Approach:** `internal/bot/messages.go` with a `Messages` struct or simple key-value map.

### 7. Inline HTML Should Be Externalized
**File:** `internal/web/web.go:349-463`

Massive HTML strings embedded in Go code. No syntax highlighting, hard to maintain, no template escaping.

**Solution:** Move to `internal/web/templates/` as `.html` files using `html/template` with `template.ParseFS()`. Automatic XSS protection via Go's template engine.

### 8. `Handler` Is a God Object
**File:** `internal/bot/handler.go:35-51`

14 fields, handles: subscription CRUD, admin commands, callbacks, menus, referrals, invites, QR codes, rate limiting, caching, notifications.

**Solution:** Split into focused handlers:
```
internal/bot/
├── handler.go              # Core struct + DI wiring
├── subscription_handler.go # Subscription lifecycle
├── admin_handler.go        # Admin commands
├── invite_handler.go       # Referral/invite flow
├── menu_handler.go         # Navigation
└── message_handler.go      # Message sending utilities
```

### 9. Expand `SubscriptionService` to Absorb All Subscription Logic
**File:** `internal/service/subscription.go`

Currently only has `Create`, `GetByTelegramID`, `Delete`. But `handleCreateSubscription`, `handleMySubscription`, `handleQRCode` in `internal/bot/subscription.go` still contain business logic (cache lookups, traffic queries, error classification).

**Solution:** Move ALL subscription business logic into `SubscriptionService`:
- `CreateWithPendingInvite()` — handles pending invite flow
- `GetWithTraffic()` — combines DB lookup + xui traffic query
- Bot layer should only handle Telegram formatting/sending

### 10. `ensureLoggedIn` Concurrent Login Deduplication
**File:** `internal/xui/client.go:149-171`

Two goroutines calling `ensureLoggedIn` after session expiry may both attempt login. The mutex protects the second check but not the breaker.

**Solution:** Use `singleflight.Group` from `golang.org/x/sync` to deduplicate concurrent login attempts.

---

## P2 — Medium Priority Improvements

### 11. Expiry Notification System
**Current State:** Not implemented

Daily scheduler that checks subscriptions approaching expiry:
- 7 days before: "⏰ Ваша подписка истекает через 7 дней"
- 3 days before: "⏰ Ваша подписка истекает через 3 дня"
- 1 day before: "⚠️ Ваша подписка истекает завтра!"

New `internal/notifier/` package with ticker-based scheduler.

### 12. `/extend` Admin Command
```
/extend <id> <days>
/extend <id> <traffic_gb>
```

### 13. Structured Error Types
**File:** `internal/bot/subscription.go:294-319`

`handleCreateError` uses `strings.Contains(errStr, "connection refused")` — fragile string matching.

**Solution:** Typed errors with `errors.Is()`:
```go
var (
    ErrXUIConnection   = errors.New("xui connection failed")
    ErrXUIAuth         = errors.New("xui authentication failed")
    ErrXUIClientCreate = errors.New("xui client creation failed")
)
```

### 14. Prometheus Metrics
Basic metrics exposed via `/metrics`:
- `bot_subscriptions_created_total` (counter)
- `bot_subscriptions_active` (gauge)
- `bot_trial_requests_total` (counter)
- `bot_callback_duration_seconds` (histogram)
- `xui_api_requests_total` (counter)
- `xui_api_duration_seconds` (histogram)

### 15. Broadcast Progress Reporting
**File:** `internal/bot/admin.go:174-279`

Currently sends "started" then "completed" with no intermediate feedback. Edit the message with progress:
```
📤 Рассылка: 150/500 (30%)
```

### 16. SQLite Connection Pooling
Configure GORM's underlying sql.DB:
```go
sqlDB, _ := db.DB()
sqlDB.SetMaxOpenConns(1)  // SQLite single writer
sqlDB.SetMaxIdleConns(2)
sqlDB.SetConnMaxLifetime(time.Hour)
```

### 17. Context Timeouts for All Bot Operations
Many handlers pass `ctx` without timeout. A slow xui call blocks indefinitely.

**Solution:** Wrap at handler entry:
```go
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
```

### 18. Integration Tests for Trial Flow
**File:** `internal/web/web_test.go`

The `/i/{code}` flow is the most complex feature but has limited test coverage:
- Cookie-based trial deduplication
- IP rate limiting
- Invalid invite codes
- Concurrent trial requests from same IP

### 19. Multi-Admin Support
**File:** `internal/config/config.go`

Currently `TELEGRAM_ADMIN_ID` (single). Change to `TELEGRAM_ADMIN_IDS=123,456,789`.

### 20. `internal/health/` Is Dead Code
**File:** `internal/health/health.go`

HANDOVER.md confirms: "legacy, unused — web/ replaces it". Delete it.

---

## P3 — New Features

### 21. Subscription Auto-Renewal Notification
When monthly reset happens:
```
🔄 Ваша подписка обновлена!
📊 Трафик сброшен до 100 ГБ
📅 Следующее обновление: 30 апреля
```

### 22. User Activity Tracking
Track last subscription check / VPN usage for:
- Identifying inactive users
- Re-engagement campaigns
- Cleaning abandoned trials

### 23. Admin Dashboard Command
`/dashboard` — comprehensive view:
- Total users, active subscriptions, trials
- Traffic usage summary
- Recent registrations (24h)
- Top referrers
- System health

### 24. Webhook Mode Support
Currently long polling only. For production with high traffic:
```
TELEGRAM_WEBHOOK_URL=https://vpn.site/webhook
```

### 25. Subscription Pause/Resume
Allow users to temporarily pause their subscription while traveling.

### 26. Analytics Dashboard (Web)
Simple web dashboard at `/admin/dashboard` (secret-protected):
- User growth chart
- Subscription status distribution
- Traffic usage over time
- Referral network graph

---

## P4 — Technical Debt & Performance

### 27. Suspicious Indirect Dependencies
**File:** `go.mod`

- `github.com/gohugoio/hugo v0.149.1` — Why is Hugo a dependency?
- `github.com/bep/godartsass/v2` — Hugo's dependency
- `github.com/air-verse/air` — Dev tool, should not be in `go.mod`

Run `go mod tidy` and audit indirect dependencies.

### 28. Cache Invalidation Gaps
**File:** `internal/bot/cache.go`

Cache invalidated on delete but not on other operations. Add TTL-based invalidation or explicit invalidation points.

### 29. Consider `log/slog` Instead of `zap`
Go 1.25 has mature `log/slog` in stdlib. Would remove one external dependency.

### 30. Fuzzing Tests
Go 1.25 native fuzzing for:
- `inviteCodeRegex` validation
- `escapeMarkdown` function
- `truncateString` function
- URL parsing in `GetExternalURL`

### 31. Migration Rollback Support
**File:** `internal/database/migrations/`

Only `.up.sql` files exist. Add `.down.sql` for each migration.

### 32. Config Hot-Reload
Watch `.env` file with `fsnotify` and reload:
- `LOG_LEVEL` — commonly needed
- `TRAFFIC_LIMIT_GB` — adjusting limits
- `DONATE_URL` — updating links

---

## Quick Wins (Under 30 Min Each)

| # | Task | Impact | Effort |
|---|------|--------|--------|
| Q1 | Remove dead `referrals` map | Code clarity | 5 min |
| Q2 | Remove empty `StoreConversation`/`GetUserContext` | Code clarity | 5 min |
| Q3 | Delete unused `internal/health/` | Reduce confusion | 10 min |
| Q4 | Extract donation/contact URLs to config | Flexibility | 15 min |
| Q5 | Add context timeouts to bot handlers | Reliability | 30 min |
| Q6 | Fix `go mod tidy` cleanup | Binary size | 15 min |
| Q7 | Add migration `.down.sql` files | Safety | 30 min |
| Q8 | Add `singleflight` for xui login | Performance | 20 min |
| Q9 | Add context cancellation check in broadcast | Graceful shutdown | 10 min |
| Q10 | Configure SQLite connection pooling | Reliability | 10 min |

---

## Recommended Implementation Order

### Sprint 1 — Cleanup & Stability (Week 1)
1. Quick Wins Q1-Q4, Q9-Q10
2. P0-4: Trial creation atomic rollback
3. P0-5: Subscription creation lock fix
4. P1-10: singleflight for xui login
5. P4-20: Remove unused health package

### Sprint 2 — Architecture (Week 2)
6. P1-6: Extract message templates
7. P1-7: Externalize HTML templates
8. P1-9: Expand SubscriptionService
9. P2-13: Structured error types
10. P2-17: Context timeouts everywhere

### Sprint 3 — Features (Week 3)
11. P2-11: Expiry notification system
12. P2-12: `/extend` admin command
13. P2-14: Prometheus metrics
14. P2-16: DB connection pooling
15. P2-19: Multi-admin support

### Sprint 4 — Polish (Week 4)
16. P2-15: Broadcast progress reporting
17. P2-18: Integration tests for trial flow
18. P3-23: Admin dashboard command
19. P4-29: Consider slog migration
20. P4-30: Fuzzing tests
