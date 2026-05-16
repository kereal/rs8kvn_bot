# Code Audit Findings (May 2026)

## Critical Bugs Fixed
1. **`internal/bot/subscription.go:269`** — `notifyAdmin()` was called with `time.Time{}` instead of `result.Subscription.ExpiryTime`, showing "01.01.0001" in admin notification. Fixed by passing `result.Subscription.ExpiryTime`.
2. **`internal/service/subscription.go:196-199`** — `DeleteByID()` had `_ = inboundID` dead code and silently discarded XUI DeleteClient error. Fixed by adding logger.Error call.

## Other Issues Found (not fixed)
- 14 unused message keys in `messages.go` (MsgStartGreeting, MsgAdminDelUsage, MsgErrClientExists, etc.) — code uses inline strings instead
- `ReferralCache.Save()` is a no-op dead method
- `KeyboardBuilder.FromConfig()` unused
- Inconsistent error logging between `Delete()` and `DeleteByID()`
