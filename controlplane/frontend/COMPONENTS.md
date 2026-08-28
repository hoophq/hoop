# COMPONENTS.md: what already exists

The catalogue `CLAUDE.md` tells you to check before you create anything. It is the
listing plus the things the listing does not tell you — which directories are not
importable, which props are ours rather than Mantine's, and which defaults are already
set so you do not set them again at the call site.

Kept short on purpose. A catalogue nobody updates is worse than no catalogue: if you add
a component, add its row.

---

## Read this first: six directories under `src/components/` are not importable

`src/components/` is not a flat list of components. Six of its 40 directories export no
component you can import by directory name. Five hold only a `theme.js` or a
`.module.css` and configure a Mantine primitive globally; the sixth, `Snackbar/`, holds a
real component you must never import directly. Reading the directory listing as a
catalogue is exactly how you write `import Paper from '@/components/Paper'` and get
nothing.

| Directory | What it holds | How you use it |
|---|---|---|
| `AppShell/` | `theme.js` | `import { AppShell } from '@mantine/core'` — the theme applies |
| `Input/` | `theme.js`, `Input.module.css` | same, for every input primitive |
| `Paper/` | `theme.js`, `Paper.module.css` | `import { Paper } from '@mantine/core'` |
| `Pill/` | `theme.js` | `import { Pill } from '@mantine/core'` |
| `Spotlight/` | `theme.js`, `Spotlight.module.css` | through `features/CommandPalette` |
| `Snackbar/` | `Toast.jsx`, `Toast.module.css` | never directly — call `showSnackbar` from `@/utils/snackbar` |

Everything else has an `index.jsx` with a default export.

---

## Wrapper components

`src/components/<Name>/index.jsx`, default export, imported as `@/components/<Name>`.

A **passthrough** adds no props: it exists so the call site never imports the primitive
directly, which keeps one place to change it and keeps the option of dropping Mantine.
Its behaviour comes from `Component.extend()` in the matching `theme.js`.

### Passthroughs over a Mantine primitive

| Component | Wraps | Note |
|---|---|---|
| `ActionIcon` | `ActionIcon` | size defaults `md`; sizes map to `--hoop-control-height-*` |
| `Alert` | `Alert` | |
| `Button` | `Button` | size defaults `md`, 40 px scale |
| `NumberInput` | `NumberInput` | |
| `PasswordInput` | `PasswordInput` | visibility toggle |
| `Radio` | `Radio` | re-exports `Radio.Group` and `Radio.Indicator`. `Indicator` is the input-less visual — use it inside a button or card, where a real `<input>` would produce invalid nested-interactive markup |
| `Select` | `Select` | single value |
| `Switch` | `Switch` | |
| `TextInput` | `TextInput` | |
| `Tabs` | `Tabs` | plus one hover-background override |

### Wrappers with their own props or defaults

| Component | Wraps | Own props / defaults |
|---|---|---|
| `Badge` | `Badge` | `variant` is **semantic**: `active` (green filled), `inactive` (gray outline), `warning` (yellow filled), `danger` (red filled). Any other value falls through to Mantine. `size="sm" radius="sm"` by default. See the trap below. |
| `Modal` | `Modal` | `size` defaults `md` |
| `MultiSelect` | `MultiSelect` | own CSS module for the pill row |
| `TagsInput` | `TagsInput` | creatable chips, Enter commits |
| `Textarea` | `Textarea` | autosize between 2 and 6 rows |
| `Tooltip` | `Tooltip` | dark background with an arrow, by default |
| `Table` | `Table` | surface style; re-exports `Thead`, `Tbody`, `Tfoot`, `Tr`, `Th`, `Td` and `Caption`, so those come from here and never from Mantine. `ScrollContainer` and `DataRenderer` are **not** re-exported — `<Table.ScrollContainer>` renders `undefined` and React throws |
| `CopyButton` | `CopyButton` + `ActionIcon` + `Tooltip` | `value`, `label`. Honours `disableClipboard` from `useUserStore` |
| `CodeSnippet` | `Box` | `code`, `variant` — `black` (default) or `gray`. Copy button included |

### Composites — no single primitive underneath

