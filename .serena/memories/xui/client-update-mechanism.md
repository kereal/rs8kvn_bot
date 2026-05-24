# XUI Client Update (2026-05-24)

New endpoint (post 3x-ui update):
POST /panel/api/clients/update/:email
- Path param: current email (identifier)
- Body: full client JSON object (replaces the row, not patch)
  Example fields: id, email (new), totalGB, expiryTime, tgId, enable, subId, limitIp, flow, reset, comment
- Response: {success:true, msg:"Client updated"}
- Changes apply to ALL attached inbounds automatically.

Migration:
- Updated XUIClient.UpdateClient signature: removed inboundID; added currentEmail as first param (the identifier for the path).
  New: UpdateClient(ctx, currentEmail, clientID, email, subID string, trafficBytes, expiryTime, tgID int64, comment string) error
- Rewrote doUpdateClient: direct client object payload (no more nested "clients" + "settings" string), URL uses /panel/api/clients/update/{escaped currentEmail}
- Kept clientID (uuid) in sig for the "id" field in body (preserves internal ID during replace).
- Used fallback flow "xtls-rprx-vision" (same as old error path) to avoid clearing protocol settings on full replace.
- Callers updated (only used for trial binding/upgrade):
  - service/subscription.go:BindTrial — currentEmail = "trial_" + subscriptionID
  - bot/command.go:handleBindTrial — same computation (duplicate bind logic)
- Updated: interface, testutil.MockXUIClient (struct + impl), xui/client_test.go (new path + body structure asserts, no more old nested settings), commands_test.go (2 mock funcs)
- No other callers in the codebase (UpdateClient exclusively for trial -> full upgrade path).

Behavior:
- Update by current email (for trials: the "trial_xxx" email at bind time)
- Target "email" in body can differ (email change during bind)
- Full object supplied to ensure subId, flow, reset etc. are preserved
- tgId and comment set from Telegram data + referrer info

Tests: build success, full short suite 1619 passed. Specific UpdateClient + BindTrial tests green.

Compatible with the new /clients/add and /clients/del patterns (email-centric, inbound-independent where possible).