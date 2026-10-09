# Hoop WebApp V2 - Development Guidelines

## Stack

React 19 + JavaScript (no TypeScript) · Vite · Mantine v8 (sole styling — no
Tailwind) · Zustand · Axios · React Router v7 · lucide-react. Setup and
details: `README.md`.

## Commands
- Development (recommended): `npm run dev:full` — Vite + shadow-cljs together; ports, proxy and HMR caveats in `README.md`
- Development (Vite only): `npm run dev`
- Build: `npm run build`
- Lint: `npm run lint`
- Preview production: `npm run preview`

## Project Structure

```
src/
├── components/          # Presentational components (receive props, no business logic)
├── layout/              # App shell
├── modes/               # The product manifest (gateway.jsx) and ByRuleTraffic
├── features/            # Complex features (e.g., CommandPalette)
├── stores/              # Zustand global stores (cross-route state)
├── services/            # Axios API calls (one file per domain)
├── hooks/               # Reusable custom hooks
├── utils/               # Pure utility functions
├── pages/               # Route-based pages (each route = folder)
│   ├── [Page]/
│   │   ├── index.jsx        # Page component
│   │   ├── <TabName>.jsx    # Tab bodies / top-level page slices (siblings of index)
│   │   ├── components/      # Reusable components scoped to this page (≥2 consumers)
│   │   ├── sections/        # Page-specific decomposition (single consumer, not "components")
│   │   ├── store.js         # Local store (only if state is page-specific)
│   │   └── [SubPage]/
│   │       └── index.jsx
├── App.jsx              # Root component + providers
├── Router.jsx           # The route table
└── main.jsx             # Entry point
```

## Application modes

One product: the gateway. `application_mode` is always `"gateway"` (`hoop start
control-plane` is the gateway too), and `modes/gateway.jsx` is the only manifest.
`src/modes/` is the only reader of `appMode` (ESLint: `appMode` anywhere else is an
error).

- **The manifest is components, not flags.** `modes/gateway.jsx` exports `{ id, theme,
  postLoginPath, postSetupPath, Page, Guard, Home, Onboarding, CatchAll }`. `Page` is the
  shell of a React page (`layout/GatewayPage`: auth gate + Layout + PageLayout), `Guard` a
  React route without the shell. `Home`, `Onboarding` and `CatchAll` are the ClojureScript
  leaves `/`, `/onboarding/*` and `/*`. `Router.jsx` reads them through `useModeConfig()`;
  nothing else does.
- **Agent and sidecar rule forms are siblings.** Guardrail, masking and analyzer forms
  come as `Gateway*Form` (agent traffic) and `ControlPlane*Form` (sidecar traffic), picked
  per rule with `<ByRuleTraffic>` in `Router.jsx`.
- **The name is the contract, enforced by lint** (`eslint.config.js`): a `ControlPlane*`
  file cannot import `Gateway*`, `ClojureApp`, the CLJS bridge, `NativeConnections` or
  `ConfigStatus`; a `Gateway*` file cannot import `ControlPlane*`; an un-prefixed file
  cannot import either side (only `modes/` and `Router.jsx` may). Matching is
  case-sensitive.
- **Sidecar features are always on.** Every gateway serves sidecars, so the Sidecars
  pages, nav and palette items and the sidecar traffic of a rule carry no gate. The
  agents product follows `agentsEnabled()` (`modes/agents.js`, the `experimental.agents`
  flag). On, `GatewayProtectedRoute` sends an admin with no connection to the agent
  onboarding. Off, it turns `/`, `/client` and the agent onboarding into the landing by
  role (`pages/Home`) on every route change, and the CLJS app does not move the URL
  while it is parked (`routes/navigate!`).
- **Auth is one gate.** `components/ProtectedRoute` (token, `/userinfo`, `/serverinfo`,
  flags, `adminOnly`, `role`, `licenseFeature`). `GatewayProtectedRoute` adds the
  onboarding redirect through the `onReady` hook.
- **Roles.** `/userinfo` reports `role`. A role is a reserved group name; `standard` is
  the absence of one and is never stored as a group. The group names come from
  `/serverinfo` (`admin_role_name`, `approver_role_name`), never a literal. Gate a route
  with `<Page role={ROLE_APPROVER}>` and a nav or palette item with `role:`; `hasRole` in
  `utils/roles.js` is the single decision and admin passes every gate. This gates pages,
  not data: the route's own middleware in `gateway/api/server.go` is the authority on
  what a request returns.

## Architecture Rules

### Stores (Zustand)
- **Global stores** (`src/stores/`): State consumed by multiple pages (auth, user, resources, connections, agents, UI)
- **Local stores** (`src/pages/[Page]/store.js`): State that only exists in that specific page (form wizard steps, local filters)
- Access store state outside React with `useStore.getState()`

### Services (Axios)
- Base instance in `services/api.js` with auth interceptor and 401 handling
- One file per domain: `services/agents.js`, `services/resources.js`, etc.

**A service returns the raw Axios promise.** No `.then`, no `response.data`, no
defaulting — the store unwraps. `services/agents.js` is the shape to copy:

```js
// ✅ services/agents.js
export const agentsService = {
  list: () => api.get('/agents'),
  create: (data) => api.post('/agents', data),
}
// consumed in useAgentStore
const { data } = await agentsService.list()

// ❌ unwrapping inside the service
list: () => api.get('/agents').then((r) => r.data ?? []),
```

