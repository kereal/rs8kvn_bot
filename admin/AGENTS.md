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
  server.go          HTTP server, auth, SPA serving (embed `dist/`)
  plans.go           JSON handlers for plans
  contract_test.go   JSON key sets every view reads (see Type checking)
  server_test.go     API + SPA tests
  jsconfig.json      checkJs config for svelte-check
  src/App.svelte     shell: token gate, sidebar, hash routing
  src/views/         one file per screen
  src/lib/           api client, formatting, shared components
  dist/              Vite output — COMMITTED, embedded into the binary
tools/adminserve/    panel-only dev server (not shipped)
lefthook.yml         pre-commit: gofmt + fresh dist + Conventional Commits
```

`dist/` is committed on purpose: `go build` stays the only required build step
in Docker and CI. Rebuild it whenever `src/` changes — the CI job
`Admin Bundle Is Fresh` runs `npm ci && npm run build` and fails if the committed
bundle differs from `src/`.

## Build and checks

```bash
cd admin && npm ci                 # dev dependencies (fresh clone / lockfile change)
cd admin && npm run check          # svelte-check: JS type check over src/
cd admin && npm run build          # writes admin/dist
go build ./...                     # embeds dist

make check                         # gofmt + go vet + npm run check + go test ./...
make admin-serve                   # panel alone on a throwaway DB copy, demo rows
```

## Theme (light + dark)

The panel follows the OS: `prefers-color-scheme: dark` swaps the variables in
`src/app.css`. Colors in components come only from those variables:

| Variable | Light | Dark | Use |
|---|---|---|---|
| `--bg` | `#f6f7f9` | `#12141a` | page background, inputs, code blocks |
| `--panel` | `#ffffff` | `#191c23` | cards, dialogs, buttons |
| `--border` | `#e2e5ea` | `#2a2f3a` | borders |
| `--text` | `#171a1f` | `#e7e9ee` | text |
| `--muted` | `#6b7280` | `#9aa3b2` | secondary text |
| `--accent` / `--danger` / `--ok` / `--warn` | … | … | accents, with `*-soft` backgrounds |

There is **no** `--surface` or `--surface-alt`: a `var(--surface, #fff)` silently
falls back to white and produces light text on a white block in dark mode (the
audit log JSON had exactly this bug). Use `--bg` for inset blocks and
`color-mix(in srgb, var(--border) 35%, transparent)` for a tinted row. Never
hardcode a light background or a dark text color.

## Type checking (`jsconfig.json` + `svelte-check`)

`checkJs` is on, so `npm run check` type-checks the JS inside components and
`src/lib`. It catches, verified by hand:

- assigning a value of the wrong type (`Type 'number' is not assignable to type 'string'`);
- calling a method that does not exist on the value (`Property 'trim' does not exist on type 'number'`);
- `Date` arithmetic that TypeScript rejects (`new Date(a) - new Date(b)`); the fix is `.getTime()`.

It does **not** catch the trap that already shipped once: `bind:value` on
`<input type="number">` hands the handler a `number` at runtime while the state
stays typed `string`, so `String(...)`/`Number(...)` conversion at the boundary is
mandatory and invisible to the checker. Keep form state in the type the input
actually produces, and convert explicitly where the value crosses into a request
payload.

Nothing is replaced by this: server-side contract changes are pinned by
`admin/contract_test.go`, which asserts the exact JSON key set of every response
the views read. Type checks are for the JS side, contract tests for the Go side.

### Response types (`src/lib/types.js`)

`src/lib/types.js` holds JSDoc typedefs for every row the views read — key sets
copied from `admin/contract_test.go`, value types from the `json` tags of the DTOs
in `admin/*.go` (`time.Time` → `string`, `*time.Time` → `string|null`,
`json.RawMessage` → `any`). The module has no runtime code (`export {}`).

Annotate the container, not the request:

```js
/** @type {import('../lib/types.js').Plan[]} */
let items = $state([])
```

