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

### Routing Split (Router.jsx)

| Route | Handler | Status |
|-------|---------|--------|
| `/login` | React | Done |
| `/register` | React | Done (local auth signup) |
| `/signup` | React | Done (IDP org setup) |
| `/auth/callback` | React | Done |
| `/signup/callback` | React | Done (IDP signup callback) |
| `/agents` | React | Done |
| `/agents/new` | React | Done |
| `/*` (catch-all) | ClojureApp (CLJS) | Ongoing |

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
/onboarding/*                 first-run setup
/dashboard
/sessions/:id?
/connections/*
/resources/*
/guardrails/*
/agents (being migrated)
/features/access-control/*
/features/access-request/*
/features/runbooks/*
/features/data-masking/*
/features/ai-session-analyzer/*
/guardrails/*
/jira-templates/*
/settings/license
/settings/infrastructure
/settings/attributes/*
/settings/jira
/settings/audit-logs
/organization/users
/plugins/*
/integrations/authentication
/integrations/aws-connect/*
/client (SQL editor)
/upgrade-plan
/auth/* (login, callback, etc.)
```

### Global Components in CLJS (need React equivalents before removal)

| Component | CLJS file | Migrated? |
|-----------|-----------|-----------|
| Sidebar | `shared_ui/sidebar/main.cljs` | ✅ Yes — `layout/Sidebar.jsx` |
| Command Palette (cmd+k) | `shared_ui/cmdk/command_palette.cljs` | ✅ Yes — `features/CommandPalette/` |
| Modal system | `components/modal.cljs` | ❌ Not yet |
| Snackbar / Toast | `components/snackbar.cljs` | ❌ Not yet |
| Confirmation Dialog | `components/dialog.cljs` | ❌ Not yet |
| Page loader | Re-frame `:page-loader-status` | ❌ Not yet |

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
| search | `services/search.js` | `/search?term=` |

### Dev Ports

| Service | Port |
|---------|------|
| Vite (React app) | 5173 |
| Gateway backend | 8009 (`VITE_GATEWAY_URL`) |
| shadow-cljs (CLJS bundle) | 8280 (`VITE_CLJS_URL`) |

### Env Variables

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

---

## What's Done vs Pending

### Done ✅
- React shell architecture (Layout, Sidebar, Header)
- CommandPalette (cmd+k / Spotlight) — fully functional with search and connection actions
- Sidebar — collapsible, persists state, synced with CLJS sidebar hiding via `react-shell` flag
- Auth pages — Login, Register (local), Signup (IDP org setup), Callback, SignupCallback
- Agents page (list + create wizard)
- Auth store, User store, UI store, Agent store
- ClojureApp bridge component
- Re-frame dispatch bridge — React can trigger CLJS actions via `window.hoopDispatch` (wrapped in Zustand stores)
- Vite proxy setup for CLJS and backend
- Onboarding (in progress, parallel track)

### In Progress / Known Gaps 🔄
- Onboarding flow (parallel track, partially done in CLJS)
- Auth pages implemented but end-to-end not yet tested in staging
- All other pages are placeholder `<ClojureApp>` delegates
- Modal/Snackbar/Dialog system not yet in React (CLJS still owns this)
- No notification/toast system in React

### Pages Prioritized for Migration (rough order)
1. Dashboard
2. Sessions
3. Resources / Connections
4. Features (Access Control, Runbooks, Data Masking)
5. Settings (Users, License, Infrastructure)
6. Plugins / Integrations

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
