# Migration Context: ClojureScript → React

This file exists to give Claude immediate context about the migration strategy
so research doesn't need to be repeated every session.

---

## The Big Picture

We have a monorepo. The original frontend (`/webapp`) is a ClojureScript SPA that
cannot be rewritten overnight. The strategy is a **React Shell**: `webapp_v2`
(React/Vite) wraps the old app — providing the global shell (Sidebar, CommandPalette)
while ClojureScript continues to render page content. Pages are migrated one by one
to React until the ClojureScript bundle can be removed entirely.

```
webapp/      → ClojureScript, Reagent, Re-frame, Tailwind, Bidi router (LEGACY)
webapp_v2/   → React 19, Vite, Mantine v8, Zustand, React Router v7, lucide-react (TARGET)
```

---

## How the Shell Works

### At Runtime

```
┌──────────────────────────────────────────────────┐
│  React App (Vite, port 5173)                     │
│  ┌────────────────────────────────────────────┐  │
│  │  Layout (Sidebar + CommandPalette)         │  │
│  │  ┌──────────────────────────────────────┐  │  │
│  │  │  React page  (fully migrated route)   │  │  │
│  │  │  – OR –                               │  │  │
│  │  │  <ClojureApp>  (catch-all  /*  )      │  │  │
│  │  │    └─ mounts /js/app.js bundle        │  │  │
│  │  │       renders content-only (no nav)   │  │  │
│  │  └──────────────────────────────────────┘  │  │
│  └────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────┘
       ↓ Vite dev proxy /js /css /images
shadow-cljs dev server (port 8280) — serves ClojureScript bundle
       ↓ axios /api calls
Gateway backend (port 8009)
```

### Key Integration Contracts

| Mechanism | Description |
|-----------|-------------|
| `localStorage.react-shell = true` | Set by `ClojureApp.jsx`. Signals CLJS to skip rendering its own sidebar/header (guards the double-render) |
| `window.hoopSetRoute(path)` | Called by `ClojureApp.jsx` on path change to sync React Router → Pushy (CLJS router) |
| `window.hoopRemount()` | Called on remount to re-render Reagent without refetching user data |
| `localStorage.jwt-token` | Shared auth token. Both apps read/write the same key |
| `hoop:session-executed` (DOM CustomEvent on `window`) | Emitted by the CLJS web terminal on exec success (`editor_plugin.cljs`); the React Config Status widget listens and refreshes instantly |

### Routing Split (Router.jsx)

| Route | Handler | Status |
|-------|---------|--------|
| `/login` | React | Done |
| `/register` | React | Done (local auth signup) |
| `/signup` | React | Done (IDP org setup) |
| `/setup` | React | Done |
| `/auth/callback` | React | Done |
| `/signup/callback` | React | Done (IDP signup callback) |
| `/dashboard` | React | Done (admin-only; lazily loaded — it owns the recharts chunk) |
| `/agents` | React | Done |
| `/agents/new` | React | Done |
| `/settings/infrastructure` | React | Done |
| `/settings/license` | React | Done |
| `/settings/api-keys` | React | Done |
| `/settings/api-keys/new` | React | Done |
| `/settings/api-keys/created` | React | Done |
| `/settings/api-keys/:id/configure` | React | Done |
| `/settings/attributes` | React | Done |
| `/settings/attributes/new` | React | Done |
| `/settings/attributes/edit/:name` | React | Done |
| `/settings/protection-rules` | React | Done |
| `/onboarding/protection-rules` | React | Done (chrome-less, above the `/onboarding/*` splat) |
| `/settings/audit-logs` | React | Done |
| `/settings/experimental` | React | Done |
| `/organization/users` | React | Done |
| `/features/data-masking` | React | Done |
| `/features/data-masking/new` | React | Done |
| `/features/data-masking/edit/:id` | React | Done |
| `/roles/:connectionName/configure` | React | Done |
| `/rulepacks` | React | Done (gated by `experimental.rulepacks`) |
| `/rulepacks/:id` | React | Done (gated by `experimental.rulepacks`) |
| `/features/event-routing` | React | Done |
| `/features/event-routing/new` | React | Done |
| `/features/event-routing/:id/edit` | React | Done |
| `/features/event-routing/:id` | React | Done |
| `/ai-agents-identities` | React | Done |
| `/ai-agents-identities/new` | React | Done |
| `/ai-agents-identities/created` | React | Done |
| `/ai-agents-identities/:id/configure` | React | Done |
| `/jira-templates` | React | Done |
| `/jira-templates/new` | React | Done |
| `/jira-templates/edit/:id` | React | Done |
| `/settings/jira` | React | Done — absorbed into `/jira-templates?tab=configuration` |
| `/integrations/slack` | React | Done |
| `/integrations/webhooks` | React | Done |
| `/guardrails` | React | Done |
| `/guardrails/new` | React | Done |
| `/guardrails/edit/:id` | React | Done |
| `/features/access-control` | React | Done |
| `/features/access-control/new` | React | Done |
| `/features/access-control/edit` | React | Done — group name comes from `?group=<name>`, the legacy URL shape |
| `/plugins/manage/jira` | React (redirect) | Done — legacy URL → `/jira-templates?tab=configuration` |
| `/plugins/manage/slack` | React (redirect) | Done — legacy URL → `/integrations/slack` |
| `/plugins/manage/webhooks` | React (redirect) | Done — legacy URL → `/integrations/webhooks` |
| `/*` (catch-all) | ClojureApp (CLJS) | Ongoing — see `MIGRATION_ROADMAP.md` for the wave plan |

