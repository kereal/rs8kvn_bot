# Instructions for AI Agents

## Before starting work

1. Activate the project in Serena:
   ```bash
   serena_activate_project(project="rs8kvn_bot")
   ```
2. Read the relevant Serena memories for context (architecture, code_style, test-info, etc.)
3. Отвечай всегда на русском
4. После окончания работы, если это требуется, обновляй документацию и память
5. Выбирай и применяй подходящие skills
6. Используй подходящие mcp-инструменты


## Git commits

- Коммит-сообщения — Conventional Commits: `feat:`, `fix:`, `docs:`, `test:`, `chore:` и т.п.
- НИКОГДА не добавлять авто-футеры вида «Generated with Codebuff» / «Co-Authored-By: …»
  в сообщения коммитов — только описание изменения.


## RTK - Rust Token Killer

**Usage**: Token-optimized CLI proxy for shell commands.

### Rule

Always prefix shell commands with `rtk` to minimize token consumption.

Examples:

```bash
rtk git status
rtk ls src/
rtk grep "pattern" src/
rtk find "*.rs" .
rtk docker ps
rtk gh pr list
```

## Codebase Knowledge Graph (codebase-memory-mcp)

This project uses codebase-memory-mcp to maintain a knowledge graph of the codebase.
ALWAYS prefer MCP graph tools over grep/glob/file-search for code discovery.

Project name in the graph: **`home-kereal-rs8kvn_bot`** (auto-generated from repo path).

### Priority Order
1. `search_graph` — find functions, classes, routes, variables by pattern
2. `trace_path` — trace who calls a function or what it calls
3. `get_code_snippet` — read specific function/class source code
4. `query_graph` — run Cypher queries for complex patterns
5. `get_architecture` — high-level project summary

### When to fall back to grep/glob
- Searching for string literals, error messages, config values
- Searching non-code files (Dockerfiles, shell scripts, configs)
- When MCP tools return insufficient results

### Examples
- Find a handler: `search_graph(project="home-kereal-rs8kvn_bot", name_pattern=".*Handler.*")`
- Who calls it: `trace_path(project="home-kereal-rs8kvn_bot", function_name="NewHandler", direction="inbound")`
- Read source: `get_code_snippet(project="home-kereal-rs8kvn_bot", qualified_name="home-kereal-rs8kvn_bot.internal.bot.handler.NewHandler")`
- Check architecture: `get_architecture(project="home-kereal-rs8kvn_bot")`


## Docs

Don't read and don't write
  * bypass_clients_comparison.md
  * bypass_research.md
  * marketing_strategy.md
  * nginx-xhttp-hysteria2-architecture.md
  * task-bot-integration.md


## Back-button navigation (CRITICAL — was broken once)

Screens with content in a SEPARATE message (QR photo, invite QR) + Back button:
**Open** = send new message, keep card underneath. **Back** = delete only that
message (its id comes in the callback), never re-show the card — re-sending
spawns a stray duplicate. Guard: `TestNavigation_OpenAndBack`
(`internal/bot/repro_qr_test.go`). Ref: `handleQRCode`/`handleBackToSubscription`.

## Error Handling Conventions

This project distinguishes between user-initiated operations (must be reliable) and background best-effort work (can tolerate partial failure).

**Subscription deletion policy**: subscriptions are NEVER physically deleted, except two cases:
1. admin `/del` — `SubscriptionService.DeleteByID` (the only product deletion path, backed by `DeleteSubscriptionByID`);
2. expired anonymous trials — `CleanupExpiredTrials` (trials are not real user subscriptions).
Everything else only changes subscription status (`revoked`, free-plan downgrade, reanimation). There is no `Delete(telegramID)` anymore — it was a dead duplicate.

- **User-initiated** (`Create`, `BindTrial`, `RenewSubscription`, `DeleteByID`): return errors to the caller. The handler will surface the failure to the user. Do NOT log + continue silently.
- **Provisioning (two-phase, user-initiated)**: Provisioning splits into a **DB-setup phase** and an **external-sync phase**, with different reliability contracts.
  - **DB-setup phase** (`GetNodesByPlanID`, `MarkActiveNodesPendingUpdate`, `ReconcilePlanNodes`): pure DB operations that create/update `pending_add`/`pending_remove` records. These are **structural prerequisites** — without them the background worker has nothing to retry — so failures MUST be returned to the caller (the handler surfaces the error to the user). The user sees an error, but the subscription/order row is already committed (status `active`); the state is recoverable via `ReconcileOrphanedClients`/`SyncPendingNodes` once the DB issue is resolved.
  - **External-sync phase** (`SyncSubscription`): best-effort. Calls the XUI/proxman node API to materialize VPN clients. If this immediate sync fails, the subscription stays `active` and the background `SyncPendingNodes` worker retries with exponential backoff. The user may receive a "success" response before VPN access is fully provisioned; this is the documented trade-off. Sub URL is valid immediately (subserver serves config once the client is provisioned by the background worker).
  - This two-phase split replaces the previous blanket "provisioning is eventual-consistency" wording: DB-setup is synchronous-must, external-sync is best-effort.
- **Delete flow** (`DeleteByID` only — admin `/del`): two-phase. Phase 1 marks the subscription `revoked` (so `/sub/{id}` returns 404 immediately). Phase 2 deprovisions VPN access via sync (best-effort; background sync retries on failure). Phase 3 physically deletes the DB row. If deprovision fails, the subscription stays revoked and `SyncPendingNodes` finishes the VPN-client removal in the background.
- **Background sync** (`SyncSubscription` for single-sub, `SyncPendingNodes`, `ReconcilePlanNodes`): per-item failures are logged as `Warn` and processing continues. `ReconcileOrphanedClients` REVOKES orphaned subscriptions (status `revoked`) instead of deleting them — the row stays in the DB, the subserver serves 404. `SyncPendingNodes` returns an aggregate error (`errors.Join`) on partial failures so the caller can observe degraded runs; the scheduler (`SubscriptionSyncWorker`) treats this as best-effort (`logger.Warn`) and does NOT abort or change retry cadence. Only `context.Cancelled`/`DeadlineExceeded` abort the scan early.
- **Never** use `panic` for control flow in handlers or services. Panic recovery exists only at the top level (`main.go`, `handleUpdateSafely`).
- Always wrap errors with `%w` to preserve the chain for `errors.Is` / `errors.As` checks.
- Sentinel errors (`database.ErrSubscriptionNotFound`, `xui.ErrClientNotFound`) are the preferred way to signal expected "not found" states. Callers must use `errors.Is` to distinguish them from infrastructure errors.
- **Traffic notifications** (`SubscriptionService.ProcessTrafficNotifications` / `SubscriptionTrafficWorker`): best-effort on traffic-limited plans only (`traffic_limit > 0`). Per-node failures are `Warn`-logged and skipped; per-subscription failures are `Warn`-logged and the scan continues. The worker NEVER re-enables a client whose quota is still exceeded — exhausted is a pure revenue CTA. It re-enables (via `UpdateClient(Enable=true)`) only when the counter was reset below the limit but the client is still disabled. Idempotency via `traffic_reminders_sent` bitmask (migration 038).

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

When the user types `/graphify`, use the installed graphify skill or instructions before doing anything else.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- Dirty graphify-out/ files are expected after hooks or incremental updates; dirty graph files are not a reason to skip graphify. Only skip graphify if the task is about stale or incorrect graph output, or the user explicitly says not to use it.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
