# Control Plane Frontend — Development Guidelines

## Read first

| Read | Before |
|---|---|
| [`../PRODUCT.md`](../PRODUCT.md) | naming anything. The vocabulary join, and where the docs and the repository disagree. |
| [`COMPONENTS.md`](./COMPONENTS.md) | creating a component. What exists, and which directories under `src/components/` export nothing. |

`../CLAUDE.md` governs `controlplane/backend`, not this app.

## What this is

The web UI for hoop's control plane: where an admin manages a fleet of sidecars,
configures features once for all of them, and approves reviews. It is an
**administration surface only** — an end user never authenticates here, they reach
their resource through the sidecar, which is transparent to them.

Extracted from `webapp_v2/` (the gateway's UI) and diverging from it. `webapp_v2` is
frozen and will be deleted when the gateway is retired; do not try to keep the two in
sync, and do not import across the boundary.

There is no ClojureScript here. If you find a comment referring to a CLJS bundle, a
`/js/app.js` proxy or a `react-shell` flag, it is a leftover from the extraction —
delete it.

## Stack

React 19 + JavaScript (no TypeScript) · Vite · Mantine v8 (sole styling — no Tailwind) ·
Zustand · Axios · React Router v7 · lucide-react.

## Commands

| | |
|---|---|
| Dev | `npm run dev` (Vite :5173, `/api` proxied to the gateway on :8009) |
| Build | `npm run build` |
| Lint | `npm run lint` |
| Preview | `npm run preview` |

The control plane backend does not exist yet, so the API comes from the gateway. Start
it with `make run-dev` from the repo root.

## Routing

`src/Router.jsx` is the whole information architecture, and there is **no catch-all**.
A path the router does not claim renders `pages/NotFound`. That is deliberate: the
pages the gateway UI had and the control plane does not (terminal, runbooks, sessions
as a browsing surface, resources created by hand, agents, most of Settings) should fail
visibly rather than silently redirect.

Routes with no backend yet render `components/NotImplemented`, which names the Linear
project that owes the work and what is missing. Do not replace one with an empty table
or a spinner — a page that looks loaded but has no data behind it is worse than one
that says so.

Adding a route means adding it to `Router.jsx`, `layout/Sidebar/constants.js` and
`features/CommandPalette/constants.js`. The last two navigate, so an entry pointing at
an unclaimed path sends the user to the 404 page.

`npm run build` runs `scripts/check-routes.mjs`, which fails when a literal
`navigate()`, `to=` or `href=` target is not claimed by `Router.jsx`. This is the check
that catches the leftovers of removing a route — `navigate('/client')` after a
successful login was one, and nothing else would have found it.

**It only sees literal paths.** A destination held in a variable or built from one is
invisible to it: `const LICENSE_PAGE = '/settings/license'` and a `setRedirectTo(...)`
into `<Navigate to={redirectTo}>` both slipped through and had to be found by review.
Prefer literals — `` navigate(`/reviews/${id}`) `` is checked against `/reviews/:id`.

## Architecture

### Stores (Zustand)
- **Global** (`src/stores/`): state consumed by multiple pages (auth, user, UI).
- **Local** (`src/pages/[Page]/store.js`): state that only exists in that page.
- Stores call services. Components read stores. Outside React, use `useStore.getState()`.

### Services (Axios)
- Base instance in `services/api.js` with the auth interceptor and 401 handling.
- One file per domain. Services return promises; stores handle the response.

### Components — every UI primitive is wrapped

App code never imports a primitive straight from Mantine. It imports from
`@/components/`, which owns the `classNames`, `styles` and default props. This gives
one place to change a primitive across the app, and keeps the option of replacing
Mantine without touching call sites.

1. Check [`COMPONENTS.md`](./COMPONENTS.md) before creating anything. Do not read the
   `src/components/` directory listing as the catalogue: **six of its 40 directories
   export nothing you can import by directory name**. `AppShell`, `Input`, `Paper`,
   `Pill` and `Spotlight` hold only a `theme.js` or a `.module.css` and configure a
   Mantine primitive globally — import those from `@mantine/core` and the theme applies.
   `Snackbar` is reached through `showSnackbar`. `import Paper from '@/components/Paper'`
   gets you nothing.
2. If it does not exist, create `src/components/[Name]/index.jsx` wrapping the Mantine
   primitive, and add its row to `COMPONENTS.md`.
3. The wrapper owns the styling. Call sites stay clean.

| Where it lives | What it is |
|---|---|
| `pages/[Page]/<X>.jsx` | A tab body or top-level slice of the page |
| `pages/[Page]/sections/` | Decomposition, single consumer, not reusable |
| `pages/[Page]/components/` | Reusable within the page (≥2 consumers) |
| `src/components/` | Reusable across the whole app |
| `src/layout/` | The app shell — Sidebar, Header, Layout, EmptyState |
| `src/features/` | Multi-component features (CommandPalette) |

A component whose styles are hard-coded for one context is not reusable even if it
wraps a generic Mantine component — keep it next to the context it serves.

### Imports
`@/` for absolute imports from `src`. Group external libraries, then `@/`, then relative.

### Code style
- JavaScript only. Functional components. Named exports for stores, default for pages.
- **JSX text with variables**: template strings or plain strings. Never HTML entities
  (`&apos;`), never bare text mixed with JSX expressions.
  ```jsx
  <Text>{`'${name}' cannot be undone.`}</Text>   // ✅
  <Text>&apos;{name}&apos; cannot be undone.</Text>  // ❌
  ```
- **Icons**: `lucide-react` only. Note it ships no brand icons — there is no `Slack`
  export, for instance.

## Three of these screens are the gateway's

The most expensive mistake available here: reading a feature page and concluding that
**is** the product. It is not. This app was extracted from `webapp_v2/`, the gateway's
UI, and three pages came with it unchanged.

`docs/adr/0009-guardrails-and-masking-architecture.md` records why — two engines for two
deployments — and the docs say it from the other side: `features/guardrails` ends with
*"Looking for guardrails on the Hoop Gateway instead? That is a different
implementation."* Same note on `features/data-masking`.

| Page | Is | Not |
|---|---|---|
| `/guardrails` | the gateway engine, 2 rule types (`src/pages/Guardrails/helpers.js`) | the Sidecar's 8-type ordered deny list |
| `/features/data-masking` | gateway Live Data Masking: Presidio entity types, score threshold, per-connection | the Sidecar `mask` block, its four strategies, `entity` XOR `columns` |
| `/features/ai-session-analyzer` | the gateway analyzer, classifying a **session**, with an inline `api_key` | the AI Analyzer, classifying a **statement**, whose credential is a file path at `0600` |

Two consequences that bite:

- `require_access_request` in the analyzer form corresponds to `review`, which the docs
  mark *Coming soon* and which a Sidecar **refuses at startup**. Do not build on human
  approval as a live Sidecar path.
- Any Control Plane → Sidecar analyzer delivery must turn a stored key into a path or a
  mounted secret. A Sidecar config with an inline `api_key` is rejected as an unknown
  field. Never render a key input for the Sidecar path.

`/sidecars` is the admin landing page — `pages/Home/index.jsx` redirects there — and it
renders **mock data**. Every row is hard-coded in `pages/Sidecars/mock/`, the page says so
in a banner, and deleting that one directory is the whole removal. `GET /api/fleet` is
EVL-232 and answers 501 today. `/reviews` is still a `NotImplemented` stub, which names
the Linear project that owes it.

A mock is the same lie as an empty table unless the screen admits it. If you add another,
follow the shape: one directory, the banner inside it, and the components ignorant of
where the data came from.

## Naming — the product name comes from `../PRODUCT.md`

A new label, route segment, store key or service name uses the noun the product uses.
`../PRODUCT.md` has the table: `Resource Role`, `Session Analyzer`, `Live Data Masking`,
`connection` and `plugin` are all gateway vocabulary the docs do not use here.

**A recorded divergence is not a rename to do in passing.** The initiative has a naming
session pending, and half-renaming a noun across routes, the sidebar, the command palette
and the API leaves the app worse than either end. Change one when the decision lands, all
at once. Until then follow the surrounding code, and add the row to `../PRODUCT.md` if it
is missing.

Copying a page or a component from `webapp_v2/` brings the gateway's vocabulary with it.
Read it, then write the version this product wants.

## Documentation links

`src/utils/docsUrl.js` is the map, and the five sections that count are listed in
[`../PRODUCT.md`](../PRODUCT.md). Everything under `/docs/learn/`, `/docs/clients/` and
`/docs/quickstart/` documents the Hoop Gateway, a different product — link there only
when the screen really is the gateway's, and say so in a comment.

**The docs site has no 404.** A path that does not exist silently serves the docs home
page, so a link check on the HTTP status passes on a dead link. Check the body:

```bash
curl -sS -L "https://hoop.dev/docs/<path>.md" | head -8 | grep -q "Runtime control for agents" \
  && echo DEAD
```

## Snackbars — use `showSnackbar`

```jsx
import { showSnackbar } from '@/utils/snackbar'
showSnackbar({ level: 'success', text: 'Rule created.' })
```

Do NOT use `@mantine/notifications` — the dependency is deliberately absent. The
`<Toaster>` is mounted once in `src/App.jsx`; do not add another.

## Styling hierarchy — follow the order, never skip levels

**1. Mantine style props.** Cover the vast majority of cases.
```jsx
<Box p="md" maw={400} bg="gray.0" />
<Text c="dimmed" fz="sm" fw={600} />
```

**2. `Component.extend()` in `src/components/[Name]/theme.js`** — for defaults that
apply to every instance. Assembled in `src/theme.js`.

**3. CSS Module with `var(--mantine-*)` only** — only when Mantine props cannot express
the rule (pseudo-elements, `[data-*]` selectors, complex `:hover` targets).

### Never use

```jsx
<Box style={{ borderRadius: 8, color: '#3e63dd' }} />   // ❌ always forbidden
<NavLink styles={{ label: { fontWeight: 600 } }} />      // ❌ move to Component.extend()
```

Accepted exceptions for `styles={{}}`: Mantine `Transition` spreads, and structural
shell slots (AppShell, Drawer) where `classNames` loses to Mantine's own specificity —
with constants defined at the top of the file, never raw values inline.

### CSS Modules — never hardcode design values

Every spacing, colour, font size, radius and line height references a Mantine variable
so the theme stays the single source of truth.

```css
/* ❌ */ .label { font-size: 12px; color: #3e63dd; }
/* ✅ */ .label { font-size: var(--mantine-font-size-xs); color: var(--mantine-color-indigo-8); }
```

## Colour scheme — palette steps are constant, semantic tokens are not

This is the rule `webapp_v2` does not have, and the reason its dark mode is broken.

Mantine has exactly **16 scheme-dependent variables**. Everything else, including every
numbered palette step, is the same value in light and dark:

```
--mantine-color-text          --mantine-color-default
--mantine-color-body          --mantine-color-default-hover
--mantine-color-dimmed        --mantine-color-default-color
--mantine-color-bright        --mantine-color-default-border
--mantine-color-placeholder   --mantine-color-disabled
--mantine-color-anchor        --mantine-color-disabled-color
--mantine-color-error         --mantine-color-disabled-border
--mantine-color-scheme        --mantine-primary-color-contrast
```

So `background: var(--mantine-color-gray-0)` freezes a surface at its light value
forever, and `rgba(0, 0, 0, .05)` as a hover tint is invisible on a dark one.

**For surfaces, borders and text, use the semantic tokens.** When the hoop value differs
from what Mantine derives, override it in the `light` bucket of `cssVariablesResolver`
in `src/theme.js` — that is how `--mantine-color-default-border` carries the hoop
hairline while still following the scheme. Numbered steps are for accents and decoration
that are meant to be the same in both schemes.

Dark mode is not shipped. What is shipped is the discipline that makes it possible
later: the `dark: {}` bucket in `src/theme.js` is where the overrides will go, and
components reading semantic tokens follow for free. `components/Spotlight/Spotlight.module.css`
is the known exception — its three-step hover progression needs a dark ramp first.

## Text colour

- Secondary text: `c="dimmed"` (slate11 `#60646c`, ~5.7:1 on white, passes WCAG AA).
- Body text: omit the prop and inherit `var(--mantine-color-text)`. Never `c="dark"` or
  `c="light"` — those palettes are not defined and fall back to Mantine defaults.
  `gray.9` is `#4d4d60`, a mid slate, not near-black.
- On the `--brand-navy` surfaces, never use the gray scale — it is calibrated for light
  backgrounds. Use white colour-mix tones (see `EnterpriseBanner.module.css`).

## CSS layers

Mantine loads through `@mantine/core/styles.layer.css`, so its component CSS sits in
`@layer mantine` and CSS Modules — which stay outside any layer — win deterministically.
You never need `!important` or doubled selectors to beat a Mantine slot.

Do not switch to `styles.css` without `.layer`; that reintroduces the cascade race.

## Assets

Everything referenced by `/images`, `/icons` or `/data` must exist in `public/`. There
is no asset proxy, and a missing file renders as a broken image with no error.

`npm run build` runs `scripts/check-assets.mjs` first, which fails the build and names
the file that referenced the missing asset. It understands the three reference styles:

| Style | Resolved from |
|---|---|
| `"/images/foo.svg"` in JS/JSX/CSS | the literal |
| `` `/icons/connections/${iconName}-default.svg` `` | the `icon-name` field of every entry in `public/data/connections-metadata.json` |
| `<FeaturePromotion image="x-promotion.png" />` | the prop at each call site |

Two of those are built at runtime and cannot be found by grepping for literals — which
is how they were missed during the extraction. **Adding a reference of a new shape means
teaching `check-assets.mjs` about it**, otherwise the guarantee quietly narrows.

## Authentication

Admin only. Local auth (email/password) and OAuth/IDP, auto-detected from the gateway.
Token in `localStorage.jwt-token`. No refresh token: a 401 saves the current URL, clears
the token and redirects to `/login`.

Key files: `stores/useAuthStore.js`, `services/auth.js`, `services/api.js`,
`components/ProtectedRoute.jsx`, `pages/Auth/`.

## Symptom — "my CSS Module does nothing on a Mantine component"

1. Is the module imported in the JSX? Vite only bundles it if there is an `import`.
2. Are classes applied via `classNames={{ slot: classes.foo }}`, not `className`?
   Internal slots ignore `className`.
3. Is the slot name right? Check the component's Styles API docs.
4. Does it use only `var(--mantine-*)` values?

## Reference implementations

- A list page with filters and a form: `pages/Guardrails/`
- A page with tabs and provider configuration: `pages/Features/AiSessionAnalyzer/`
- A page whose paths were renamed during extraction: `pages/Features/AccessRequest/`
  (Reviews rules — the paths follow the product name, the identifiers still follow the
  code, pending the initiative's naming session)