---

## Legacy App Summary (`/webapp`)

- **State**: Re-frame (Redux-like, event/subscription model)
- **Router**: Bidi + Pushy (HTML5 history)
- **Styling**: Tailwind CSS
- **HTTP**: Custom `http/api.cljs` wrapper — adds `Authorization: Bearer` header automatically, 401 → logout
- **Build**: `shadow-cljs` → outputs `resources/public/js/app.js`
- **Auth token key**: `localStorage.jwt-token` (must match React app)

### All CLJS Routes (what still lives in the old app)

```
/                             home (redirects to onboarding)
/onboarding/*                 first-run setup (except /onboarding/protection-rules → React)
/sessions, /sessions/filtered, /sessions/:id
/workflows/:correlation-id
/resources, /resources/new, /resources/configure/:id, /resources/:id/add-role
/resource-catalog
/provisioning
/features/access-request/*
/features/machine-identities/*   (decision gate vs React /ai-agents-identities)
/features/runbooks/setup, /features/runbooks/rules/*
/features/ai-session-analyzer/*
/plugins/*  (jira manage + review details only — slack/webhooks moved to React at /integrations/*)
/integrations/authentication
/integrations/aws-connect/*
/client (SQL editor)
/runbooks (runner)
/slack/user/new/:id, /slack/organization/new
/upgrade-plan
/idplogin, /logout
```

Dead bidi entries (route exists, panel deleted — cleanup planned in
`MIGRATION_ROADMAP.md` Track A): `/404`, `/hoop-app`, `/plugins/manage/jira`
(hangs on an infinite spinner today), `/plugins/reviews/:review-id`,
`/features/runbooks/edit/:connection-id`.

Shadowed bidi entries (route + panel still exist but React matches first, so
the CLJS page is unreachable): `/guardrails/*`, `/features/access-control/*`.
`events/guardrails.cljs` stays regardless — resource setup/configure and the
activation journey still subscribe to it. The `features/access_control/` tree
has no consumer outside itself (its `events.cljs` also registers
`:plugins->get-plugin-by-name-with-callback`, which nothing else dispatches),
so it can go whole, together with the `access-control-promotion` block in
`features/promotion.cljs`. Removal belongs to a Track A cleanup PR.

### Global Components in CLJS (need React equivalents before removal)

