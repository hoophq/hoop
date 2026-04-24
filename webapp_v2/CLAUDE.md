# Hoop WebApp V2 - Development Guidelines

## Stack
- **React 19** + JavaScript (no TypeScript)
- **Vite** - Build tool
- **Mantine v8** - Component library (sole styling solution, no Tailwind)
- **Zustand** - State management
- **Axios** - HTTP client
- **React Router v7** - Routing

## Commands
- Development: `npm run dev`
- Build: `npm run build`
- Lint: `npm run lint`
- Preview production: `npm run preview`

## Project Structure

```
src/
├── components/          # Presentational components (receive props, no business logic)
├── layout/              # App shell: Sidebar, Header, EmptyState
├── features/            # Complex features (e.g., CommandPalette)
├── stores/              # Zustand global stores (cross-route state)
├── services/            # Axios API calls (one file per domain)
├── hooks/               # Reusable custom hooks
├── utils/               # Pure utility functions
├── pages/               # Route-based pages (each route = folder)
│   ├── [Page]/
│   │   ├── index.jsx        # Page component
│   │   ├── components/      # Components scoped to this page
│   │   ├── store.js         # Local store (only if state is page-specific)
│   │   └── [SubPage]/
│   │       └── index.jsx
├── App.jsx              # Root component + providers
├── Router.jsx           # Route definitions
└── main.jsx             # Entry point
```

## Architecture Rules

### Stores (Zustand)
- **Global stores** (`src/stores/`): State consumed by multiple pages (auth, user, resources, connections, agents, UI)
- **Local stores** (`src/pages/[Page]/store.js`): State that only exists in that specific page (form wizard steps, local filters)
- Stores access services for API calls. Components access stores for state.
- Access store state outside React with `useStore.getState()`

### Services (Axios)
- Base instance in `services/api.js` with auth interceptor and 401 handling
- One file per domain: `services/agents.js`, `services/resources.js`, etc.
- Services return axios promises. Stores handle the response.

### Components
- `src/components/` = Reusable across the whole app. Receive props, no direct store access preferred.
- `src/pages/[Page]/components/` = Scoped to that page/domain only.
- **Before creating a new component**, check `COMPONENTS.md` — it catalogs every existing component, hook, store, and service with usage examples.

### Layout
- `src/layout/` = Shared layout infrastructure (Sidebar, Header, EmptyState, Layout container)
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
- `components/ProtectedRoute.jsx` - Route protection wrapper
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
- `VITE_API_URL` (optional): Custom API endpoint. Defaults to `/api` (relative to current domain)

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
| Only inside one page or feature | `src/pages/[Page]/components/` | `RunbookActionButton` only used in Runbooks |

**`src/components/` is for truly reusable components.** A component whose styles are hard-coded for a specific context (dark sidebar, data table, modal shell) is NOT reusable — even if it wraps a generic Mantine component. Keep it co-located with the context it serves.

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
/* Spacing — xs=4px sm=8px md=16px lg=24px xl=32px xxl=48px xxxl=64px */
var(--mantine-spacing-xs | sm | md | lg | xl | xxl | xxxl)

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

- For secondary text use `c="dimmed"`. The `--mantine-color-dimmed` variable is overridden globally in `src/main.jsx` to match the legacy webapp's Radix `--gray-11` (`gray.8` = `#8d8d8d`). Mantine's default (`gray.6` = `#d9d9d9`) has ~1.8:1 contrast on white — fails WCAG AA.
- Never use raw names like `c="dark"` or `c="light"` — those palettes are not defined in `theme.js` and silently fall back to Mantine defaults. Use `c="gray.9"` for near-black, or simply omit the prop to inherit `var(--mantine-color-text)`.
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
4. Does the CSS Module use only `var(--mantine-*)` values? Hardcoded hex/px are forbidden by the styling hierarchy — exception: `Sidebar.module.css` uses `rgba(255,255,255,…)` intentionally because it paints dark-on-dark where Mantine's gray palette (calibrated for light) would not fit.
5. If it still doesn't apply and `layers.css` is imported, inspect the rule in DevTools and confirm it's inside `@layer mantine` — if not, the `styles.layer.css` import was broken.

## Reference Implementation
- `pages/Agents/` is the reference page showing the full pattern: store + service + list page + create page

## Migration Rule
This project is a migration of `../webapp/` (ClojureScript) to React — not a greenfield build.
Before implementing any behavior (mobile nav, modals, transitions, keyboard handling, etc.),
check how it works in the original app first (`../webapp/src/webapp/`). Replicate the behavior
using Mantine/React equivalents. Do not invent new patterns when the original already has one.

**When migrating a page**, follow `MIGRATION_CHECKLIST.md` — it covers every step from reading the CLJS source to updating the routing table, including verification against the original behavior.
