# Fix: Back-button navigation for separate-message screens (QR / invite)

Date: 2026-07-17

## Problem
Screens that show content in a SEPARATE Telegram message (QR-code photo under
the subscription card, invite QR) broke: pressing "Назад" did not remove the QR
message, or instead deleted/re-sent the wrong message. Root cause was a stray
fix that made `handleBackToSubscription` RE-SEND the subscription card (spawning
a duplicate). The QR photo's `messageID` was never the one targeted.

## Fix
- `handleQRCode` / `handleBackToSubscription` in
  `internal/bot/subscription_handler.go`:
  - Open: send the QR as a NEW photo message; do NOT delete the underlying
    card/menu — it stays open underneath.
  - Back: delete ONLY that photo. The Back inline button lives on the photo, so
    the callback's `messageID` IS the photo's id — delete it directly, no extra
    state (`sync.Map` was tried then removed as unnecessary).
- Removed the network crutch in `cmd/bot/main.go` (reverted to plain
  `tgbotapi.NewBotAPI`), which was masking the real stale-binary issue.

## Contract (load-bearing — do not regress)
- Open = send new message, keep card underneath.
- Back = delete only that message, never re-show the card.
- Enforced by `TestNavigation_OpenAndBack` in `internal/bot/repro_qr_test.go`
  (covers `qr_code→back_to_subscription` and `share_invite→qr_telegram→
  back_to_invite`). `testutil.BotAPI` captures `DeletedMessageIDs`.
- Documented in `AGENTS.md` under "Back-button navigation (CRITICAL — was
  broken once)".

## Files
- internal/bot/subscription_handler.go
- internal/bot/repro_qr_test.go (renamed from TestRepro_QRBackDeleteTarget)
- internal/bot/subscription_test.go, internal/bot/callbacks_test.go (updated
  expectations: Back must NOT call Send)
- internal/testutil/testutil.go (DeletedMessageIDs capture)
- cmd/bot/main.go (network crutch removed)
- AGENTS.md (back-button navigation section)