| Component | What it is |
|---|---|
| `ActionMenu` | Row / card / header dropdown. Exports `ActionMenu.Item`, which takes `danger`. |
| `PaginatedMultiSelect` | Multi-select over a paginated, server-searched source, with infinite scroll. The base every connection picker builds on. |
| `ConnectionsMultiSelect` | Resource-role picker keyed by **id**. |
| `ConnectionNamesMultiSelect` | Resource-role picker keyed by **name**. Two exist because the APIs disagree — pick by what the endpoint takes. |
| `ValueFilter` | Single-value filter dropdown over a static list. |
| `AsyncValueFilter` | The same over a paginated, server-searched source. Has the CSS module `ValueFilter` lacks. |
| `SelectionCard` | Selectable card: glyph, title, optional description. Dark-surface variant. |
| `RuleTableControls` | New / Select / Select all / Delete, shared by every rule table. `disableNew` blocks creation on the free plan. |
| `Table` sub-parts | see above |
| `PageLoader` | Loader with `message`, `description`, `error`, `overlay`, `h`. Height defaults to `calc(100dvh - var(--app-shell-header-offset, 0rem))`, so it fits inside `AppShell.Main` and falls back to a full viewport outside the shell. |
| `AuthPageLoader` | Full-screen loader for the auth routes. Always dark — auth screens are. |
| `NotImplemented` | A route that exists in the IA with no backend. Takes `title`, `project`, `missing[]`. Renders nothing that could be mistaken for loaded state — no empty table, no zero counter, no spinner. **Use this instead of a stub page.** |
| `FeaturePromotion` | Split panel, copy and highlights left, illustration right. `image` names a file under `public/images/illustrations/`. |
| `EnterpriseBanner` | Dark upsell banner for free-plan users on a feature page. |
| `FreeLicenseCallout` | Inline callout on a gated feature page. `message`, `variant`. |
| `DocsBtnCallOut` | Bordered `text` + `href` link out to the docs. |
| `ProtectedRoute` | Not a directory — `src/components/ProtectedRoute.jsx`. Route guard: auth, `adminOnly`, `licenseFeature`. |

### Traps that do not raise an error

- **`<Badge variant="danger" color="indigo">` renders red.** A semantic `variant` carries
  its own colour, so a `color` prop alongside it is silently ignored.
- `AsyncValueFilter` and `ValueFilter` look interchangeable and are not: one takes a
  loader, the other an array.
- `ConnectionsMultiSelect` and `ConnectionNamesMultiSelect` differ only in the key they
  emit. Sending ids to a name-keyed endpoint fails at the API, not at the call site.

---

## Theme

`src/theme.js` is the single theme file. It exports `cssVariablesResolver` and `theme`,
and assembles the per-component objects. `src/main.jsx` mounts both on
`<MantineProvider defaultColorScheme="light">`.

### Custom variables

| Variable | Value | For |
|---|---|---|
| `--brand-navy` | `#1F2D5C` | Dark brand surfaces: auth, `SelectionCard`, `EnterpriseBanner`, `AuthPageLoader`. A fixed brand value, not a scheme token. |
| `--hoop-surface-raised` | `#ffffff` | A raised surface. `theme.white` is `#FCFCFD`, the same as the page, so it cannot separate the two. |
| `--hoop-control-height-{xs,sm,md,lg}` | 24 / 32 / 40 / 48 px | The single source of the control scale. Read by the Button, ActionIcon and Input resolvers. |

Overridden in the `light` bucket: `--mantine-color-body`, `--mantine-color-text`,
`--mantine-color-dimmed`, `--mantine-color-placeholder`, and
`--mantine-color-default-border` (set to `gray[2]`, the hoop hairline — Mantine would
derive a much heavier `gray[4]`).

The `dark` bucket is empty. Dark mode is not shipped; the discipline that makes it
possible later is. See `CLAUDE.md`, "Colour scheme".

### Scales

`primaryColor: indigo`, `primaryShade: 5`, `defaultRadius: md`.

- **spacing** adds `xsAlt` 4, `smAlt` 8, `mdAlt` 16, `lgAlt` 24, `xlAlt` 32, `xxlAlt` 48,
  `xxxlAlt` 64 **alongside** Mantine's own `xs…xl`. There is no `xxl` — `xxlAlt` is the
  48 px step, and dropping the suffix silently renders nothing.
- **radius** xs 4.5 / sm 6 / md 9 / lg 12 / xl 18 px.
- **fontSizes** 12 / 14 / 16 / 18 / 20 px. **lineHeights** 1.4 → 1.6.
- **headings** h1 36/700 … h6 14/500.
- **colors** custom `gray` and `indigo` ramps, plus `amber` → `yellow` and `sky` → `cyan`
  aliases. Mantine ships neither, and both are named on `Alert` and `Badge` call sites;
  unaliased they lose their colour with no warning.

### Component themes

Seven `theme.js` files, all registered in `theme.components`:
`ActionIcon`, `AppShell`, `Button`, `Input` (which exports `InputTheme`, `InputBaseTheme`,
`InputWrapperTheme`, `TextareaTheme`, `MultiSelectTheme`, `TagsInputTheme`,
`PillsInputTheme`), `Paper`, `Pill`, `Spotlight`. Plus an inline
`PickerInputBase: { defaultProps: { size: 'md' } }`.

---

## Layout — `src/layout/`

The app shell. Not reusable pieces; there is one of each.

| File | What it is |
|---|---|
| `Layout.jsx` | The `AppShell`: Sidebar, Header, main. |
| `PageLayout.jsx` | The per-page container inside `Layout`. |
| `FullBleed.jsx` | Escape hatch for a page that must ignore `PageLayout`'s padding. |
| `SkipLink.jsx` | Skip-to-content, for keyboard users. |
| `Header/` | `index.jsx`, `HeaderSearch.jsx`, `UserAvatar.jsx`, `UserMenu.jsx`. |
| `Sidebar/` | `index.jsx` plus `SidebarExpanded`, `SidebarCollapsed`, `SidebarNavLink`, `NavItem`, `IconBtn`, `ItemBadge`, and `helpers.js` — `shouldHide` (gating) and `isActive` (active route). `features/CommandPalette/MainPage.jsx` imports `shouldHide` from here; do not write a second copy. |
| `Sidebar/constants.js` | **The navigation.** `MAIN_ITEMS`, `REVIEW_ITEMS`, `FEATURE_ITEMS`, `ORGANIZATION_ITEMS`. Every path must be claimed by `Router.jsx`. |
| `EmptyState/index.jsx` | The empty state for a list page. |
| `LicenseBanner/index.jsx` | Expired / invalid licence warning. Read-only; its action opens support. |