Then a typo in `item.total_count` fails `npm run check` instead of rendering
`undefined`. Two rules keep this honest:

- the annotation describes what the handler actually sends — read the DTO or the
  contract test, never `api.get`'s inferred `any`;
- when a view deliberately unwraps a wrapper (`audience = data.count ?? 0`), the
  state is the unwrapped type (`number|null`), not the wrapper typedef.

## Local run without the bot

`make admin-serve` (or `go run ./tools/adminserve -seed`) runs the panel alone
against a copy of `data/rs8kvn.db` (`-empty -seed` starts from a fresh database
with demo rows). Node clients are empty, so every external call is a no-op: the
harness never touches real panels. It prints `ADMIN_READY <url> token=<token>`.
`npm run dev` gives Vite with HMR and proxies `/admin/api`.

## Git hooks

`lefthook.yml` is installed with `lefthook install` (hooks live in the global
hooks path). Pre-commit runs `gofmt` on staged Go files and rebuilds plus stages
`admin/dist` whenever `admin/src` is staged; commit-msg enforces Conventional
Commits. Bypass with `LEFTHOOK=0 git commit`. Tests, lint and race stay in CI.

## API contract

Base path `/admin/api`, JSON, bearer token (`Authorization: Bearer <ADMIN_TOKEN>`).
The embedded SPA itself is served by `GET /admin/` (`handleSPA` in `server.go`),
outside the JSON API below.

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/admin/api/session` | — | `{"ok":true}` (token check) |
| GET | `/admin/api/overview` | — | агрегаты экрана «Состояние» |
| POST | `/admin/api/subscriptions/{id}/sync` | — | `200` + `{"subscription":{...},"warning":"..."}` |
| POST | `/admin/api/broadcasts/preview` | broadcast | `200` + предпросмотр (ничего не создаёт) |
| GET | `/admin/api/plans` | — | `{"items":[…],"total":N}` |
| POST | `/admin/api/plans` | plan | `201` + created plan |
| PUT | `/admin/api/plans/{id}` | plan | `200` + updated plan |
| DELETE | `/admin/api/plans/{id}` | — | `200` + deleted plan |

Every list answers `{"items":[…],"total":N}`, even when it is not paginated.
Growing tables (`subscriptions`, `orders`, `audit_log`) take `?limit=` (default
50, max 200), `?offset=`, `?q=`, `?sort=&order=`; allowed sort fields are
whitelisted per handler.

Payloads use **bytes** for traffic (`0` = unlimited), **days** for durations,
RFC3339 **UTC** for time, and **minor units** for money: RUB — kopecks
(1 RUB = 100 minor units), XTR — whole Telegram Stars (1 Star = 1 minor unit,
no scaling). Conversion (`toMinorUnits`/`fromMinorUnits`/`formatMoney`) happens
only in `src/lib/format.js`. One value carries its currency from the row
(`currency` field); currencies are never mixed in one aggregate.

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

## Manual checklist: Рассылки (spec 06)

1. «Новая рассылка» → форма. Кнопка в подвале — «Просмотр», не «Сохранить».
2. «Просмотр» открывает превью и **ничего не создаёт**: список рассылок
   остаётся прежним (`GET /admin/api/broadcasts` — `total` не меняется).
3. В превью видны: точное число получателей, фильтр словами, расписание и
   сообщение в виде пузыря.
4. Разметка: `*жирный*`, `_курсив_`, `~зачёркнутый~`, `` `код` ``,
   `[текст](ссылка)` — как в Telegram; `# Заголовок` и `__подчёркнутый__` —
   обычным текстом (Telegram в MarkdownV2 их не поддерживает).
5. «Назад к форме» возвращает в форму с фокусом в тексте сообщения и
   сохраняет введённое. Второй кнопки возврата нет — в футере превью только
   «Назад к форме» и подтверждение.
6. Подтверждение («Отправить N получателям») создаёт ровно одну рассылку в
   статусе `scheduled`; при пустой аудитории кнопка заблокирована.