The store is where status, headers and defaulting belong, and a service that
unwraps forces a rewrite the day a caller needs a header or a status. **10 of
the 34 service files still unwrap**; they predate the rule and are not a
licence to add an eleventh. Touching one is the moment to fix it.

### Who owns a request

**The store owns the request: its loading flag, its error, and its
cancellation.** A page asks for an id and renders what comes back.

A page **may** import a service directly — 39 do, and routing a one-shot probe
through a store is worse, not better. What a page must not do is hand-roll
loading/error state for a resource a store already owns. A call that stays out
of the store on purpose: `Sidecars/Setup/sections/WaitingStep.jsx` polls every
3s. In a global store that re-renders every subscriber, for state that dies when
the wizard closes.

**One generation counter per resource** when a store's state has more than one
async writer, so a late response cannot restore what a newer one replaced.
`useSidecarStore` carries `listRequestId` and `selectedRequestId`; each fetch
takes the next number and commits only while it is still current. A superseded
*list* response still clears `loading` — the request it belonged to is over
either way, and leaving the flag set strands the page on a loader.

**Reset session-scoped stores on logout**, from the store's own side:

```js
useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) useMyStore.getState().reset()
})
```

Module state outlives a logout — nothing on that path reloads the tab — so the
next user in the same tab sees the previous one's data until the first fetch
answers. Subscribing from this side keeps the dependency pointing the right way
and avoids the cycle a call from `useAuthStore` would create. Live examples:
`useNativeAccessStore`, `useSidecarStore`.

### Components — Component Library Strategy

**Every UI primitive is wrapped.** We own every component — even a Button, a Table, a TextInput. App code never imports these directly from Mantine; it always imports from `@/components/`. This gives us:
- A single place to change the visual behaviour of any primitive across the whole app
- A Storybook-ready inventory: every component in `src/components/` is a candidate for a story
- Portability: if we ever replace Mantine, only the wrapper internals change, not the app

**How to apply this rule:**
1. Check `COMPONENTS.md` first — the component you need may already exist.
2. If it doesn't exist, create a wrapper in `src/components/[Name]/index.jsx` that imports the Mantine primitive internally and re-exports a styled, opinionated version.
3. The wrapper owns all `classNames`, `styles`, and default props. Call sites stay clean.
4. Update `COMPONENTS.md` with usage examples after creating the wrapper.

**Scope:**
- `src/components/` = Reusable across the whole app. No direct Mantine imports at call sites for any component that has a wrapper.
- `src/pages/[Page]/components/` = Reusable **within the page** (≥2 consumers, or a clear candidate to graduate to the global `src/components/`). May still import Mantine directly if no wrapper exists yet and it's too specific to generalise.
- `src/pages/[Page]/sections/` = **Single-consumer page decomposition.** A file that exists only to keep a tab/page body small. Not a "component" — don't put it in `components/`. Example: `pages/Roles/Configure/sections/ConnectionTagsEditor.jsx` is rendered by exactly one tab; it lives in `sections/`. A `components/SecretField` rendered by multiple renderers lives in `components/`.
- **Tabs / top-level page slices** live at the page root next to `index.jsx` (e.g. `pages/Roles/Configure/CredentialsTab.jsx`). They're not reusable and not decomposition — they're the page itself, sliced for readability.
- **Before creating a new component**, check `COMPONENTS.md` — it catalogs every existing component, hook, store, and service with usage examples.

**The reusability bar — what makes something a "component"?**

| Where it lives | What it is | Examples |
|---|---|---|
| `pages/[Page]/<X>.jsx` | A tab body or top-level slice of the page | `CredentialsTab.jsx`, `DetailsTab.jsx`, `ConfigureHeader.jsx` |
| `pages/[Page]/sections/` | Decomposition, single consumer, not reusable | `TestConnectionModal.jsx`, `ConnectionTagsEditor.jsx`, `MetadataFieldsInput.jsx` |
| `pages/[Page]/components/` | Reusable within the page (≥2 consumers) | `SecretField/`, `ToggleSection.jsx`, `ReviewSection.jsx` |
| `src/components/` | Reusable across the whole app | `Button`, `TextInput`, `Table` |

When something in `pages/[Page]/components/` starts getting reused outside the page, graduate it to `src/components/` (and update `COMPONENTS.md`).

### Layout
- `src/layout/` = App shell infrastructure (Layout, Header, Sidebar, PageLayout, EmptyState)
- These are not generic reusable components, but structural elements that define the app shell

### Features
- `src/features/` = Complex features with multiple interconnected components (e.g., CommandPalette)
- Features can have their own internal structure with pages, components, and utilities

### Pages
- Each page is a folder. Sub-pages are sub-folders.
- Shared files for a page and its sub-pages live at the page's root folder.
- Entry point is always `index.jsx`.

### Imports
- Use `@/` alias for absolute imports from src (e.g., `import { useAuthStore } from '@/stores/useAuthStore'`)
- Group: external libraries first, then `@/` imports, then relative imports

### UI Components
- Use Mantine components exclusively. No custom CSS unless absolutely necessary.
- Use Mantine's built-in props for styling (size, variant, color, etc.)
(+283 more lines)
