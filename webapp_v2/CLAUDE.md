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
- Test: `npm test` (vitest; the session fixtures in `test/fixtures/sessions` come from `npm --prefix ../webapp run fixtures:sessions`)
- Preview production: `npm run preview`

## Project Structure

```
src/
├── components/          # Presentational components (receive props, no business logic)
├── layout/              # App shell
├── modes/               # Route wrappers (gateway.jsx), ByRuleTraffic, the agents flag
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

## Routing, gates and roles

One product: the gateway. `hoop start control-plane` runs the same gateway and UI.

- **Route wrappers.** `modes/gateway.jsx` exports `{ Page, Guard, Home, Onboarding,
  CatchAll }`. `Page` is the shell of a React page (`layout/GatewayPage`: auth gate +
  Layout + PageLayout), `Guard` a React route without the shell. `Home`, `Onboarding` and
  `CatchAll` are the ClojureScript leaves `/`, `/onboarding/*` and `/*`. Only `Router.jsx`
  reads them.
- **Guardrail and masking rules have one form each.** `pages/Guardrails/Create/GuardrailForm.jsx`
  and `pages/Features/DataMasking/Create/DataMaskingForm.jsx` write both halves of a rule (the
  agent fields and `sidecar_spec`); an option one engine does not run carries a
  `RuleSupportTag` (`utils/ruleSupport.js`). Only the analyzer keeps `Gateway*Form` /
  `Sidecar*Form` siblings, picked per rule with `<ByRuleTraffic>` in `Router.jsx`.
- **Sidecar features are always on.** Every gateway serves sidecars, so the Sidecars
  pages, nav and palette items and the sidecar traffic of a rule carry no gate. The
  agents product follows `agentsEnabled()` (`modes/agents.js`, the `experimental.agents`
  flag). Off, `GatewayProtectedRoute` turns `/`, the agent onboarding and every path a
  nav or palette entry flags with `AGENTS_FLAG` (`modes/agentPaths.js`), sub-paths
  included, into the landing by role (`pages/Home`) on every route change, and the CLJS
  app does not move the URL while it is parked (`routes/navigate!`). Flag a new agent
  page in `gatewayNav.js` and its URL closes too; an agent page without a menu entry
  goes in `UNLISTED_AGENT_PATHS` (`modes/agentPaths.js`).
- **Auth is one gate.** `components/ProtectedRoute` (token, `/userinfo`, `/serverinfo`,
  flags, `adminOnly`, `role`, `licenseFeature`). `GatewayProtectedRoute` adds the
  agents swap.
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
- `src/pages/[Page]/sections/` = **Single-consumer page decomposition.** A file that exists only to keep a tab/page body small. Not a "component" — don't put it in `components/`. Example: `pages/Roles/Configure/sections/MetadataFieldsInput.jsx` is rendered by exactly one tab; it lives in `sections/`. A `components/SecretField` rendered by multiple renderers lives in `components/`.
- **Tabs / top-level page slices** live at the page root next to `index.jsx` (e.g. `pages/Roles/Configure/CredentialsTab.jsx`). They're not reusable and not decomposition — they're the page itself, sliced for readability.
- **Before creating a new component**, check `COMPONENTS.md` — it catalogs every existing component, hook, store, and service with usage examples.

**The reusability bar — what makes something a "component"?**

| Where it lives | What it is | Examples |
|---|---|---|
| `pages/[Page]/<X>.jsx` | A tab body or top-level slice of the page | `CredentialsTab.jsx`, `DetailsTab.jsx`, `ConfigureHeader.jsx` |
| `pages/[Page]/sections/` | Decomposition, single consumer, not reusable | `SidecarMirrorView.jsx`, `MetadataFieldsInput.jsx` |
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
- **Icons**: Use `lucide-react` exclusively. Do NOT use `@tabler/icons-react` or any other icon library.
  ```jsx
  import { TriangleAlert, Plus, Trash2 } from 'lucide-react'
  ```

### Code Style
- JavaScript only (no TypeScript)
- Functional components with hooks
- Named exports for stores, default exports for page components
- Keep components small and focused
- **JSX text with variables**: always use template strings or plain strings — never HTML entities (`&apos;`, `&quot;`, etc.) and never mix bare text with JSX expressions.
  ```jsx
  // ❌ Wrong
  <Text>&apos;{name}&apos; cannot be undone.</Text>
  <Text>Hello {name}!</Text>

  // ✅ Correct
  <Text>{`'${name}' cannot be undone.`}</Text>
  <Text>{'Hello ' + name + '!'}</Text>
  ```

## Real-time updates — use `useLiveEvent`, never a new stream or socket

When a page must react to a change made elsewhere (a sidecar checked in, a
record changed), subscribe with `useLiveEvent`. The browser holds one shared
connection to `GET /api/events/stream`: one tab streams and passes events to the
others. It opens with the first subscriber and closes when the last page
unmounts. Do not open an `EventSource`, a websocket or a new `fetch` stream for
this.

```jsx
import { useLiveEvent } from '@/hooks/useLiveEvent'
import { LIVE_EVENTS } from '@/services/liveEvents'

useLiveEvent(LIVE_EVENTS.SIDECAR_CHECKED_IN, (event) => {
  if (event.id === sidecar.id) refreshSidecar(sidecar.id)
}, enabled)
```

**Rules:**
- An event is `{ type, id }` and carries no data. Read the resource again
  through its store or service.
- A new type is added in both places: the gateway constant
  (`gateway/liveevents`) and `LIVE_EVENTS`.
- A page that must not miss an event keeps a slow poll: the stream is
  best effort (one replica, no replay, reconnects after 3-8s).
- Users: the sidecar setup wizard (`pages/Sidecars/Setup`), approval
  notifications (`features/ApprovalNotifications`) and the sidebar pending
  approvals count (`layout/Sidebar/usePendingApprovalsCount`).

## Snackbars / Toasts — use `showSnackbar`, never Mantine notifications

The legacy CLJS app shows toasts through `sonner` at the **top-right** of the screen.
The React side uses the **same library** (`sonner`) through a thin wrapper at
`src/utils/snackbar.jsx`, rendered by `src/components/Snackbar/Toast.jsx` (a
one-to-one port of the legacy toast), so users see one consistent toast style
across CLJS and React routes.

```jsx
import { showSnackbar } from '@/utils/snackbar'

showSnackbar({ level: 'success', text: 'Connection enabled.' })
showSnackbar({ level: 'error',   text: 'Failed to update.', description: err.message })
showSnackbar({ level: 'info',    text: 'Heads up.' })
```

**Rules:**
- Do NOT use `@mantine/notifications` — the dependency has been removed from the project
  (it produced a completely different visual from v1 and broke parity). Do not re-add it.
- The `<Toaster>` is mounted once at `src/App.jsx`. Do not add additional Toaster instances.
- The wrapper accepts the same `{ level, text, description }` shape as the CLJS
  `:show-snackbar` event so the mental model stays identical on both sides.
- Do NOT show snackbars through the CLJS bridge (`clojureDispatch('show-snackbar', ...)`) —
  the CLJS Toaster only exists while the CLJS tree is mounted, so toasts fired from
  React-only routes would be silently lost. Always use `showSnackbar` from `@/utils/snackbar`.

## Authentication Flow

### Overview
Authentication follows the same logic as the original webapp (ClojureScript):
- Supports **local auth** (email/password) and **IDP/OAuth** providers
- Token stored in localStorage as `jwt-token` (not just `token`)
- Token can come from cookies (`hoop_access_token`) or query params (`?token=xxx`)
- **No refresh token** - on 401, redirects to login
- Saves current URL before redirect for post-auth navigation

### Key Files
- `stores/useAuthStore.js` - Token management, cookie/query param handling
- `services/auth.js` - Login/logout API calls
- `services/api.js` - Axios interceptor for auth header and 401 handling
- `components/ProtectedRoute.jsx` - Route protection wrapper; `GatewayProtectedRoute.jsx` adds the agents swap
- `pages/Auth/Login/` - Login page (detects auth method from gateway)
- `pages/Auth/Register/` - Local auth signup form
- `pages/Auth/Signup/` - IDP org setup (post-OAuth)
- `pages/Auth/Callback/` - OAuth login callback
- `pages/Auth/SignupCallback/` - OAuth signup callback → redirects to `/signup`

### Auth Flow
1. **Check token**: If no token in localStorage, redirect to `/login` (saves current URL)
2. **Fetch user**: If token exists, fetch user data from `/api/users/me`
3. **Validate user**: If user data is empty/invalid, clear token and redirect to login
4. **401 Handling**: On any API 401 response, save URL, clear token, redirect to login
5. **OAuth Callback**: On `/auth/callback`, extract token from cookie/query, save to localStorage, redirect to saved URL or home
6. **OAuth Signup**: On `/signup/callback`, extract token, redirect to `/signup` for org setup

### Environment Variables
Env vars (`VITE_API_URL`, `SEGMENT_WRITE_KEY`, `API_URL`, `VITE_CLJS_URL`): see the table in `README.md` (Environment Variables).

## Re-frame Interop (CLJS ↔ React)

See `CLJS_PATTERNS.md` for the complete CLJS → React mapping (state, HTTP, lifecycle, routing, Tailwind → Mantine, and how to find CLJS source files).

When React needs to trigger a CLJS re-frame action (e.g., navigate to a CLJS-owned route, open a CLJS modal):

- **Never call `window.hoopDispatch` directly from a component.** Always wrap it in a Zustand store method. This makes it trivial to swap the underlying mechanism when the CLJS side is eventually removed.
- Put the wrapper in the most relevant existing store, or create a `stores/useBridgeStore.js` for cross-cutting concerns.

```js
// ✅ Correct — store owns the bridge call
// stores/useUIStore.js
openLegacyModal: (modalName) => {
  window.hoopDispatch(['modal->open', modalName])
}

// ❌ Wrong — component reaches directly into CLJS
window.hoopDispatch(['modal->open', 'some-modal'])
```

## Styling hierarchy — follow this order, never skip levels

**1. Mantine style props** — always first. Cover the vast majority of cases.
```jsx
<Box mih="100%" p="md" maw={400} w="90%" h="100vh" bg="gray.0" />
<Text c="dimmed" fz="sm" fw={600} ta="center" />
<Stack gap="lg" align="center" />
```

**2. `Component.extend()` in `src/components/[Name]/theme.js`** — for global defaults that apply to every instance of a component. Imported and assembled in `src/theme.js`.
```js
// src/components/NavLink/theme.js
export const NavLinkTheme = NavLink.extend({
  defaultProps: { radius: 'sm' },
  styles: { label: { fontWeight: '600' } },
})
```

**3. CSS Module with `var(--mantine-*)` only** — only when Mantine props cannot express the rule (pseudo-elements, `[data-*]` selectors, `:hover` with complex targets). See CSS Modules section below.

### Never use

```jsx
// ❌ style={{}} — always forbidden
<Box style={{ borderRadius: 8, color: '#3e63dd', padding: '8px 16px' }} />

// ❌ styles={{}} on instances — code smell: move it to Component.extend() in theme
<NavLink styles={{ label: { fontWeight: 600 } }} />
<AppShell styles={{ navbar: { transition: 'width 200ms ease' } }} />
```

`style={{}}` and `styles={{}}` on instances generate inline styles on the DOM, bypass the theme, and scatter visual decisions across the codebase. If you find yourself reaching for either, step back:
- Simple value? → use a Mantine prop
- Repeated across instances? → move to `Component.extend()`
- Complex selector? → CSS Module with `var(--mantine-*)`

Accepted exceptions for `styles={{}}`:
- Mantine `Transition` animation spread: `style={transitionStyles}`
- Structural shell slots (AppShell, Drawer) where `classNames` loses to Mantine's own CSS specificity — use `styles` with constants defined at the top of the file, never with raw hardcoded values inline

## Wrapping Mantine components with context-specific styles

When a Mantine component (NavLink, Button, Drawer, Badge…) needs styles specific to one context, **create a wrapper component** that owns all the visual decisions. Never scatter `classNames` or `styles` props across call sites.

### Where the wrapper lives — reusability decides

| The wrapper is used… | Put it in… | Example |
|---|---|---|
| Across the whole app | `src/components/` | `StatusBadge` used in Sessions, Agents, Resources |
| Only inside one layout section | `src/layout/[Section]/` | `SidebarNavLink` only used in the Sidebar |
| Reused by ≥2 consumers inside one page | `src/pages/[Page]/components/` | `SecretField/` rendered by multiple credential renderers |
| Only one consumer inside one page | `src/pages/[Page]/sections/` | `MetadataFieldsInput.jsx` rendered by exactly one tab |

**`src/components/` is for truly reusable components.** A component whose styles are hard-coded for a specific context (dark sidebar, data table, modal shell) is NOT reusable — even if it wraps a generic Mantine component. Keep it co-located with the context it serves. And a file that exists only to break up a long tab body isn't a component at all — put it in `sections/`.

### Rules

1. **Apply all styles inside the wrapper** via `classNames={{}}` pointing to a co-located CSS Module. Never pass `styles={{}}` on instances.
2. **Expose semantic props** (`danger`, `blocked`, `profileItem`) so call sites stay declarative and free of CSS class logic.
3. **The CSS Module is the single source of truth** for that component's appearance — one file to read, one file to change.

```jsx
// ✅ Correct — wrapper owns classNames, call site stays clean
<SidebarNavLink danger label="Log out" onClick={onLogout} />

// ❌ Wrong — styling leaks into the call site
<NavLink
  styles={{ root: { color: 'rgba(255,120,120,0.85)' } }}
  classNames={{ root: classes.navLink }}
  label="Log out"
  onClick={onLogout}
/>
```

## Styled Components

When Mantine's built-in props and theme tokens are not enough for a visual requirement:
- Create a dedicated component in `src/components/` (not inline in the page).
- Use `Component.extend()` in `src/components/[Name]/theme.js` for global defaults.
- Use a CSS Module scoped to that component for complex selectors only.
- Never add global CSS or unscoped styles.

### CSS Modules — mandatory rule

CSS Modules are allowed **only** for complex selectors that Mantine props cannot express (pseudo-elements, `:nth-child`, etc.).

**NEVER hardcode design values in CSS Modules.** Every spacing, color, font size, radius, and line-height value must reference a Mantine CSS variable so the theme remains the single source of truth.

Available Mantine CSS variables (set by the theme in `src/theme.js`):

```css
/* Spacing. The theme's own scale carries an `Alt` suffix; Mantine's five
   defaults stay alongside it, so both spellings resolve and they are NOT the
   same size. There is no --mantine-spacing-xxl: that one silently resolves to
   nothing and the rule using it is dropped.
   theme:   xsAlt=4px smAlt=8px mdAlt=16px lgAlt=24px xlAlt=32px xxlAlt=48px xxxlAlt=64px
   Mantine: xs=10px sm=12px md=16px lg=20px xl=32px */
