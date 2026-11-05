# Admin panel — conventions for agents

Local administration panel for the bot. **Secondary to the bot**: keep it small.
The panel reads and edits the bot's SQLite database through the Go API in this
directory; it never talks to the database directly.

## Stack (pin, do not add alternatives)

| Layer | Choice |
|---|---|
| Framework | Svelte 5 (runes: `$state`, `$derived`, `$props`, `$effect`) |
| Build | Vite 7 + `@sveltejs/vite-plugin-svelte` 6 |
| Routing | `src/App.svelte` route map + URL hash — **no router package** |
| UI | hand-written components + `src/app.css` — **no UI kit, no Tailwind** |
| HTTP | `fetch` via `src/lib/api.js` |
| Backend | `package admin` (this directory), `net/http` `ServeMux` patterns |

Adding a second UI framework, CSS framework, or state manager is out of scope.
If a screen needs a new primitive (select, toast, date picker), write it in
`src/lib/` in the same style as `Modal.svelte`.

## Layout

```
admin/
  server.go        HTTP server, auth, SPA serving (embed `dist/`)
  plans.go         JSON handlers for plans
  server_test.go   API + SPA tests
  src/App.svelte   shell: token gate, sidebar, hash routing
  src/views/       one file per screen
  src/lib/         api client, formatting, shared components
  dist/            Vite output — COMMITTED, embedded into the binary
```

`dist/` is committed on purpose: `go build` stays the only required build step
in Docker and CI. Rebuild it whenever `src/` changes.

## Build

```bash
cd admin && npm run build          # writes admin/dist
go build ./...                     # embeds dist
```

`npm install` is needed only when `package.json` changes (or on a fresh clone).

## API contract

Base path `/admin/api`, JSON, bearer token (`Authorization: Bearer <ADMIN_TOKEN>`).

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/admin/api/session` | — | `{"ok":true}` (token check) |
| GET | `/admin/api/plans` | — | `{"items":[…]}` |
| POST | `/admin/api/plans` | plan | `201 {"id":N}` |
| PUT | `/admin/api/plans/{id}` | plan | `200 {"ok":true}` |
| DELETE | `/admin/api/plans/{id}` | — | `200 {"ok":true}` |

Plan payload: `{"name":str,"devices_limit":int,"traffic_limit":int(bytes),"is_active":bool}`.
`traffic_limit` is **bytes** (0 = unlimited); the UI converts to GiB.

Errors: `{"code":str,"message":str}` with codes `unauthorized`, `bad_request`,
`not_found`, `name_taken`, `protected`, `in_use`, `internal`. The SPA maps codes
to Russian text in `src/lib/format.js`; never render `message` directly.

Responses are `200` with a JSON body, not `204`: Chromium logs body-less `204`
replies to PUT/DELETE as `net::ERR_ABORTED`, which looks like a failure.

## Write scope (safety rule)

The panel MUST NOT mutate state machines:

| Entity | Panel access |
|---|---|
| `plans` | full CRUD (guards below) |
| `products`, `nodes`, `plan_nodes`, `invites` | write in later iterations, via repository methods |
| `subscriptions`, `orders`, `subscription_nodes`, `broadcasts`, `audit_log` | **read-only** — mutations belong to `SubscriptionService`/`OrderService` |

Guards enforced in `internal/database/plans_admin.go`, mirrored by disabled
buttons in the UI:

- `free`/`trial` plans cannot be renamed or deleted (`ErrPlanProtected`);
- `trial` cannot be deactivated — `GetPlanByName("trial")` filters inactive plans,
  so deactivation breaks trial provisioning;
- a plan referenced by products or subscriptions cannot be deleted
  (`ErrPlanInUse`); products cascade on plan delete, so a silent delete would
  erase catalogue rows;
- plan names are unique (`ErrPlanNameTaken`), trimmed, ≤ 50 chars.

## Adding a screen

1. Add DB methods in `internal/database/<entity>_admin.go` with sentinel errors
   for every refusal (never a bare `fmt.Errorf`).
2. Add a narrow interface + handlers in `admin/<entity>.go`, mount the route in
   `Handler()`.
3. Add `src/views/<Entity>.svelte` and one entry in the `routes` map in
   `src/App.svelte`.
4. Extend `admin/server_test.go` with the guard cases.
5. `npm run build` and commit `dist/`.

## Verifying

```bash
go test ./admin/ ./internal/database/ -run 'Plan|Admin' -count=1
```

Manual: set `ADMIN_TOKEN=…`, run the bot, open `http://127.0.0.1:8881/admin/`,
enter the token, exercise list → create → edit → delete. Dev mode with HMR:
`npm run dev` (proxies `/admin/api` to `127.0.0.1:8881`).
