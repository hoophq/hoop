# CLJS → React Migration Roadmap

Status of record for finishing the `webapp/` (ClojureScript) → `webapp_v2/` (React)
migration. Produced from a full audit of both apps (routes, panels, dead code,
navigation, global infra parity) on 2026-07-27. `CONTEXT_MIGRATION.md` explains the
shell architecture; this file answers "what's left, in what order, and what can be
deleted".

**Working model:** three parallel tracks (Cleanup, Migration, Parity) plus a Phase 0
of quick wins and an Endgame (bundle removal). One Linear ticket = one worktree =
one draft PR, branch names from Linear. Every PR gates on: webapp compiles
(shadow-cljs release), `npm run lint && npm run build` in `webapp_v2`, and smoke
navigation of touched routes.

**Landed elsewhere (context):**
- `/plugins/manage/:name` (Slack + Webhooks) migrated on EVL-101 (PR #1633,
  merged): `pages/Integrations/{Slack,Webhooks}` live at
  `/integrations/{slack,webhooks}` with legacy redirects for
  `/plugins/manage/{slack,webhooks}`; the `Plugins/` stub and the CLJS
  `:manage-plugin` route/panel + plugin views + `events/slack_plugin.cljs` are
  gone. The `:manage-jira`/`:reviews-plugin-details` bidi entries remain
  (still Track A2).
