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
| `localStorage.react-shell = true` | Set by `ClojureApp.jsx`. Signals CLJS to skip rendering its own sidebar/header |
| `window.hoopSetRoute(path)` | Called by `ClojureApp.jsx` on path change to sync React Router → Pushy (CLJS router) |
| `window.hoopRemount()` | Called on remount to re-render Reagent without refetching user data |
| `localStorage.jwt-token` | Shared auth token. Both apps read/write the same key |
| `localStorage.react-shell = true` | Guards double-render of sidebar in CLJS mode |
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
/dashboard
/sessions, /sessions/filtered, /sessions/:id
/workflows/:correlation-id
/resources, /resources/new, /resources/configure/:id, /resources/:id/add-role
/resource-catalog
/provisioning
/guardrails/*
/features/access-control/*
/features/access-request/*
/features/machine-identities/*   (decision gate vs React /ai-agents-identities)
/features/runbooks/setup, /features/runbooks/rules/*
/features/ai-session-analyzer/*
/guardrails/*
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
| Clipboard copy/cut blocking | `events/clipboard.cljs` | 🔶 Partial — React hides copy buttons but has no document-level listeners (roadmap Parity track) |

---

## React App Summary (`/webapp_v2`)

### Stores (Zustand)

| Store | File | Responsibility |
|-------|------|----------------|
| `useAuthStore` | `stores/useAuthStore.js` | Token, cookie/query extraction, redirect URL |
| `useUserStore` | `stores/useUserStore.js` | User data, isAdmin, isFreeLicense |
| `useUIStore` | `stores/useUIStore.js` | Sidebar open/collapsed state (persists to localStorage) |
| `useAgentStore` | `stores/useAgentStore.js` | Agents CRUD, loading state |
| `useCommandPaletteStore` | `stores/useCommandPaletteStore.js` | Palette page navigation, search results |

### Services (Axios)

| Service | File | Endpoints |
|---------|------|-----------|
| api | `services/api.js` | Base instance + auth interceptor + 401 handler |
| auth | `services/auth.js` | `/publicserverinfo`, `/localauth/login`, `/userinfo`, `/serverinfo` |
| agents | `services/agents.js` | CRUD `/agents`, `/agents/:id` |
| dataMasking | `services/dataMasking.js` | CRUD `/datamasking-rules`, `/datamasking-rules/:id` |
| connections | `services/connections.js` | `getConnections()` (full list) + `getConnectionsPaginated({page,pageSize,search,connectionIds})` (`page`/`page_size`/`search`/`connection_ids` → `{pages,data}`) for infinite-scroll dropdowns |
| search | `services/search.js` | `/search?term=` |

### Dev Ports

| Service | Port |
|---------|------|
| Vite (React app) | 5173 |
| Gateway backend | 8009 (`VITE_GATEWAY_URL`) |
| shadow-cljs (CLJS bundle) | 8280 (`VITE_CLJS_URL`) |

### Env Variables

All optional — `vite.config.js` provides working defaults, so a `.env` file
is not required to run `npm run dev` or `npm run dev:full`.

```
VITE_API_URL       Optional. Overrides the /api default base URL
VITE_GATEWAY_URL   Dev only. Backend proxy target (default: localhost:8009)
VITE_CLJS_URL      Dev only. shadow-cljs proxy target (default: localhost:8280)
```

---

## Migration Pattern (Reference: `/pages/Agents/`)

The Agents page is the reference implementation. Follow this pattern for every new migration:

```
pages/FeatureName/
├── index.jsx             # List page
├── Create/
│   └── index.jsx         # Create/edit form
└── store.js              # Local store (only if state is page-scoped)
```

Steps to migrate a page:
1. Create service file: `services/featureName.js` (one function per endpoint)
2. Create store: `stores/useFeatureNameStore.js` or `pages/Page/store.js`
3. Build page components using Mantine only
4. Add route in `Router.jsx` above the `/*` catch-all
5. Sidebar link in `layout/Sidebar.jsx` is already there — just confirm `to` path matches

### Reusable building blocks (don't re-derive these per migration)

Several CLJS patterns recur across feature pages and already have shared React
equivalents. **Reach for these before writing a new one** (full props in `COMPONENTS.md`):

| CLJS pattern | React building block | Use it for |
|--------------|----------------------|------------|
| `features/promotion` (`*-promotion`) | `components/FeaturePromotion` + `layout/FullBleed` | Empty/gated feature pages (split marketing panel + illustration). |
| `resource-role-filter` / `attribute-filter` (full list) | `components/ValueFilter` | Single-value table/list filter over a fully loaded array. |
| `resource-role-filter` (paginated) | `components/AsyncValueFilter` | Single-value filter over a paginated, server-searched source. |
| `components/multiselect` `paginated` | `components/PaginatedMultiSelect` | Generic multi-select over a paginated, server-searched source. |
| `components/connections-select` | `components/ConnectionsMultiSelect` | Resource-role (connection) picker with infinite scroll + search. |
| `:connections->pagination` slice | `hooks/usePaginatedConnections` | Paginated connection option source (data layer for the two above). |

Infinite scroll uses Mantine's built-in `useIntersection` (sentinel at list bottom) — no bespoke component. The full `/connections` load stays only where a page must resolve every `connection_ids → name` (e.g. list displays); dropdowns paginate.

---

## What's Done vs Pending

### Done ✅
- React shell architecture (Layout, Sidebar, Header)
- CommandPalette (cmd+k / Spotlight) — fully functional with search and connection actions
- Sidebar — collapsible, persists state, synced with CLJS sidebar hiding via `react-shell` flag
- Auth pages — Login, Register (local), Signup (IDP org setup), Setup, Callback, SignupCallback
- Agents page (list + create wizard)
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
- Onboarding flow 
- Auth pages
- Slack & Webhooks integration pages (`/integrations/slack`, `/integrations/webhooks`) — legacy `/plugins/manage/*` route removed from CLJS

### In Progress / Known Gaps 🔄
- No shared ConfirmDialog component yet (pages build ad-hoc confirmations)
- Native-client-access flow still lives in CLJS and the React CommandPalette depends on it via the bridge
- Sentry, MS Clarity, Segment `track()`, document-level clipboard blocking and the org-migration dialog exist only in CLJS
- Slack/Webhooks plugin pages are being migrated on EVL-101 (PR #1633, in review — adds `/integrations/{slack,webhooks}` + legacy redirects)

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
- **Radix → Mantine gray mapping**: the `gray` scale in `webapp_v2/src/theme.js` is a slate-tinted ramp (`gray.0` `#f0f0f3` … `gray.9` `#4d4d60`); the Radix Slate text steps live outside the array as semantic tokens set in `cssVariablesResolver()` — `--mantine-color-dimmed` = slate11 `#60646c` and `--mantine-color-text` = slate12 `#1c2024` — so `c="dimmed"` works out of the box. Note `gray.9` is a mid slate, not near-black: to get body-text black, omit the color prop and inherit `--mantine-color-text`.
- **CSS Layers for Mantine vs CSS Modules**: `main.jsx` imports `@mantine/core/styles.layer.css` (not `styles.css`), and `src/layers.css` declares `@layer mantine, app;`. Mantine's built-in CSS lives in the `mantine` layer; CSS Modules stay unlayered, so they always win the cascade. Without this, `classes.item` of a CSS Module would compete with `.mantine-Accordion-item` at equal specificity and the result would depend on bundle order.
- **CLJS stylesheet toggle**: `ClojureApp.jsx` loads `/css/site.css` as a `<link data-cljs-css>` and toggles `link.disabled` on mount/unmount. This keeps the parsed stylesheet in memory (no re-fetch, no flash) but removes its rules from the cascade while a React-only route is active — otherwise Tailwind/Radix rules persist in `<head>` after any visit to a CLJS route and override every React page. Do NOT replace the `<link>` with `<style>@import url(...)</style>` — that serializes the fetch through the CSS parser and produces a visible FOUC.
