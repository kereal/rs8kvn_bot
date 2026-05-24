# XUI Client Delete (2026-05-24)

New endpoint (post 3x-ui update):
POST /panel/api/clients/del/:email
- Path: email (unique)
- Query (opt): keepTraffic=1 (retain traffic row)
- Response: {success:true, msg:"Client deleted"}

Migration:
- Changed XUIClient.DeleteClient(ctx, email string) error (removed inboundID + clientID/UUID)
- Internal: doDeleteClient uses the new /clients/del/{email} (url.PathEscape)
- All callers updated to pass email (XUIEmail(username, tgID) or "trial_"+subID for trials)
- DB cleanup for trials now RETURNING subscription_id and constructs "trial_"+subID email
- Updated: interfaces, testutil.Mock*, all *_test.go (bot, database, scheduler, xui, e2e), integration mocks, real handler prefixes
- Scheduler XUICleanupTarget and anonymous interfaces in TrialRepository aligned to email-only DeleteClient

Behavior:
- Delete by email removes client from ALL attached inbounds automatically
- Traffic record dropped (no keepTraffic param used yet)
- Signature change is breaking for direct callers but kept minimal surface (only email now required)

Compatible with the new /clients/add payload (client + inboundIds array).

Tests: build ok, 500+ unit/integration short tests green (xui Delete/Add, database cleanup, scheduler, bot handlers).