| Component | CLJS file | Migrated? |
|-----------|-----------|-----------|
| Sidebar | `shared_ui/sidebar/main.cljs` | ✅ Yes — `layout/Sidebar.jsx` |
| Command Palette (cmd+k) | `shared_ui/cmdk/command_palette.cljs` | ✅ Yes — `features/CommandPalette/` (Native Client action still bridges to CLJS) |
| Modal system | `components/modal.cljs` | ✅ Pattern replaced — `components/Modal` + colocated `useDisclosure` (no global registry by design) |
| Snackbar / Toast | `components/snackbar.cljs`, `components/toast.cljs` | ✅ Yes — `utils/snackbar.jsx` + `components/Snackbar/Toast.jsx` (sonner, top-right) |
| Confirmation Dialog | `components/dialog.cljs` | 🔶 Partial — no shared ConfirmDialog yet; pages build ad-hoc confirmations (planned: roadmap Wave 1) |
| Page loader | Re-frame `:page-loader-status` | ✅ Yes — `components/PageLoader` + `hooks/useMinDelay` |
| Native client access + draggable card | `connections/native_client_access/`, `components/draggable_card.cljs` | ❌ Not yet — hard blocker; React CommandPalette dispatches into CLJS (roadmap Wave 4) |
| org-migration dialog | `features/users/views/org_migration_dialog.cljs` | ❌ Not yet (roadmap Wave 3) |
| Sentry / Clarity / Segment `track()` | `events/tracking.cljs`, `events/clarity.cljs`, `events/segment.cljs` | ❌ CLJS-only today — React has Segment `identify()` + Intercom only (roadmap Parity track) |
| Clipboard copy/cut blocking | `events/clipboard.cljs` (deleted) | ✅ Yes — `hooks/useClipboardGuard` + `utils/clipboardPolicy`, installed once in `App.jsx`. Scope is the **document-level** listener; three CLJS views still call `navigator.clipboard.writeText` ungated (`integrations/authentication/views/advanced_tab.cljs:13`, `features/workflows/views/header.cljs:22`, `features/ai_session_analyzer/views/rule_form.cljs:189`) — separate code path, never covered by either implementation, follow-up EVL-177 |

---

## React App Summary (`/webapp_v2`)

One Zustand store per concern in `src/stores/`, one Axios service per API domain
in `src/services/` — **the filesystem is the source of truth**, don't trust any
enumerated list. Non-obvious notes (base API instance, bridge store methods,
pagination variants) live in `COMPONENTS.md` (Stores / Services sections).

Dev servers, ports, and env variables: see `README.md` (Development /
Environment Variables).

---

## Migration Pattern (Reference: `/pages/Agents/`)

The Agents page (`pages/Agents/`) is the reference implementation. Follow
`MIGRATION_CHECKLIST.md` for the full step-by-step — it includes Step 0
(read the CLJS source first) and Steps 7–8 (behavior-parity verification and
doc updates), which any shortened summary tends to skip.