var(--mantine-spacing-xsAlt | smAlt | mdAlt | lgAlt | xlAlt | xxlAlt | xxxlAlt)
var(--mantine-spacing-xs | sm | md | lg | xl)

/* Font sizes — xs=12px sm=14px md=16px lg=18px xl=20px */
var(--mantine-font-size-xs | sm | md | lg | xl)

/* Line heights */
var(--mantine-line-height-xs | sm | md | lg | xl)

/* Border radius — xs=4.5px sm=6px md=9px lg=12px xl=18px */
var(--mantine-radius-xs | sm | md | lg | xl)

/* Colors — e.g. indigo, gray, green, amber, red, sky */
var(--mantine-color-{name}-{0-9})
var(--mantine-color-{name}-filled)       /* primary shade, solid bg */
var(--mantine-color-{name}-light)        /* light variant bg */
var(--mantine-color-{name}-light-color)  /* light variant text */
```

```css
/* ❌ Wrong — hardcoded values leak out of the theme */
.label { font-size: 12px; margin-bottom: 8px; color: #3e63dd; }

/* ✅ Correct — always reference the theme */
.label { font-size: var(--mantine-font-size-xs); margin-bottom: var(--mantine-spacing-sm); color: var(--mantine-color-indigo-8); }
```

## Text color — use Mantine tokens, not raw names

- For secondary text use `c="dimmed"`. The `--mantine-color-dimmed` variable is set in `theme.js` `cssVariablesResolver()` to Radix slate11 (`#60646c`, ~5.7:1 on white — passes WCAG AA). Body text is `--mantine-color-text` = slate12 (`#1c2024`).
- Never use raw names like `c="dark"` or `c="light"` — those palettes are not defined in `theme.js` and silently fall back to Mantine defaults. For near-black text simply omit the prop to inherit `var(--mantine-color-text)`; `gray.9` is `#4d4d60` (a mid slate), not near-black.
- On dark navy surfaces (`--brand-navy`), never color text with the gray scale — it is calibrated for light backgrounds. Use white color-mix tones instead (see `EnterpriseBanner.module.css`, `pages/Auth/Setup/Setup.module.css`).
- When adding a new palette or changing `primaryColor`, re-verify `c="dimmed"` contrast in DevTools.

## CSS Layers — do not disable

- The project loads Mantine via `@mantine/core/styles.layer.css` (not `styles.css`) and declares layer order in `src/layers.css`:
  ```css
  @layer mantine, app;
  ```
- `mantine` holds Mantine's built-in component CSS. `app` is declared but left empty — CSS Modules stay **outside** any named layer, which gives them the highest precedence in the cascade.
- Without this, Mantine's internal classes (`.mantine-Accordion-item`, etc.) compete with your CSS Module classes at equal specificity, and bundle import order decides the winner. Layers make CSS Modules win deterministically.
- Practical consequence: when you add a new CSS Module that targets a Mantine slot via `classNames={{}}`, you do NOT need `!important` or doubled selectors (`.foo.foo {}`).
- DO NOT change the Mantine import to `styles.css` (without `.layer`) — that reintroduces the cascade bug.

### CLJS stylesheet isolation

The legacy CLJS app (`/webapp`) ships its own stylesheet (`/css/site.css`, Tailwind + Radix). `ClojureApp.jsx` loads it as a regular `<link rel="stylesheet" data-cljs-css>` on mount, and on unmount toggles `link.disabled = true`. Remounts re-enable the same `<link>` — the browser keeps the parsed stylesheet in memory, so there is no re-fetch and no flash of unstyled content. This is why the CLJS CSS does NOT need a `@layer legacy` wrap: while a React-only route is rendered, the CLJS stylesheet is disabled and its rules do not enter the cascade at all.

Rules:
- Do NOT remove the `disableCLJSCSS()` call in ClojureApp's cleanup. Without it, Tailwind/Radix rules leak into every React page as soon as the user visits a CLJS route once.
- Do NOT switch to `<style>@import url(...) layer(...)</style>` — it works in theory but `@import` is processed serially by the CSS parser, producing a visible FOUC on mount and unpredictable timing on unmount.

## Symptom — "my CSS Module does nothing on a Mantine component"

Debug checklist:
1. Is the CSS Module imported in the JSX? (Vite only bundles it if there's `import classes from './X.module.css'`.)
2. Are classes applied via `classNames={{ slot: classes.foo }}`, not `className`? Internal slots of Mantine components ignore `className`.
3. Is the slot name correct? Check the "Styles API" section of the Mantine component's docs.
4. Does the CSS Module use only `var(--mantine-*)` values? Hardcoded hex/px are forbidden by the styling hierarchy — the only sanctioned literals are Figma alpha-overlay tints that have no Mantine variable (e.g. `rgba(0, 0, 51, 0.06)` Neutral Alpha and `rgba(0, 0, 0, 0.05)` Black Alpha, used by `Pill/theme.js` and `Sidebar.module.css` hover/active states), and rgba-white contrast painting on the dark `--brand-navy` surfaces (`SelectionCard`).
5. If it still doesn't apply and `layers.css` is imported, inspect the rule in DevTools and confirm it's inside `@layer mantine` — if not, the `styles.layer.css` import was broken.

## Reference Implementation
- `pages/Agents/` is the reference page showing the full pattern: store + service + list page + create page

## Required reading — do this before every task

**Read these files at the start of every session, before writing any code:**

1. `CONTEXT_MIGRATION.md` — architecture, shell/bridge contracts, routing split, migration status. Required for any work in this repo.
2. `CLAUDE.md` (this file) — coding rules, styling hierarchy, component strategy.
3. `COMPONENTS.md` — catalog of every existing component, hook, store, and service. **Check before creating anything new.**

**Additionally, for specific task types:**
- **Migrating a CLJS page to React** → also read `MIGRATION_CHECKLIST.md` and `CLJS_PATTERNS.md`
- **Building UI or adding a component** → `COMPONENTS.md` is mandatory (already listed above)
- **Any behavior that exists in the original app** → read the CLJS source first under `../webapp/src/webapp/` and replicate it; do not invent patterns the original already has

Skipping these reads is the leading cause of regressions: wrong column structure, invented UI that diverges from the original, components duplicated instead of reused, and styling that bypasses the theme.

## Migration Rule
This project is a migration of `../webapp/` (ClojureScript) to React — not a greenfield build.
Before implementing any behavior (mobile nav, modals, transitions, keyboard handling, etc.),
check how it works in the original app first (`../webapp/src/webapp/`). Replicate the behavior
using Mantine/React equivalents. Do not invent new patterns when the original already has one.

**When migrating a page**, follow `MIGRATION_CHECKLIST.md` — it covers every step from reading the CLJS source to updating the routing table, including verification against the original behavior.