## Features — `src/features/`

| Feature | Files |
|---|---|
| `CommandPalette/` | `index.jsx`, `CommandPaletteRoot.jsx`, `MainPage.jsx`, `spotlight.js`, `constants.js`. The gating predicate is **shared**: `MainPage.jsx` imports `shouldHide` from `layout/Sidebar/helpers`. The action list in `constants.js` is its own, and every target must be a route `Router.jsx` claims. |
| `OriginSurvey/` | `index.jsx`, `constants.js`. One-time "how did you hear about us". |

## Stores — `src/stores/`

Zustand. Global state only; page state goes in `pages/<Page>/store.js`.

| Store | Holds |
|---|---|
| `useAuthStore` | `token`, `isAuthenticated`, `initializeToken`, `setToken`, `logout`, `saveRedirectUrl`, `getAndClearRedirectUrl`. |
| `useUserStore` | The big one: `user`, `isAdmin`, `isSelfHosted`, `isFreeLicense`, `featureFlags`, `licenseFeatures`, `licenseInfo`, `gatewayVersion`, `redactProvider`, `disableClipboard`, analytics and Intercom, plus `isFeatureFlagEnabled` and `isLicenseFeatureEnabled`. |
| `useUIStore` | `sidebarOpen`, `sidebarCollapsed` and their setters. |
| `useConnectionsMetadataStore` | The catalogue from `public/data/connections-metadata.json`. Loaded once in `App.jsx`. Backs `getIconName`, so the icon path is built at runtime — see `CLAUDE.md`, "Assets". |

Outside React: `useStore.getState()`.

## Services — `src/services/`

One axios instance, one file per domain, each exporting a `<name>Service` object.
Services return promises; stores handle the response.

`api.js` is the instance: `VITE_API_URL` or `${origin}/api`, bearer token from
`useAuthStore`, and a 401 handler that saves the current URL, clears the token and
redirects to `/login`.

`accessRequests`, `aiSessionAnalyzer`, `attributes`, `auth`, `connections`,
`connectionsMetadata`, `dataMasking`, `featureFlags`, `guardrails`, `originSurvey`,
`plugins`, `userGroups`, `users`. Plus `analytics.js`, which exports `identify` rather
than a service object.

**Every domain service here calls the gateway on `:8009`** — there is no Control Plane
backend on `main` — with two exceptions: `connectionsMetadata` fetches the static
`/data/connections-metadata.json` with no axios and no auth, and `analytics.js` talks to
Segment.

## Hooks — `src/hooks/`

| Hook | What it does |
|---|---|
| `usePaginatedConnections({ pageSize })` | Paginated, server-searched connections. Backs all three connection pickers. |
| `useMinDelay(isLoading, delay)` | Holds a loading state open for a minimum time so a fast response does not flash. |

## Utils — `src/utils/`

| File | Exports |
|---|---|
| `snackbar.jsx` | `showSnackbar({ level, text, description, details })`. **The only way to raise a toast.** `@mantine/notifications` is deliberately absent. |
| `docsUrl.js` | `docsUrl`, the documentation link map. New links come from the Sidecar / Control Plane trunk — see [`../PRODUCT.md`](../PRODUCT.md#link-index). |
| `license.js` | `LICENSE_STATUS`, `formatLicenseDate`, `daysUntilExpiration`. |
| `user.js` | `getUserInitials`, `getUserDisplayName`. |
| `support.js` | `openSupport(message)`, `GITHUB_DISCUSSIONS_URL`. |
| `rowOps.js` | `makeRowOps` — add / remove / update for the rule tables. |
| `connectionIcons.js` | `getConnectionIcon`, `useConnectionIconGetter`. |
| `connectionsMetadataMapper.js` | `jsonCredentialToField`. |
| `connectionPolicy.js` | `canAccessNativeClient`, `canOpenWebTerminal`, `canHoopCli`. Gateway-era predicates; nothing in this app reaches a resource. |

---

## Where a new component goes

| Where | What |
|---|---|
| `pages/<Page>/<X>.jsx` | A tab body or top-level slice of the page |
| `pages/<Page>/sections/` | Decomposition, single consumer, not reusable |
| `pages/<Page>/components/` | Reusable within the page (2 consumers or more) |
| `src/components/` | Reusable across the app |
| `src/layout/` | The app shell |
| `src/features/` | A multi-component feature |

A component whose styles are hard-coded for one context is not reusable even if it wraps
a generic Mantine component. Keep it next to the context it serves.
