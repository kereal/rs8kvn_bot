# Admin panel — conventions for agents

Local administration panel for the bot. **Secondary to the bot**: keep it small.
The panel reads and edits the bot's SQLite database through the Go API in this
directory; it never talks to the database directly.

Binding rules live in [`doc/adr/0002-admin-panel-contract.md`](../doc/adr/0002-admin-panel-contract.md):
access model, per-entity write matrix, API envelope, units, secrets, mutation
boundaries, deletion policy, test expectations and the screen checklist. Read it
before adding a screen; this file only covers mechanics.

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
| GET | `/admin/api/plans` | — | `{"items":[…],"total":N}` |
| POST | `/admin/api/plans` | plan | `201` + created plan |
| PUT | `/admin/api/plans/{id}` | plan | `200` + updated plan |
| DELETE | `/admin/api/plans/{id}` | — | `200` + deleted plan |

Every list answers `{"items":[…],"total":N}`, even when it is not paginated.
Growing tables (`subscriptions`, `orders`, `audit_log`) take `?limit=` (default
50, max 200), `?offset=`, `?q=`, `?sort=&order=`; allowed sort fields are
whitelisted per handler.

Payloads use **bytes** for traffic (`0` = unlimited), **cents** for money,
**days** for durations, RFC3339 **UTC** for time. Conversion happens only in
`src/lib/format.js`.

Errors: `{"code":str,"message":str,"details":{…}}` with codes `unauthorized`,
`bad_request`, `validation`, `not_found`, `name_taken`, `protected`, `in_use`,
`stale`, `internal`. The SPA maps codes to Russian text in `src/lib/format.js`;
never render `message` directly. `validation` carries `details` as
`{field: reason}` for inline form errors.

`PUT` replaces all editable fields; unknown JSON keys are rejected (`400`).
Secrets never appear in responses — expose `has_<secret>: bool` instead.

Responses are `200` with a JSON body, not `204`: Chromium logs body-less `204`
replies to PUT/DELETE as `net::ERR_ABORTED`, which looks like a failure.

## Write scope (safety rule)

The panel MUST NOT mutate state machines. Full matrix in ADR-0002 §2.

| Entity | Panel access |
|---|---|
| `plans` | full CRUD (guards below) |
| `products`, `nodes`, `plan_nodes`, `invites` | full CRUD via repository methods (`api_token` is write-only) |
| `subscriptions` | read + 3 service calls: `AdminSetPlan`, new `RevokeSubscription`, `DeleteByID` |
| `orders`, `subscription_nodes`, `audit_log` | **read-only** |
| `broadcasts` | create/edit scheduled, cancel, delete — the bot worker sends; the panel never posts to Telegram |

Every panel mutation writes an audit event with `actor_type=admin` and
`actor_id=0` (0 means "the panel"; Telegram IDs are always positive) through
`service.WithAuditAdmin(ctx, 0)`. New events: one constant + one constructor in
`internal/database/audit_log.go`, no migration.

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
6. Tick the checklist at the end of ADR-0002.

## Verifying

```bash
go test ./admin/ ./internal/database/ -run 'Plan|Admin' -count=1
go test -race ./admin/ -count=1
```

Manual: set `ADMIN_TOKEN=…`, run the bot, open `http://127.0.0.1:8881/admin/`,
enter the token, exercise list → create → edit → delete. Dev mode with HMR:
`npm run dev` (proxies `/admin/api` to `127.0.0.1:8881`).

## Manual checklist: Журнал аудита (spec 07)

1. Первый запрос экрана — `sort=created_at&order=desc`: свежие события сверху
   без ручного переключения.
2. Фильтры: тип события / сущность / исполнитель (опции приходят из
   `/admin/api/audit/events`), ID сущности, даты от/до. Пустой результат
   предлагает «Сбросить фильтры».
3. Пагинация «‹ ›» и размер страницы 50/100/200; счётчик «Найдено: N».
4. Клик по строке раскрывает запись: old/new как `<pre>`-JSON с кнопкой
   «Скопировать»; null-состояния подписаны («поля не менялись» / «сущность
   создана» / «сущность удалена»). Повторное раскрытие не рефетчит.
5. Неизвестное событие (нет в словаре) показывается своим кодом моноширинно.
6. Экран только читает: ни одной кнопки мутации, бейдж «только чтение».