- Snackbar unification landed on EVL-104 (PR #1638): all
  `@mantine/notifications` call sites moved to `showSnackbar`, the dependency
  removed, and `useBridgeStore.showSnackbar` deleted (the `show-snackbar` bridge
  event no longer exists).

---

## Phase 0 — Quick Wins (2 PRs, no dependencies)

### PR 0.1 — Shell bug fixes (S) — EVL-105, PR #1641 (merged)

Bugs found by the audit; all in `webapp_v2` except one CLJS one-liner:

- Remove CommandPalette dead links: Quick Access "Reviews" → `/reviews` and the
  connection action "Test" → `/connections/<name>/test` — neither route exists on
  either side (both land on the CLJS "Page not found" screen).
- Add admin/selfhosted gating to CommandPalette items (today QUICK_ACCESS exposes
  admin-only pages to any user); restore the `selfhosted-only` gating on the sidebar
  item Integrations > Authentication (the CLJS sidebar had it, the React one lost it).
- Fix Integrations > Jira sidebar highlight: `helpers.js isActive()` compares only
  pathname, but the target is `/jira-templates?tab=configuration`, so the item never
  activates.
- ~~Enforce `experimental.rulepacks` on the `/rulepacks` routes in `Router.jsx`~~ —
  verified non-issue: both Rulepacks pages already gate themselves with
  `FeatureFlagGate` (loader + redirect home when the flag is off), which is the
  established gate point. No Router change needed.
- Fix the attributes edit param mismatch: CLJS navigates to
  `/settings/attributes/edit?name=x` (query param —
  `webapp/src/webapp/features/attributes/views/list.cljs:48`) but the React route is
  `/settings/attributes/edit/:name`, so the URL falls through the catch-all and the
  supposedly-dead CLJS panel is still reachable. One-line CLJS fix to the path-param
  form.
- `/plugins/manage/jira` hangs forever: the bidi route exists but its panel was
  deleted (`:default` renders an infinite spinner). Add a React redirect →
  `/jira-templates?tab=configuration`. EVL-101 (PR #1633) redirects only
  `/plugins/manage/{slack,webhooks}` — the jira gap is ours; expect a trivial
  `Router.jsx` merge conflict with #1633 in the redirect block.

### PR 0.2 — Scaffolding deletion + doc refresh (S) — EVL-115 (this PR)

- ✅ Delete the unrouted, unreferenced 7-line stubs from the initial shell commit:
  `pages/{Connections (incl. Setup/), Dashboard, Sessions, Reviews, Guardrails,
  Resources}`. Verified: zero imports anywhere; Sidebar/CommandPalette reference
  those features by *path* only (they land in the CLJS catch-all). Also deleted
  the orphaned `pages/Integrations/Authentication` stub — EVL-101 merged without
  touching it, and B3.7 rebuilds that page from scratch.
- ✅ Reorganize the webapp_v2 docs to "single owner per topic": each topic lives
  in exactly one of the 7 md files (see the Documentation map in `README.md`);
  store/service inventories replaced by pointers to `src/stores/`/`src/services/`
  (the filesystem is the source of truth) plus non-obvious notes in
  `COMPONENTS.md`.
- ~~Switch the bridge snackbar call sites to `@/utils/snackbar`~~ — resolved:
  `Roles/Configure` by EVL-104 (PR #1638, which also deletes
  `useBridgeStore.showSnackbar`), `Onboarding/ProtectionRules` already local since
  PR #1627.
- Refresh `CONTEXT_MIGRATION.md` (routing table, global components table) — done in
  the same PR that introduces this roadmap.

---

## Track A — Dead-CLJS Cleanup (3 sequential PRs, start immediately)

These pages are unreachable by URL (React Router matches first) but their CLJS code
is still compiled into the bundle. Deleting early shrinks the bundle and reduces
`app.cljs`/`routes.cljs` merge noise for every later migration PR.

Two ordering constraints govern everything:

1. **bidi fails *silently*, so route deletions need a manual grep gate.**
   `bidi/path-for` does **not** throw on an unknown handler (verified against the
   bidi 2.1.6 jar: `bidi.cljc:391-398` `path-for*` throws only for a literally `nil`
   handler; `bidi.cljc:324-326` `unmatch-pair` uses `when-let` and returns `nil`
   otherwise). `routes.cljs:114-116` `url-for` is a thin `bidi/path-for` wrapper, so
   a stale reference yields `nil` — the anchor renders with no `href` at all (dead
   link) — and `routes.cljs:126-136` `navigate!` computes `(str nil "")` = `""` →
   `pushy/set-token! ""` → **silent misnavigation**. Nothing — not the compiler, not
   the runtime, not CI — catches a missed reference.
   Consequence: **before deleting any route entry**,
   `grep -rn "url-for :<kw>\|:navigate :<kw>" webapp/src` must return zero hits
   outside the files that PR deletes, with the output pasted into the PR
   description. **The gate — not PR ordering — is the safety mechanism**, so a route
   entry and its sidebar entry may be removed in the *same* PR (EVL-119 did exactly
   that for `:dashboard`). A1's separate prune of `shared_ui/sidebar/constants.cljs`
   (which calls `(routes/url-for …)` at load time for `:agents :settings-api-keys
   :settings-attributes :settings-infrastructure :settings-experimental
   :settings-audit-logs :license-management :users :ai-data-masking
   :integrations-authentication`) is kept for reviewability, not because bidi throws.
   Worked example waiting for A3: `features/attributes/events.cljs:74,108,131` each
   dispatch `[:navigate :settings-attributes]` in a file A3 **keeps**. It is not a
   live risk — those three `-success` handlers fire only from
   `features/attributes/views/form.cljs:52,53,80`, which A3 deletes in the same PR
   (live callers use `:attributes/create-inline`, which does not navigate) — so the
   gate hit must be resolved by **deleting the three handlers**, not by repointing
   them at a surviving route.
2. **`webapp.events.auth` is load-bearing but only required by dead code.**
   `auth/views/signup.cljs` (dead) is the only app-reachable require of
   `events/auth`, which powers live flows (`:auth->logout` from onboarding pages and
   the sidebar profile; `:auth->get-saml-link`/`get-auth-link` from the live
   `/idplogin` panel). Add `[webapp.events.auth]` to `app.cljs` requires **before**
   deleting signup, or logout/idplogin silently break.

### PR A1 — Safety prep (S) — EVL-116 — must land first

- Add `[webapp.events.auth]` to `webapp/src/webapp/app.cljs` requires.
- Prune from `shared_ui/sidebar/constants.cljs` the entries whose `url-for` targets
  A2/A3 will delete: `:agents :settings-api-keys :settings-attributes
  :settings-infrastructure :settings-experimental :settings-audit-logs
  :license-management :users`, plus the 4 `icons-registry` keys this orphans
  (`"Agents" "infrastructure" "license" "users"`) and the now-unused `BrainCog`
  refer. Do **not** touch `:ai-data-masking` or `:integrations-authentication`
  (still needed — see keep-list below), nor the `"authentication"` icon.
  `organization-routes` and `settings-management` are left as empty vectors. Their
  five consumers iterate with `for`/`mapcat`/`concat` and emit nothing, but two of
  them still render their own chrome over an empty vector: the Settings disclosure
  at `navigation.cljs:158-197` and its collapsed-sidebar twin at
  `main.cljs:210-219` (icon + "Settings" label + empty panel). No user sees it
  because `app.cljs:260` drops `sidebar/main` entirely in shell mode — the defs are
  harmless because the CLJS sidebar is unreachable, not because the consumers
  degrade gracefully. The legacy `app.cljs:288` (`STATIC_UI_PATH`) path would show
  the empty section. Deleting the defs, those two consumers, and the pre-existing
  `icons-registry` orphans (`"Reviews" "JiraTemplates" "jira"`) is A3.
- Remove two latent no-op dispatches (their registering namespaces are never
  loaded into the bundle): `events/users.cljs:143`
  (`:slack->send-message->user`) and `events/connections.cljs:88`
  (`:connections->filter-connections`).
- Add the four entries React's palette lacks — API Keys, Attributes, Experimental,
  Internal Audit Logs — to `features/CommandPalette/constants.js`, gating mirrored
  from `layout/Sidebar/constants.js`. **This closes a separate, pre-existing gap in
  the React palette — it does not restore what the prune removes.** The CLJS palette
  is still the only one rendered on CLJS routes (`app.cljs` renders it in the
  `react-shell` branch; `spotlight.js` delegates to it there) and builds its Quick
  Access from the pruned vectors, so it does lose 8 entries. All 8 pages stay one
  click away in the React sidebar, which is the shell and renders on every route, so
  the loss is ⌘K discoverability on CLJS routes only — accepted, and it disappears
  as those routes migrate.

### PR A2 — Dead settings/admin panels + routes (M) — EVL-117

Delete files + their `app.cljs` requires/defmethods + `routes.cljs` entries:

- `agents/{panel,new}.cljs` — **keep** `agents/deployment.cljs` (used by live
  resource setup + onboarding) and `events/agents.cljs` (used by 6+ live namespaces;
  only `:agents->delete-agent` may be pruned).
- `settings/license/` + `events/license.cljs`, `settings/infrastructure/`,
  `settings/api_keys/`, `settings/experimental/`, `audit_logs/` (+ its `db.cljs`
  initial-state key).
- Orphan `:audit-plugin-panel` defmethod (no route points at it).
- Route entries `:404 :runbooks-edit :hoop-app :reviews-plugin-details` (bidi
  entries with no panel and no references) and `:manage-jira` — gate satisfied:
  PR 0.1's React redirect merged with EVL-105/#1641.

Route entries that must **stay** (url-for/`:navigate` from live code):
`:configure-role` (navigated from live `/resources` pages), the `:ai-data-masking`
family (url-for from sidebar constants, configure-role tabs, data-masking analytics,
activation-journey templates), `:login-hoop`/`:auth-callback-hoop`/
`:signup-callback-hoop` (url-for in `events/auth` + logout view),
`:onboarding-protection-rules` (navigated from onboarding effects).

### PR A3 — Dead auth/users/org (M) — EVL-118

- `auth/local/*`, `auth/views/login_panel.cljs` (fully orphaned — zero requires),
  `auth/views/signup.cljs` (safe: A1 landed), `events/localauth.cljs`.
- `events/organization.cljs` + its orphaned sub (`subs.cljs:198`) + `db.cljs:77`.
- `events/slack.cljs` and `events/connections_filters.cljs` (never loaded; their
  dangling dispatch sites were removed in A1).
- `features/users/{main,views/user_list,views/user_form,views/empty_state}.cljs` —
  **keep** `events.cljs`, `subs.cljs` and `views/org_migration_dialog.cljs` (the
  dialog is mounted globally in the CLJS layout and is a Parity-track port source;
  the events/subs serve it and `features/promotion`).
- `features/attributes/{main.cljs,views/form.cljs}` — **keep** `events.cljs` +
  `subs.cljs` (used by live guardrails, resource setup/configure, machine
  identities, access control until those migrate).
- **The sidebar scaffolding A1 left empty** — `organization-routes` and
  `settings-management` in `shared_ui/sidebar/constants.cljs`, plus all five
  consumers: `navigation.cljs:98` (the `organization-routes` loop) and `:158-197`
  (the Settings disclosure), `main.cljs:186-198` and `:210-219` (their
  collapsed-rail twins), and the four references in
  `command_palette_constants.cljs:24,26,70,72`. Until this lands, an admin opening
  the raw shadow-cljs dev server (`:8280`, `react-shell` unset) sees a "Settings"
  disclosure that expands to nothing and a collapsed-rail gear that opens an empty
  panel. Nothing throws and no deployment is affected — every packaging path
  (`Makefile:256-258`, `scripts/dev/build-webapp.sh`) merges React's `index.html`
  over the CLJS resources, so `STATIC_UI_PATH` always serves the shell and
  `app.cljs:288` never runs. Flagged by Qodo on PR #1655; deliberately deferred
  here rather than patched with a `(seq …)` guard on a literal `[]`.
- **Deferred:** `events/reviews_plugin.cljs` (46 LOC) — session details still
  approves/rejects reviews through it; delete in Wave 6's cleanup commit.

**Traps found while landing A1 — read before starting A3:**

- `navigation.cljs:158-197` is **not** a whole-line deletion. The disclosure form
  closes at line 197 *column 60*; the trailing `]])` on that same line closes the
  enclosing `[:ul]` (95), `[:li]` (93) and `(when admin?)` (92). Those three closers
  must survive.
- Removing that disclosure leaves `navigation.cljs:94`'s
  `[section-title "Organization"]` hollow — delete it too, or the section renders a
  heading with no items.
- The `:settings-attributes` grep-gate hit is resolved by **deleting** the three
  `-success` handlers in `features/attributes/events.cljs` (74, 108, 131), not by
  repointing them — see Track A constraint #1.
- A3 must state explicitly that it removes the corresponding `routes.cljs` entries;
  the grep gate also flags `features/users/main.cljs:27` and the users
  `views/empty_state`.
- `events/connections_filters.cljs` is now 100% dead (A1 removed its last dangling
  dispatch site); confirm the deletion above actually lands, or the file lingers as
  a false signal that connection filtering flows through it.
- **Cross-PR orphan:** `LayoutDashboard` in the `lucide-react` refer of
  `shared_ui/sidebar/constants.cljs` had two consumers — the `"Dashboard"` icon key
  (removed by EVL-119) and `"infrastructure"` (removed by A1). Each PR is correct
  alone; once both are merged the refer has zero consumers. Sweep it with the other
  `icons-registry` orphans here.

---

## Track B — Migration Waves

Sizes are relative PR effort (S < M < L < XL). Waves are ordered; tickets inside a
wave can run in parallel. Shared CLJS infra migrates with its *first* consumer.

### Wave 1 — Visible value + tiny pages

| Ticket | Scope | Size | Unblocks |
|---|---|---|---|
| B1.1 Dashboard | `/dashboard` (admin-only): 3 charts + summary (`dashboard/` ~600 LOC, `events/reports.cljs`). Use `@mantine/charts` (recharts-based) — don't hand-roll recharts. | M | Priority #1 page; establishes chart patterns |
| B1.2 Small-pages sweep | `/upgrade-plan` (60 LOC), `/idplogin`, `/logout`, `/slack/user/new/:id`, `/slack/organization/new`, `/` + `""` → redirect to `/onboarding` | S | Removes 6 routes from the catch-all cheaply; exercises the auth-redirect pattern in React |
| B1.3 Shared ConfirmDialog | Build a shared ConfirmDialog component (Mantine Modal wrapper) + adopt in 1–2 existing pages | S | Every CRUD wave needs it; ends ad-hoc delete confirmations |

### Wave 2 — Sessions foundation

| Ticket | Scope | Size | Unblocks |
|---|---|---|---|
| B2.1 Sessions list | `/sessions` + `/sessions/filtered`: rich filter bar (user/connection/type/status/date), pagination, batch-id fetches. Port the list surface of `events/audit.cljs` (786 LOC) into `services/sessions` + a store designed to later host the SSE live tail (Wave 6). | L | Biggest de-risk for session details; filter/pagination components reused there |
| B2.2 Workflows | `/workflows/:correlation-id` (`features/workflows`, 659 LOC) | M | Independent |

### Wave 3 — Feature CRUD block (parallelizable)

All follow the same list/new/edit pattern with ConfirmDialog from B1.3:

| Ticket | Scope | Size | Notes |
|---|---|---|---|
| B3.1 Guardrails | `/guardrails(+new,edit)` incl. the `rules_table.cljs` port (257 LOC, fiddly) **+ port `activation_journey` template seeding** (2,482 LOC shared) | M+ | Do first in the wave — unblocks B3.2. Old URL stays even though React `/rulepacks` is the conceptual successor |
| B3.2 AI Session Analyzer | `/features/ai-session-analyzer(+rules)`: provider config, rules, system prompt. Free-license gate (1 rule) | M | Needs B3.1's template seeding |
| B3.3 Access Control | `/features/access-control(+new,edit)`, backed by the `access_control` plugin (GET/PUT `/plugins/:name`) | M | Independent |
| B3.4 Access Request | `/features/access-request(+new,edit)`. Free-license gate (1 rule) | M | Independent |
| B3.5 Runbooks Setup | `/features/runbooks/setup` + rules new/edit (git repo config + path rules) | M | Independent of the runbooks *runner* (Wave 5) |
| B3.6 Machine Identities | **Decision gate — see below** | S or M | |
| B3.7 Integrations Authentication | `/integrations/authentication` (~324 LOC but **sensitive**: switches gateway auth method, rotates API key; admin + selfhosted only). EVL-101 merged — unblocked; requires a dedicated manual test pass on a selfhosted instance — CI green is not enough. Only afterwards may `:integrations-authentication` be pruned from CLJS sidebar/routes | M | |

**Decision gate — Machine Identities (product decision pending):**
CLJS `/features/machine-identities` (API `/machineidentities`, 812 LOC) and React
`/ai-agents-identities` (API `/ai-agents`) coexist in the sidebar today and both
gateway APIs exist. Two options:

- **Option A — retire/absorb:** add React redirects
  `/features/machine-identities/* → /ai-agents-identities/*`, sunset the CLJS pages,
  plan the old API's deprecation. Cost: S. Requires product confirmation that
  ai-agents-identities is the successor and existing records are covered.
- **Option B — migrate as-is:** port the 812 LOC against `/machineidentities`.
  Cost: M.

**Default until decided:** the page stays on CLJS and does not block the wave; the
decision must close before the Endgame.

### Wave 4 — Resource setup wizard block (the big shared-infra investment)

| Ticket | Scope | Size | Unblocks |
|---|---|---|---|
| ~~B4.0 Native-client-access port~~ | ✅ **Done** — shipped early with EVL-171, because the Native Connections drawer had to live in the React shell to work on CLJS routes. `features/NativeConnections/` + `stores/useNativeAccessStore`; CommandPalette and Config Status now call the store directly. CLJS stands down via `react-shell?`; namespace deletion is a follow-up | L | — |
| B4.1 Resources list + wizard core | `/resources` list (537 LOC) + multi-step wizard skeleton (state machine, step chrome); port `events/connections.cljs`; reuse the kept `agents/deployment` logic as a React service | XL pt.1 | Everything below |
| B4.2 Resources new/configure/add-role | `/resources/new`, `/resources/configure/:id`, `/resources/:id/add-role`: federation OAuth, MCP OAuth popup + polling, terminal/native access tabs (needs B4.0). Reuse patterns from the already-React `/roles/:name/configure` — don't duplicate | XL pt.2 | Onboarding reuse |
| B4.3 Resource catalog + onboarding | `/resource-catalog` (562 LOC, no API — seeds wizard state) + `/onboarding(+setup, setup/resource, setup/agent, resource-providers)` chrome (~700 LOC) on a no-sidebar layout, reusing the B4.1/B4.2 wizard | L | Kills the `/onboarding/*` ClojureApp route in `Router.jsx` |
| B4.4 AWS Connect wizard | One shared wizard, two modes: `/integrations/aws-connect(+setup)` + `/onboarding/aws-connect` (~1,660 LOC). Port `events/jobs.cljs` 5s polling as a shared `useJobPolling` hook. | L | `useJobPolling` reused by Provisioning |

### Wave 5 — Runner stack + Provisioning (parallel lanes)

| Ticket | Scope | Size | Unblocks |
|---|---|---|---|
| B5.1 parallel_mode + jira gate | Port `parallel_mode/` (1,393 LOC — multi-connection exec engine) and the jira-templates prompt-gate flow (`jira_templates/` 168 + events 288) as standalone React features, no page attached yet | L | Runbooks runner, webclient, session-details review gate |
| B5.2 Runbooks runner | `/runbooks` (1,565 LOC): dynamic param forms from templates; first consumer of B5.1 | L | Proves parallel_mode + jira gate before the webclient bets on them |
| B5.3 Provisioning | `/provisioning` (4,741 LOC): plan/apply pipeline, CSV upload/parse, job tracking via `useJobPolling`. Split into 2 PRs (pipeline + plan view / apply + CSV + jobs) | XL | Independent lane, can run with a second owner |

### Wave 6 — XL monster 1: Session details

| Ticket | Scope | Size |
|---|---|---|
| B6.1 Session details core | `/sessions/:id` shell, data loading (services from B2.1), review approve/reject with the jira gate (B5.1), kill/re-run session. Verify then delete `events/reviews_plugin.cljs` in this PR's CLJS cleanup commit | XL pt.1 |
| B6.2 Playback + live tail | asciinema-player terminal replay, canvas RDP playback (port `session_data_rdp.cljs` 739 LOC; vendor `rle.js` unchanged), SSE live tail via fetch ReadableStream (port the pattern verbatim from `events/audit.cljs:666`), shared AgGrid table wrapper (also serves the webclient) | XL pt.2 |

Split rationale: core ships user value and retires review flows first; playback is
the fidelity-sensitive half and can bake behind the route incrementally.

### Wave 7 — XL monster 2: Webclient (last page standing)

By now every dependency exists: parallel_mode + jira gate (B5.1),
native-client-access (B4.0), AgGrid wrapper (B6.2), connections service (B4.1).
Three PRs behind an internal flag:

1. **B7.1 Editor core:** CodeMirror 6 + copilot AI completion, Allotment split
   panes, single-connection exec (`POST /sessions` — no websockets; 50s gateway
   timeout + "still running" handling), results grid.
2. **B7.2 Schema + parallel exec:** DB schema tree (`events/database_schema.cljs`
   433 LOC) + multi-connection exec via parallel_mode.
3. **B7.3 Gates + polish:** jira prompt gate, native-client-access entry points,
   localStorage code persistence, flag removal.

After B7.3 there are **zero panels behind the catch-all** → Endgame.

---

## Track C — Parity Track (interleaved with the waves)

Global CLJS behaviors that must exist in React before the bundle can be removed.
Decisions already made: **port Clarity** and **port Segment track()** (no metric
dies silently).

| Item | When | Notes |
|---|---|---|
| Bridge snackbar fixes | ✅ Done | EVL-104 (PR #1638) unified everything on `showSnackbar` and deleted `useBridgeStore.showSnackbar` |
| Shared ConfirmDialog | Wave 1 (B1.3) | Needed by every CRUD wave |
| Sentry init in React | Wave 1 (own S ticket) | Today errors on React routes go unreported unless a CLJS route loaded the bundle. Replicate the CLJS condition from `events/tracking.cljs` (init when gateway `analytics_tracking` is NOT enabled) |
| Segment `track()` | Wave 2 (own S ticket) | Add a `track()` util next to the existing `identify()` in `services/analytics.js`; wire the 8 CLJS `:segment->track` equivalents per page as each page migrates. The util must exist before Wave 3's CRUD pages land |
| Clipboard copy/cut blocking | Wave 2 (own S ticket) | Gap **today** on React-only routes: CLJS installs document-level copy/cut listeners when `disable_clipboard_copy_cut` is on; React only hides copy buttons. Install/remove the listeners in the React shell keyed on the flag |
| MS Clarity port | Wave 2–3 (own S ticket) | Port the script injection (`events/tracking.cljs`) + environment start/stop gating (`events/clarity.cljs`) |
| org-migration dialog | Wave 3 (own S ticket) | Port `features/users/views/org_migration_dialog.cljs` into the React Layout so it fires on React routes too (source kept alive by PR A3 for this purpose) |
| native-client-access | ✅ Done (EVL-171) | Ported ahead of Wave 4; see B4.0 |

**Bridge teardown inventory** (deleted in the Endgame): `utils/clojureDispatch.js`,
`stores/useBridgeStore.js`, `components/ClojureApp.jsx`, the CLJS branch in
`features/CommandPalette/spotlight.js`, `window.__hoopReactShellPresent` /
`__hoopReactShellCljsVisible`, `localStorage react-shell`. Remaining bridge events
after EVL-104: `users->get-user` (refreshLegacyUser), `command-palette->toggle`
(spotlight CLJS branch). `native-client-access->start-flow` is gone from the React side —
the traffic now runs the other way, over the `hoop:native-access-*` CustomEvents, which
outlive the flag because `/resources` and `/sessions/:id` stay CLJS.

---

## Endgame — Bundle Removal (after B7.3, 3 PRs)

**E1 — Route + bridge teardown (webapp_v2):**
- Remove the `/*` and `/onboarding/*` ClojureApp routes from `Router.jsx`; add a
  real React 404.
- Delete the full bridge inventory (list above).
- Grep gate: zero matches for `clojureDispatch|react-shell|hoopSetRoute|hoopRemount`
  in `webapp_v2/src`.

**E2 — Build teardown (Makefile/gateway):**
- `Makefile build-webapp`: drop the CLJS npm install / `release:hoop-ui` /
  `version.txt` steps and the `_ui_merge` two-source copy — package `webapp_v2/dist`
  alone; keep `embed-webapp` (`gateway/webappui/staticui`) consuming the new tar.
- Update `scripts/dev/build-webapp.sh`, `build-dev-webapp`, hoopgateway packaging,
  and any Dockerfiles/CI referencing `webapp/`.
- Verify gateway `webappui` static serving needs no path changes and `version.txt`
  consumers are fed from webapp_v2.

**E3 — Source removal:**
- Tag `pre-cljs-removal`, then delete `webapp/` entirely (shadow-cljs config,
  `css/site.css`, package.json, all CLJS).
- Retire/replace `CONTEXT_MIGRATION.md`, `CLJS_PATTERNS.md`,
  `MIGRATION_CHECKLIST.md`; update root `CLAUDE.md` / `DEV.md`.

**Exit checklist before E1 starts:** all wave tickets merged; Parity table complete;
Machine Identities decision closed; EVL-101 merged (✅ done); one full week of production
traffic with catch-all hit-count telemetry at zero (add a cheap counter/log to
`ClojureApp.jsx` mount during Wave 7 to prove it).

---

## Sequencing Summary

```
Now ──► Phase 0 (0.1, 0.2) ─┐
Now ──► Track A (A1→A2→A3) ─┤ parallel
Now ──► Wave 1 + Sentry ────┘
        Wave 2 + Segment track + clipboard + Clarity
        Wave 3 (parallel CRUD) + org-migration dialog + MI decision gate
        Wave 4 (B4.0 first, then wizard chain)
        Wave 5 (runner lane ∥ provisioning lane)
        Wave 6 (session details, 2 PRs)
        Wave 7 (webclient, 3 PRs)
        Endgame (E1→E2→E3)
```

## Top Risks

1. **Resource wizard scope (Wave 4)** — 6,614 LOC + OAuth popups; mitigated by the
   4-way PR split and reusing the already-React `/roles/:name/configure` patterns.
2. **Playback fidelity (Wave 6)** — RDP RLE canvas and SSE tail are
   behavior-fragile; vendor `rle.js` unchanged, port the fetch-ReadableStream
   pattern verbatim, add side-by-side manual comparison to the test plan.
3. **bidi silent failures during cleanup** — a deleted route entry produces a dead
   link or a no-op navigation, never an error, and CI cannot catch it. A2/A3 must run
   the `url-for` / `:navigate` grep gate per deleted keyword (see Track A constraint
   #1) and never delete the keep-list route entries. The sidebar constants prune (A1)
   still lands before the route deletions.
4. **Pending product decision** — Machine Identities has a default outcome (stays
   on CLJS) so no wave stalls, but it must close before the Endgame.