Recurring CLJS features already have shared React building blocks
(FeaturePromotion, ValueFilter, PaginatedMultiSelect, …) — the mapping table
lives in `CLJS_PATTERNS.md` ("Recurring CLJS features → shared React building
blocks"); full props in `COMPONENTS.md`.

---

## What's Done vs Pending

### Done ✅
- React shell architecture (Layout, Sidebar, Header)
- CommandPalette (cmd+k / Spotlight) — fully functional with search and connection actions
- Sidebar — collapsible, persists state, synced with CLJS sidebar hiding via `react-shell` flag
- Auth pages — Login, Register (local), Signup (IDP org setup), Setup, Callback, SignupCallback
- Agents page (list + create wizard)
- Dashboard (`/dashboard`, admin-only) — three charts + today's overview. First chart consumer: introduced `@mantine/charts`, the `BarChart`/`DonutChart`/`SegmentedControl` wrappers and `CHART_SERIES_COLORS` in `theme.js`
- Configure Role page (`/roles/:connectionName/configure`) — write-only credentials, four tabs (Details, Credentials, Terminal Access, Native Access). Backward-compat Review section deliberately omitted; legacy editor still handles review-configured connections. Carries the CLJS features added after the migration started: `application/ssh-local` (proxy/local Connection Type radio in the SSH renderer, PR #1576) and the Google Vertex AI provider for `httpproxy/claude-code` (PR #1560, gated by `experimental.claude_code_vertex`).
- Settings pages — Infrastructure, License, API Keys, Attributes, Protection Rules, Audit Logs, Experimental
- Organization Users, Rulepacks (flag-gated), Event Routing, Data Masking, AI Agents Identities, Jira Templates (incl. the Jira integration Configuration tab)
- Onboarding Protection Rules (`/onboarding/protection-rules` — the rest of `/onboarding/*` is still CLJS)
- Toast/snackbar parity — `utils/snackbar.jsx` + `Toast.jsx` (same sonner library as CLJS)
- Page loaders (`PageLoader`, `AuthPageLoader`, `useMinDelay`)
- Auth store, User store, UI store, Agent store
- ClojureApp bridge component
- Re-frame dispatch bridge — React can trigger CLJS actions via `window.hoopDispatch` (wrapped in Zustand stores)
- Vite proxy setup for CLJS and backend
- Slack & Webhooks integration pages (`/integrations/slack`, `/integrations/webhooks`) — legacy `/plugins/manage/*` route removed from CLJS

### In Progress / Known Gaps 🔄
- No shared ConfirmDialog component yet (pages build ad-hoc confirmations)
- Native-client-access flow still lives in CLJS and the React CommandPalette depends on it via the bridge
- Sentry, MS Clarity, Segment `track()` and the org-migration dialog exist only in CLJS

### Migration order

The full wave plan (remaining pages, dead-CLJS cleanup batches, parity items,
bundle-removal endgame) lives in **`MIGRATION_ROADMAP.md`**. Update that file — not
this section — when scheduling migration work.

---

## Gotchas & Non-Obvious Details

- **Token key is `jwt-token`** not `token`. Both apps must use the same key.
- **CLJS sidebar hidden via `react-shell` flag** in localStorage. If the flag is missing, the user sees double navbars.
- **`window.hoopSetRoute`** must be called after every React Router navigation when ClojureApp is mounted — otherwise Pushy stays on the old route and content doesn't update.
- **CLJS runs inside a `<div id="app">`** created by ClojureApp. React renders its own tree elsewhere. They don't share React context.
- **Mantine is the only styling tool** — no Tailwind, no custom CSS files. The old app uses Tailwind; don't bleed it into `webapp_v2`.
- **Sidebar collapse state** is persisted to `localStorage.sidebar` (`"opened"/"closed"`). The CLJS sidebar also used to do this — keep the key the same.
- **Free vs Enterprise license** is checked from `/api/serverinfo` in `useUserStore`. Some nav items are hidden or locked for free tier.
- **`isAdmin` is derived** from user data (`user.role === 'admin'`). Admin-only routes are guarded in Sidebar and ProtectedRoute.
- **`window.hoopRemount()`** must be called on ClojureApp remount (not initial mount) to avoid re-fetching user data when React Router re-renders the component.
- **Radix → Mantine gray mapping**: the gray scale is slate-tinted; `dimmed`/`text` are semantic tokens set in `cssVariablesResolver()` — see `CLAUDE.md` "Text color".
- **Clipboard blocking lives in React only.** `useClipboardGuard()` is called from `App.jsx` and nowhere else — `Layout` misses `/onboarding/*` and `PageLayout` misses the CLJS catch-all. It listens in the **capture** phase and calls `stopImmediatePropagation`, because a canceled `copy` event still writes whatever a downstream handler put in `clipboardData` (CodeMirror does this). `navigator.clipboard.writeText` is invisible to it, so programmatic copies must go through `copyToClipboard` from `@/utils/clipboardPolicy`. Two consequences: the raw shadow-cljs origin (`:8280`) serves the CLJS-only `index.html` and therefore has **no** clipboard enforcement — test on the merged build; and on catch-all routes the React toast renders in a different sonner instance than the CLJS one, so the two can overlay until the CLJS Toaster goes away at bridge endgame.
- **CSS Layers**: Mantine loads via `styles.layer.css` + `@layer mantine, app;` so CSS Modules always win the cascade — see `CLAUDE.md` "CSS Layers — do not disable".
- **CLJS stylesheet toggle**: `ClojureApp.jsx` toggles the `<link data-cljs-css>` on mount/unmount so Tailwind/Radix rules never leak into React-only routes — see `CLAUDE.md` "CLJS stylesheet isolation".
