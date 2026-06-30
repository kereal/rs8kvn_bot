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


## Error Handling Conventions

This project distinguishes between user-initiated operations (must be reliable) and background best-effort work (can tolerate partial failure).

- **User-initiated** (`Create`, `BindTrial`, `RenewSubscription`, `Delete`): return errors to the caller. The handler will surface the failure to the user. Do NOT log + continue silently.
- **Create/GetOrCreateSubscription provisioning**: provisioning is eventual-consistency by design. `ensureSubscriptionNodes` creates `pending_add` records and triggers a best-effort `SyncSubscription`. If the immediate sync fails, the subscription is still created (status `active`) and the background `SyncPendingNodes` worker retries provisioning with exponential backoff. The user may receive a "success" response before VPN access is fully provisioned; this is a documented trade-off, not a bug. Sub URL is valid immediately (subserver serves config once the client is provisioned by the background worker).
- **Delete flow** (`Delete`, `DeleteByID`): two-phase. Phase 1 marks the subscription `revoked` (so `/sub/{id}` returns 404 immediately). Phase 2 deprovisions VPN access via sync (best-effort; background sync retries on failure). Phase 3 physically deletes the DB row. If deprovision fails, the subscription stays revoked and `ReconcileOrphanedClients`/`SyncPendingNodes` finishes removal in the background.
- **Background sync** (`SyncSubscription` for single-sub, `SyncPendingNodes`, `ReconcilePlanNodes`, `ReconcileOrphanedClients`): per-item failures are logged as `Warn` and processing continues. `SyncPendingNodes` returns an aggregate error (`errors.Join`) on partial failures so the caller can observe degraded runs; the scheduler (`SubscriptionSyncWorker`) treats this as best-effort (`logger.Warn`) and does NOT abort or change retry cadence. Only `context.Cancelled`/`DeadlineExceeded` abort the scan early.
- **Never** use `panic` for control flow in handlers or services. Panic recovery exists only at the top level (`main.go`, `handleUpdateSafely`).
- Always wrap errors with `%w` to preserve the chain for `errors.Is` / `errors.As` checks.
- Sentinel errors (`database.ErrSubscriptionNotFound`, `xui.ErrClientNotFound`) are the preferred way to signal expected "not found" states. Callers must use `errors.Is` to distinguish them from infrastructure errors.

