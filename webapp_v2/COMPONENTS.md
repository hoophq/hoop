# Component Catalog

## Strategy — We own every component

App code **never imports UI primitives directly from Mantine**. Every primitive (Table, Button, TextInput, Badge, …) is wrapped in `src/components/` before being used in pages or layout. This makes the visual layer centrally owned, easy to audit, and Storybook-ready.

**Rule of thumb:** if a component doesn't have a wrapper in this catalog yet, create one before using it in more than one place.

Before creating a new component, check this list. Re-use what already exists.

## Control size scale — Button, ActionIcon, and every input

All interactive controls share one 4-step height scale, owned by the theme
(`--hoop-control-height-*` in `src/theme.js` `cssVariablesResolver`, applied by
the vars resolvers in `src/components/{Button,ActionIcon,Input}/theme.js`).
Text size pairs with height: 24px↔12px text, 32px↔14, 40px↔16, 48px↔18.

| `size` | Height | When |
|---|---|---|
| *(none)* / `md` | 40px | **Default — do not pass a size prop** |
| `xs` | 24px | Micro affordances in dense chrome |
| `sm` | 32px | Compact contexts: table row-action buttons, icon buttons in tight slots (e.g. an input `rightSection`). Inputs stay at the default — no small fields in the app. |
| `lg` | 48px | Prominent/hero actions |

Rules:
- **Never pass `size` for the regular 40px control** — the theme default handles it.
- `xs`, `sm`, and `lg` are the only variants. Do not use `xl` or numeric sizes on
  Button, ActionIcon, or input-family components (`compact-*` on Button is allowed —
  it's a separate inline-button axis, not a height override).
- **Never pass `radius`** on these components — `defaultRadius: 'md'` (9px) applies.
- Row-action `ActionIcon`s next to inputs need no size prop: both default to 40px
  and align automatically.

---

## Reusable Components (`src/components/`)

### `PageLoader`
Full-page or contained loading state with optional error display.
```jsx
import PageLoader from '@/components/PageLoader'

<PageLoader />                    // centered spinner, default height
<PageLoader h={400} />           // fixed height container
<PageLoader overlay />            // fixed full-screen overlay
<PageLoader error="message" />   // error state with icon
```
Use with `useMinDelay` to prevent flash on fast requests.

### `AuthPageLoader`
Full-screen dark loading state for auth-flow routes (login redirect, OAuth callbacks, session verification). Auth screens are always dark — there is no light variant — so the dark styling is baked in. For loaders inside the (light) app shell, use `PageLoader`.
```jsx
import AuthPageLoader from '@/components/AuthPageLoader'

<AuthPageLoader message="Verifying authentication..." />
<AuthPageLoader
  error
  message="Authentication failed"
  description="Redirecting to login..."
/>
```

### `EmptyState` (`src/layout/EmptyState/`)
Empty list / zero-data state: illustration, title, description, and optional CTA.
```jsx
import EmptyState from '@/layout/EmptyState'

<EmptyState
  title="No agents yet"
  description="Set up your first agent to connect resources."
  action={{ label: 'Setup new Agent', onClick: () => navigate('/agents/new') }}
/>

// Empty result inside a page that already renders its own header and filters
<EmptyState
  compact
  title="No Guardrails match your filters"
  description="Try clearing the filter above."
/>
```
`action` is optional — omit when the user has no permission to create.
`compact` tightens the vertical space (drops the 50vh floor, smaller padding
and gap; the illustration is unchanged) for an empty result rendered below a
page header, callout or filter bar rather than as the whole screen.
Optional `docsUrl` + `docsLabel` append a documentation line.

### `CodeSnippet`
Scrollable code block with copy-to-clipboard button. `variant` accepts `'black'` (default, terminal look) or `'gray'` (light surface).
```jsx
import CodeSnippet from '@/components/CodeSnippet'

<CodeSnippet code="docker run ..." />
<CodeSnippet code={mcpConfigJson} variant="gray" />
```

### `Table`
Surface-style table — matches Radix `Table.Root variant="surface"` from the legacy webapp. Re-exports all sub-components so call sites never import from Mantine directly.
```jsx
import Table from '@/components/Table'

// Standard table with column headers
<Table>
  <Table.Thead>
    <Table.Tr>
      <Table.Th>Name</Table.Th>
      <Table.Th>Status</Table.Th>
    </Table.Tr>
  </Table.Thead>
  <Table.Tbody>
    <Table.Tr>
      <Table.Td>agent-1</Table.Td>
      <Table.Td>Online</Table.Td>
    </Table.Tr>
  </Table.Tbody>
</Table>

// Key-value table (no thead — use Table.Th as row labels)
<Table>
  <Table.Tbody>
    <Table.Tr>
      <Table.Th w="30%">Hostname</Table.Th>
      <Table.Td>app.hoop.dev</Table.Td>
    </Table.Tr>
  </Table.Tbody>
</Table>
```
Styles: light chrome — `1px solid gray.1` outer border with `border-radius`, `gray.1` row separators, `verticalSpacing="sm"` / `horizontalSpacing="md"`. Striping is opt-in: pass `striped` and rows alternate with `gray.0`. Defined in `components/Table/index.jsx` + `Table.module.css`.

> **Note on `Table.Th`**: The wrapper's CSS already sets `font-size: sm` and `font-weight: 700` on all `th` cells. Do not wrap the content in `<Text>` — it's redundant.

### `DocsBtnCallOut`
Bordered link to external documentation. Equivalent of `webapp.components.callout-link` in CLJS.
```jsx
import DocsBtnCallOut from '@/components/DocsBtnCallOut'

<DocsBtnCallOut href="https://hoop.dev/docs/..." text="Learn more about gRPC" />
```
Props: `href` (string), `text` (string).

### `MethodCard`
Selectable card for picking an installation/deployment method (icon + label + description).
```jsx
import MethodCard from '@/components/MethodCard'

<MethodCard
  icon={Docker}
  label="Docker"
  description="Run the agent as a Docker container"
  selected={installMethod === 'docker'}
  onClick={() => setInstallMethod('docker')}
/>
```

### `SelectionCard`
Selectable card for picking a single option from a group (lucide icon + title + description). Use for mutually exclusive choices like the analytics privacy mode in Settings → Infrastructure.
```jsx
import SelectionCard from '@/components/SelectionCard'
import { BarChart3 } from 'lucide-react'

<SelectionCard
  icon={BarChart3}
  title="Identified"
  description="Share usage data including identified events."
  selected={mode === 'identified'}
  onClick={() => setMode('identified')}
/>
```
Differs from `MethodCard` by accepting a lucide icon component instead of image sources, and rendering the icon in a `ThemeIcon` rather than an `Avatar`. Pass `disabled` when the choice is locked (Hoop-managed access request rules cannot change their access type) — the card dims, stops responding to hover and no longer fires `onClick`, while a selected one keeps its accent fill so it still reads as the current choice.

### `RingProgress`
Compact progress ring with a built-in percentage label, sized for inline/sidebar use (e.g. the Config Status checklist). `value` is 0–100. Arc color defaults to `indigo.5` (the Figma main accent), track to `gray.2`.
```jsx
import RingProgress from '@/components/RingProgress'

<RingProgress value={33} />                    // 32px ring, "33%" label
<RingProgress value={80} size={48} label={<Text fz="xs">4/5</Text>} />
```

### `BarChart`
Bar chart, wrapping `@mantine/charts` (recharts). Carries app-wide defaults only — `gridAxis="none"`, `withLegend={false}`, `tickLine="none"`, and a `valueFormatter` that adds thousands separators. Everything chart-specific stays at the call site, because two charts on the same page can need opposite configurations.
```jsx
import BarChart from '@/components/BarChart'

// Grouped bars, no axes (identification via tooltip):
<BarChart
  h={300}
  data={buckets}
  dataKey="label"
  withXAxis={false}
  withYAxis={false}
  barProps={{ radius: 4 }}
  series={[
    { name: 'approved', label: 'Approved', color: 'green.5' },
    { name: 'rejected', label: 'Rejected', color: 'red.5' },
  ]}
/>
```
Note Mantine colors **per series**, not per data point. `getBarColor` only receives the numeric value, so it cannot map a category to a color — if you need one color per bar, either show the category on the x-axis with a single series, or reach for `barProps.shape` and accept that the tooltip swatch will still use the series color.

### `DonutChart`
Donut/pie chart, wrapping `@mantine/charts`. `data` is `[{ name, value, color }]`, so colors are per slice — pull them from `CHART_SERIES_COLORS` and cycle with `i % length`. Defaults to `withLabels={false}` and `tooltipDataSource="segment"` (Mantine's default lists every slice at once).
```jsx
import DonutChart from '@/components/DonutChart'

<DonutChart data={slices} size={240} thickness={60} strokeWidth={5} />
```
Radii are derived: `outerRadius = size / 2`, `innerRadius = size / 2 - thickness`.

### `CHART_SERIES_COLORS` (theme export, not a component)
The categorical palette for chart series and slices, exported from `src/theme.js`. Entries are theme color references (`'indigo.6'`, `'green.5'`, …) so the theme stays the single source of truth.
```jsx
import { CHART_SERIES_COLORS } from '@/theme'

const color = CHART_SERIES_COLORS[index % CHART_SERIES_COLORS.length]
```
**Always cycle with `%`** — a category list longer than the palette would otherwise resolve to `undefined` and render black. Shades 0–1 are deliberately absent: they are invisible against `--mantine-color-body`.

### `SegmentedControl`
Segmented control with a **locked item** concept for gated options: visible and hoverable, but not selectable.
```jsx
import SegmentedControl from '@/components/SegmentedControl'

<SegmentedControl
  size="xs"
  value={range}
  onChange={setRange}
  lockedTooltip="Available on Enterprise plan only."
  data={[
    { value: '1', label: '24h', locked: isFreeLicense },
    { value: '7', label: '7d' },
  ]}
/>
```
Locked items render dimmed, show `lockedTooltip` on hover, and are dropped in `onChange`. Do **not** reach for Mantine's `disabled` instead: it sets `pointer-events: none`, which kills hover, so the tooltip would never open and the user would see a greyed-out option with no explanation.

### `StepAccordion`
Multi-step accordion that mirrors the CLJS wizard pattern.
```jsx
import StepAccordion from '@/components/StepAccordion'

<StepAccordion
  steps={[
    { id: 'info', title: 'Agent information', subtitle: 'Name your agent', done: created, content: <FormStep /> },
    { id: 'install', title: 'Installation', disabled: !created, content: <InstallStep /> },
  ]}
  activeStep={activeAccordion}
  onChange={setActiveAccordion}
/>
```

### `ProtectedRoute`
Route guard — checks auth, fetches user, handles onboarding redirect. Already wrapping all routes in `Router.jsx`. Do not add another instance.

### `ClojureApp`
Bridge component that mounts the CLJS bundle for un-migrated routes. Only used in `Router.jsx` as the `/*` catch-all. Do not use elsewhere.

### `Input` (theme-level, no wrapper)
Global resting border color **and height scale** for every Input-based component (`TextInput`, `Select`, `Textarea`, `MultiSelect`, `DatePickerInput`, …) via `Input.extend()` in `src/components/Input/theme.js` (registered in `src/theme.js`). Fields default to `size="md"` = 40px with xs=24 / sm=32 / lg=48 variants — see "Control size scale" above. The border is `--input-bd`, which Mantine declares per variant directly on the input wrapper element — a `:root` override from `cssVariablesResolver` never reaches it, so the extension applies a co-located CSS Module rule on the wrapper instead. Scoped to `[data-variant='default']:not([data-error])` so filled/unstyled variants, the error state, and the focus swap keep Mantine's behavior. To change the app-wide input border, edit `src/components/Input/Input.module.css` — do not add border variables to `cssVariablesResolver`.

### `Paper` (theme-level, no wrapper)
Global border color for `Paper` — and `Card`, which renders through Paper — via `Paper.extend()` in `src/components/Paper/theme.js` (registered in `src/theme.js`). Same story as `Input`: Mantine declares `--paper-border-color` per color scheme directly on the Paper root, out of reach of `cssVariablesResolver`, so a co-located CSS Module rule overrides it on the element. Only visible on instances using `withBorder`; the value matches the app-wide input resting border. To change it, edit `src/components/Paper/Paper.module.css`.

### `Pill` (theme-level, no wrapper)
Chips are styled globally via `Pill.extend()` in `src/components/Pill/theme.js` (registered in `src/theme.js`): fully rounded, Figma neutral background `rgba(0,0,51,0.06)`, `#60646c` text. Every Mantine component that renders pills — `MultiSelect`, `TagsInput`, `PillsInput` compositions — inherits it automatically, matching the legacy webapp's react-select chips. Variant pills (e.g. the managed protection-profile pill) override per instance with `bg`/`c` style props.

### `Badge`
Semantic status badge. Use the `variant` shorthand to express meaning; falls back to standard Mantine props otherwise.
```jsx
import Badge from '@/components/Badge'

<Badge variant="active">Active</Badge>       // green filled
<Badge variant="inactive">Deactivated</Badge> // gray outline
<Badge variant="warning">Reviewing</Badge>   // yellow filled
<Badge variant="danger">Failed</Badge>       // red filled
// Standard Mantine props also work:
<Badge color="indigo" variant="outline">Custom</Badge>
```

### `ActionMenu`
Dropdown action menu for table rows and cards. Uses a `MoreHorizontal` icon trigger.
```jsx
import ActionMenu from '@/components/ActionMenu'

<ActionMenu>
  <ActionMenu.Item onClick={() => navigate('/configure')}>Configure</ActionMenu.Item>
  <ActionMenu.Divider />
  <ActionMenu.Item danger onClick={handleDelete}>Delete</ActionMenu.Item>
</ActionMenu>
```
Props on `ActionMenu.Item`: `onClick`, `disabled`, `danger` (red color). Everything else
(`leftSection`, `id`, ...) is forwarded to Mantine's `Menu.Item`.

Props on `ActionMenu`: `target` (replaces the default kebab trigger — must forward a ref),
`width` (default 180), `position` (default `bottom-end`), `disabled`. `ActionMenu.Label`
and `ActionMenu.Divider` are re-exported. The header's user menu is the `target` use case.

> No `Drawer` wrapper exists, deliberately. The two drawers in the app (the mobile sidebar
> in `layout/Layout.jsx` and the Native Connections drawer) want opposite geometry on every
> axis, so a shared wrapper would be a pass-through with two mutually exclusive prop
> bundles. Revisit if a third drawer appears.

### `Modal`
Application modal dialog wrapping Mantine `Modal` with centered + radius defaults.
```jsx
import Modal from '@/components/Modal'
import { useDisclosure } from '@mantine/hooks'

const [opened, { open, close }] = useDisclosure(false)
<Modal opened={opened} onClose={close} title="Add User" size="lg">
  {/* form content */}
</Modal>
```

### `Select`
Single-value select input.
```jsx
import Select from '@/components/Select'

<Select
  label="Status"
  data={[{ value: 'active', label: 'Active' }, { value: 'inactive', label: 'Inactive' }]}
  value={status}
  onChange={setStatus}
/>
```

### `MultiSelect`
Multi-value select input, commonly used for groups and connection lists.
```jsx
import MultiSelect from '@/components/MultiSelect'

<MultiSelect
  label="Groups"
  data={groupOptions}
  value={selectedGroups}
  onChange={setSelectedGroups}
  searchable
  clearable
/>
```

### `Radio`
Radio input. Re-exports `Radio.Group` and `Radio.Indicator` so call sites never import from Mantine directly. `Radio.Indicator` renders the radio visual without an `<input>` — use it inside buttons/cards (e.g. selectable option cards) where a nested input would be invalid markup.
```jsx
import Radio from '@/components/Radio'

<Radio.Group value={value} onChange={setValue} label="Mode">
  <Radio value="a" label="Option A" />
  <Radio value="b" label="Option B" />
</Radio.Group>

// Inside a selectable card (no real input):
<UnstyledButton role="radio" aria-checked={selected} onClick={onSelect}>
  <Radio.Indicator checked={selected} size="sm" />
</UnstyledButton>
```

### `Switch`
Toggle switch for boolean settings.
```jsx
import Switch from '@/components/Switch'

<Switch label="Enable integration" checked={enabled} onChange={(e) => setEnabled(e.currentTarget.checked)} />
```

### `TextInput`
Standard text input field.
```jsx
import TextInput from '@/components/TextInput'

<TextInput label="Name" placeholder="e.g. my-key" value={name} onChange={(e) => setName(e.currentTarget.value)} />
```

### `SourcedInput`
Input paired with an optional credential source picker (Manual / Vault KV / AWS Secrets Manager / AWS IAM Role) glued to its left. The picker carries the seam border so the two components meet at a single shared edge and read as one control. When `sources` is empty or has a single entry, only the input renders.
```jsx
import SourcedInput from '@/components/SourcedInput'

<SourcedInput
  label="Host"
  required
  type="password"
  value={value}
  onChange={setValue}
  source={source}
  sources={['manual-input', 'aws-secrets-manager']}
  onSourceChange={setSource}
/>

// Sizes follow the app control scale — default `md` (40px), sm/lg variants:
<SourcedInput size="sm" {...props} />
```
Supports `type="text" | "password" | "textarea"` and `size="sm" | "md" | "lg"` (default `md`, the app-wide control default). Heights track the `--hoop-control-height-*` variables so a SourcedInput lines up with a TextInput of the same size on the same form. Textareas render the picker stacked above instead of inline — multi-line + horizontal picker doesn't read cleanly.

### `PasswordInput`
Password / secret input with visibility toggle.
```jsx
import PasswordInput from '@/components/PasswordInput'

<PasswordInput label="API Token" value={token} onChange={(e) => setToken(e.currentTarget.value)} />
// Read-only (for generated passwords):
<PasswordInput value={password} readOnly />
```

### `Pagination`
Page-based pagination control.
```jsx
import Pagination from '@/components/Pagination'

<Pagination total={totalPages} value={page} onChange={setPage} />
```

### `Accordion`
Expandable accordion. Re-exports sub-components so call sites never import from Mantine.
```jsx
import Accordion from '@/components/Accordion'

<Accordion>
  <Accordion.Item value="details">
    <Accordion.Control>Show details</Accordion.Control>
    <Accordion.Panel>Expanded content here</Accordion.Panel>
  </Accordion.Item>
</Accordion>
```

### `CopyButton`
Icon button that copies `value` to the clipboard. Shows a checkmark for 2 seconds after copying.
```jsx
import CopyButton from '@/components/CopyButton'

<CopyButton value="secret-key-here" />
<CopyButton value={key} label="Copy API Key" />
// Inside an input rightSection (~38px slot), use the small variant:
<TextInput rightSection={<CopyButton value={key} size="sm" />} />
```

### `DatePickerInput`
Date or date range picker. Requires `@mantine/dates` (already installed). Styles imported in `main.jsx`.
```jsx
import DatePickerInput from '@/components/DatePickerInput'

// Single date:
<DatePickerInput label="Start date" value={date} onChange={setDate} />
// Date range:
<DatePickerInput type="range" label="Period" value={[start, end]} onChange={setRange} w={220} />
```

### `FreeLicenseCallout`
In-page callout for free-license users on Enterprise-gated feature pages. React equivalent of `webapp.shared-ui.free-license-banner` from the CLJS app. The callout's "Contact our Sales team" link opens Intercom when analytics tracking is enabled, otherwise opens `https://hoop.dev/meet` in a new tab.
```jsx
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import { useUserStore } from '@/stores/useUserStore'

const isFreeLicense = useUserStore((s) => s.isFreeLicense)

{isFreeLicense && (
  <FreeLicenseCallout message="Applying rulepacks to connections is an Enterprise feature." />
)}

// Hard limit reached — red variant
<FreeLicenseCallout variant="limit" message="You reached the free-plan limit." />
```
Props: `message` (string), `variant` (`'info'` | `'limit'`, default `'info'`). Always gate the render on `useUserStore.isFreeLicense` at the call site so it disappears for Enterprise users.

### `EnterpriseBanner`
Dark-navy enterprise upsell banner pinned to feature pages for free-plan users (activation journey). React counterpart of the CLJS `webapp.features.activation-journey.views.enterprise-banner`, sharing the same visual (`--brand-navy`, the brand's dark navy). The built-in "Talk to Sales" button opens Intercom when analytics tracking is enabled (booting it first if needed, via `useUserStore.showIntercomMessage`), otherwise `https://hoop.dev/meet` in a new tab.
```jsx
import EnterpriseBanner from '@/components/EnterpriseBanner'
import { useUserStore } from '@/stores/useUserStore'

const isFreeLicense = useUserStore((s) => s.isFreeLicense)

{isFreeLicense && <EnterpriseBanner />}

// Custom copy
<EnterpriseBanner title="Protect your resource" subtitle="..." badgeLabel="Enterprise" />
```
Props (all optional): `title` (default `"Unlock all protection controls"`), `subtitle` (default `"Unlock unlimited Guardrails, Masking Rules, AI Session Analyzer, and more."`), `badgeLabel` (default `"Enterprise"`). Always gate the render on `useUserStore.isFreeLicense` at the call site. Use `FreeLicenseCallout` for inline informational/limit callouts; use this for the pinned dark upsell surface.

### `ValueFilter`
Popover-backed single-value filter dropdown — icon trigger, search input, and a scrollable list. Used for filtering tables by a single column value (resource, type, attribute, tag, …).
```jsx
import ValueFilter from '@/components/ValueFilter'
import { Rotate3d } from 'lucide-react'

<ValueFilter
  icon={Rotate3d}
  label="Resource"
  values={resourceOptions}
  selected={filters.resource}
  onSelect={(v) => setFilter('resource', v)}
  onClear={() => setFilter('resource', null)}
/>
```
Props: `icon` (lucide component), `label` (string), `values` (string[]), `selected` (string | null), `onSelect(value)`, `onClear()`.
### `Autocomplete`
Single-value combobox: free-typing input with autocompleted suggestions. Differs from `Select` in that the user can type any value (not just the ones in `data`).
```jsx
import Autocomplete from '@/components/Autocomplete'

<Autocomplete
  label="Key"
  data={['team', 'environment', 'region']}
  value={value}
  onChange={setValue}
/>
```

### `NumberInput`
Numeric input. Supports `min`, `max`, `step`, and clamping.
```jsx
import NumberInput from '@/components/NumberInput'

<NumberInput label="Approvals" min={1} value={n} onChange={setN} />
```

### `TagsInput`
Multi-tag creatable input. Each tag becomes a chip; press Enter (or any `splitChars`) to commit.
```jsx
import TagsInput from '@/components/TagsInput'

<TagsInput
  label="Command Arguments"
  value={args}
  onChange={setArgs}
  splitChars={[',']}
/>
```


---

### `AsyncValueFilter`
Async counterpart of `ValueFilter` for **paginated, server-searched** option sources (e.g. orgs with thousands of connections). Same trigger/skeleton, but the option list is fed page-by-page and infinite-scrolls (Mantine `useIntersection` sentinel). Presentational/controlled — pair it with a data hook like `usePaginatedConnections`. `onSelect` receives the full option object. An option may carry an optional `iconUrl`, rendered as a 16px image before its label — that is how the Resource Role filter shows connection-type icons.
```jsx
import AsyncValueFilter from '@/components/AsyncValueFilter'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import { Rotate3d } from 'lucide-react'

const roles = usePaginatedConnections({ pageSize: 50 })

<AsyncValueFilter
  icon={Rotate3d}
  label="Resource Role"
  placeholder="Search resource roles"
  selected={selectedRole}
  onSelect={setSelectedRole}
  onClear={() => setSelectedRole(null)}
  options={roles.options}
  loading={roles.loading}
  hasMore={roles.hasMore}
  onLoadMore={roles.loadMore}
  searchValue={roles.searchValue}
  onSearchChange={roles.setSearch}
  onOpen={roles.ensureLoaded}
/>
```
Props: `icon`, `label`, `placeholder`, `selected` (option | null), `onSelect(option)`, `onClear()`, `options` (`[{value,label,iconUrl?}]`), `loading`, `hasMore`, `onLoadMore()`, `searchValue`, `onSearchChange(term)`, `onOpen()`.

---

### `PaginatedMultiSelect`
Generic multi-select for a **paginated, server-searched** option source — built on Mantine `Combobox`/`PillsInput` with infinite scroll (`useIntersection`). Presentational/controlled (no fetching). `selectedOptions` supplies labels for already-selected values so chips render correctly even when the selection is not on the current page. For connections, use the `ConnectionsMultiSelect` wrapper below rather than wiring this directly.
Props: `label`, `placeholder`, `required`, `disabled`, `value` (ids[]), `onChange(ids)`, `options`, `selectedOptions`, `loading`, `hasMore`, `onLoadMore()`, `searchValue`, `onSearchChange(term)`, `onDropdownOpen()`.

---

### `ConnectionsMultiSelect`
Resource-role (connection) multi-select with infinite-scroll pagination + server search. Composes `usePaginatedConnections` + `PaginatedMultiSelect` and resolves labels for already-selected ids on demand via `?connection_ids=` (so edit-mode chips show names without loading every connection). This is the React port of CLJS `connections-select` — use it anywhere a feature needs a connection picker.
```jsx
import ConnectionsMultiSelect from '@/components/ConnectionsMultiSelect'

<ConnectionsMultiSelect
  value={form.connectionIds}
  onChange={(ids) => setField({ connectionIds: ids })}
/>
```
Props: `value` (ids[]), `onChange(ids)`, `label` (default "Resource Roles"), `placeholder`, `required`.

---

### `FeaturePromotion`
Split-screen promotion panel (marketing copy + feature highlights left, illustration right) shown when a feature is empty or gated. Faithful port of the CLJS generic `feature-promotion`, reused across feature migrations (Live Data Masking, Guardrails, and future Access Control / Runbooks / etc.). Wrap it in `FullBleed` to fill the screen.
```jsx
import FeaturePromotion from '@/components/FeaturePromotion'
import { FolderLock } from 'lucide-react'

<FeaturePromotion
  featureName="Live Data Masking"
  mode="empty-state"
  image="data-masking-promotion.png"
  description="Zero-config DLP policies…"
  featureItems={[{ icon: <FolderLock size={20} />, title: '…', description: '…' }]}
  onPrimaryClick={goCreate}
  primaryText="Configure Live Data Masking"
  // OR the docs/deprecation path:
  // docsHref={docsUrl.features.aiDatamasking} docsText="Go to docs" extraInformation="…"
/>
```
Props: `featureName`, `mode` ('empty-state' | 'upgrade-plan'), `image` (file under `/images/illustrations/`), `description`, `featureItems` (`[{icon, title, description}]`), `onPrimaryClick`, `primaryText`, `extraInformation`, `docsHref`, `docsText`.

---

### `RuleTableControls`
Toolbar under an editable rule table: New / Select / Select all / Delete. Port of the CLJS `rule_buttons`, shared by Jira Templates and Guardrails. Pair it with `makeRowOps` (see Utils below) — `ops.allSelected`, `ops.toggleAll`, `ops.deleteSelected` and `ops.addRow` map 1:1 onto its props. On the free plan pass `disableNew` so a second rule can't be added; selection and deletion stay available (the button is disabled, never hidden).
```jsx
import RuleTableControls from '@/components/RuleTableControls'

<RuleTableControls
  onAdd={() => ops.addRow()}
  selectMode={selectMode}
  onToggleSelectMode={() => setSelectMode((v) => !v)}
  allSelected={ops.allSelected}
  onToggleAll={ops.toggleAll}
  onDelete={ops.deleteSelected}
  disableNew={freeLicense && rows.length >= 1}
/>
```
Props: `onAdd()`, `selectMode`, `onToggleSelectMode()`, `allSelected`, `onToggleAll()`, `onDelete()`, `disableNew`.

---

### `FullBleed` (`src/layout/FullBleed/`)
Lets a page render edge-to-edge and exactly one viewport tall **inside** the padded `PageLayout` — cancels the page padding (single-sourced from `PageLayout`'s `PAGE_PADDING`) and fills the `AppShell.Main` height. Use for hero/promotion panels.
```jsx
import FullBleed from '@/layout/FullBleed'

<FullBleed><FeaturePromotion … /></FullBleed>
```

---

## Page Patterns

### Configure Role page (`pages/Roles/Configure/`)
Reference implementation for a **multi-tab edit page with write-only secrets and a sticky footer**:
- `index.jsx` orchestrates four `Tabs.Panel`s with `keepMounted` so HTML5 form validation can see required inputs even when the user is on a different tab.
- `store.js` keeps `drafts` (editable scalars/arrays) and `stagedSecrets` (Replace/Delete/New on individual credentials) separate, plus a `baseline` snapshot for diffing. `save()` PATCHes only keys that actually diverged.
- `FormFooter.jsx` is sticky via `position: sticky; bottom: 0` in a small CSS Module — the only sanctioned use of CSS Modules in this page because Mantine props can't express a directional border.
- `SecretField.jsx` implements the write-only credential UX with states `set` / `editing` / `deleted` / `new`. Always uses `SecretField` instead of a raw `PasswordInput` for any credential field — the current value is never re-displayed.
- `PredefinedFieldsCredentials.jsx` is the shared renderer driven by a static `{ key, label, required, placeholder, type }[]` schema (`utils/credentialsSchema.js`). Every fixed-schema connection type (catalog DBs, SSH, HTTP proxy, Claude Code, Kubernetes token) reuses it.
- `CustomCredentials.jsx` handles the free-form `custom` type: list existing envvars + an "Add new variable" row that stages keys with `action: 'new'`.

When migrating a similar edit page, prefer extending this pattern over rolling a new state shape.

> **`sections/AttributesSelect.jsx` (single consumer — graduation planned).**
> Combobox+PillsInput attribute picker with mixed pill styles: Hoop-managed
> protection-profile attributes render as indigo award pills (removable and
> re-addable like any other — removing one detaches the role from the
> profile on save), user attributes as plain pills. Deliberately NOT built on
> `components/MultiSelect` (Mantine's MultiSelect can't custom-render
> individual selected pills) nor on `components/PaginatedMultiSelect` (its
> contract is pagination/server-search specific, and it also only renders
> uniform pills). When the React resource-creation wizard needs the same
> control, graduate this to `src/components/` and consider extracting a
> shared PillsInput base with `PaginatedMultiSelect`.

### Settings `SectionRow`
Settings pages use a 2-column grid (description left, control right) via an inline `SectionRow` component defined per-page. Each settings page defines its own since it's not used outside that domain.

```jsx
function SectionRow({ title, description, callout, children }) {
  return (
    <Grid columns={7} gutter="xl">
      <Grid.Col span={2}>
        <Stack gap="xs">
          <Title order={4}>{title}</Title>
          <Text size="sm" c="dimmed">{description}</Text>
          {callout}  {/* optional DocsBtnCallOut */}
        </Stack>
      </Grid.Col>
      <Grid.Col span={5}>{children}</Grid.Col>
    </Grid>
  )
}
```

Reference: `pages/Settings/Infrastructure/index.jsx`.

---

## Hooks (`src/hooks/`)

### `useMinDelay(value, ms = 500)`
Returns `true` for at least `ms` milliseconds even if `value` goes `false` sooner. Prevents loading flash.
```jsx
import useMinDelay from '@/hooks/useMinDelay'

const showLoader = useMinDelay(loading, 500)
if (showLoader) return <PageLoader />
```

### `useCountdown(expireAt)`
Display-only countdown against a shared 1 Hz clock (`utils/tick.js`), which is reference-counted
so N countdowns cost one interval. Returns `{ remainingMs, label, expired }`; `label` is
`HH:MM:SS` above an hour and `MM:SS` below, or `null` when `expireAt` is null (persistent
credential). Expiry side effects belong to the owning store, not to this hook — see
`useNativeAccessStore`.
```jsx
const { label } = useCountdown(credential.expire_at)
```

### `usePaginatedConnections({ pageSize = 50 })`
Page-local paginated connection (resource role) option source with server-side search and infinite scroll — the data layer behind `ConnectionsMultiSelect` and the paginated Resource Role filter. Each call site gets independent state.
```jsx
const roles = usePaginatedConnections({ pageSize: 50 })
// roles.options, roles.items, roles.loading, roles.hasMore, roles.searchValue
// roles.setSearch(term), roles.loadMore(), roles.ensureLoaded(), roles.reset()
```
Returns `{ options ([{value,label,iconUrl}]), items, loading, hasMore, searchValue, setSearch, loadMore, ensureLoaded, reset }`. `items` is the raw connection rows behind `options`, for call sites that need more than an id and a name — filtering the picker by access mode or subtype, for instance (`pages/Features/AccessRequest/components/ResourceRolesSelect.jsx`). Only the pages loaded so far are in it. `iconUrl` is the connection-type icon (`useConnectionIconGetter`) for renderers that show it — `AsyncValueFilter` does, `PaginatedMultiSelect` ignores it. Search is debounced 300ms and only hits the server for an empty term or >2 chars.

---

## Stores (`src/stores/`)

One Zustand store per concern — **the filesystem is the source of truth**
(`ls src/stores/`); store names describe their responsibility. Check the
directory before creating a new store.

Access store state outside React (e.g., inside another store action):
```js
useAuthStore.getState().token
```

Non-obvious notes only:

- `useBridgeStore` — wraps `window.hoopDispatch` re-frame bridge calls; never
  call `hoopDispatch` from a component (rule in `CLAUDE.md` "Re-frame Interop").
  Current methods: `refreshLegacyUser()`, `syncPrimaryConnectionFromUrl()`.
  Snackbars are NOT bridged — use `showSnackbar` from `@/utils/snackbar`.
- `useNativeAccessStore` — the native-access credential lifecycle (start flow,
  request, resume after review, disconnect, revoke) plus `activeByName`, fed by
  `GET /connection-credentials`. Owns session expiry: it subscribes to
  `utils/tick.js` while any credential is bounded, so the "session expired"
  toast fires exactly once and still fires with the drawer closed. Deliberately
  does NOT persist to localStorage — the server is authoritative and the CLJS
  key (`hoop-native-client-access`, EDN) must never be touched from React.
- `useNativeConnectionsStore` — the drawer's own state: open/closed, search
  query, expanded row, and the list of natively-connectable roles. Loads the
  non-paginated `/connections` because the paginated one applies an RBAC join
  that can hide visible rows, and neither can filter on `access_mode_connect`.
- `useConfigStatusStore` — sidebar setup-checklist snapshot, admin only. One
  call to `GET /orgs/onboarding`; never derive the checks client-side. Refreshes
  on navigation, on window focus and on the `hoop:session-executed` DOM event
  from the CLJS terminal — no timers. Resets itself on logout (subscribes to
  `useAuthStore`), and every read is scoped by `forUserId`.
- `useConnectionsMetadataStore` — loaded once at app start (`App.jsx`); feeds
  credential field schemas + connection icons; `load()` is idempotent.

---

## Services (`src/services/`)

One Axios service file per API domain — **the filesystem is the source of
truth** (`ls src/services/`). Check the directory before creating a new file;
when adding one, follow the pattern in `services/agents.js`.

Non-obvious notes only:

- `api.js` — the base Axios instance (Bearer-token interceptor + 401 → saved
  URL + logout). Every other service imports it; never call axios directly.
- `analytics.js` — Segment (`identify()` only today), not a gateway API
  wrapper; the write key is a build-time define (see the env table in
  `README.md`).
- `accessRequests.js` — `/access-requests/rules`. `list()` deliberately sends no
  `page_size`: the handler defaults it to 0 and reads 0 as "no pagination", so
  every rule comes back (still inside the `{ pages, data }` envelope). Rule names
  are user-defined and travel in the path — every interpolated one is encoded.
- `attributes.js` — `list(params)` is paginated (`page`, `page_size`); the
  gateway caps `page_size` at 100. `listAll()` walks the pages and returns the
  complete array — use it for pickers that drive a full-replace write, where a
  truncated list would silently drop associations the admin never saw.
- `connections.js` — both `getConnections()` (full list — only for resolving
  every `connection_ids → name`, e.g. list displays) and
  `getConnectionsPaginated({page,pageSize,search,connectionIds})` for
  infinite-scroll dropdowns.
- `eventRouting.js` — normalizes the backend's snake_case JSON to camelCase at
  the service boundary.
- `sessions.js` — `list(params)`. **`limit` does not make the call cheap**: the
  gateway always runs an unbounded `COUNT(*)` (joined against reviews) to fill
  `total` before applying the limit, so `{ limit: 1 }` costs the same as a full
  page. Never use it as an existence probe.
- `onboarding.js` — `GET /orgs/onboarding`, admin only. The gateway computes the
  whole setup checklist in one query; backs `useConfigStatusStore`.
- `reports.js` — `/reports/sessions` takes `YYYY-MM-DD` dates only and `end_date`
  is **exclusive** (the gateway compares against midnight *starting* that day, so
  send tomorrow to include today). Never send `group_by`.
- `reviews.js` — `/reviews` returns a bare array, accepts **no query params** and
  is unbounded; fetch it once and filter client-side.
- `userGroups.js` — `list()` returns a bare string array, sorted, unioning the
  identity side (users, service accounts, API keys, AI agents) with the
  `access_control` plugin config. Empty organizations get `[]`; gateways older
  than EVL-217 answer `null`, so callers still coalesce.

---

## Utils (`src/utils/`)

Pure helpers with no React or Mantine dependency. `snackbar.jsx` is documented under Notifications below.

### `makeRowOps({ rows, setRows, factory, filterFn })`
Row operations for editable rule tables (Jira Templates, Guardrails). Rows are plain objects with a stable `id` and a `selected` flag; the table owns the array through a `setRows` state setter. Deleting the last row reseeds a blank one via `factory`, so a table is never left with nothing to type into. Pass `filterFn` only when several tables render disjoint subsets of one shared rows array — select/delete then touch only the rows that table actually shows. Pairs with `RuleTableControls`.
```jsx
import { makeRowOps } from '@/utils/rowOps'

const ops = makeRowOps({ rows, setRows, factory: createEmptyRow })
// ops.visible, ops.allSelected
// ops.patchRow(id, patch), ops.toggleSelect(id), ops.toggleAll()
// ops.deleteSelected(), ops.addRow(transform?)
<Table.Tr key={row.id}>…</Table.Tr>
```
Returns `{ visible, allSelected, patchRow, toggleSelect, toggleAll, deleteSelected, addRow }`. `addRow(transform)` applies `transform` to the fresh row before appending (used to pre-fill a type/value).

### `connectionPolicy.js`
Connection-shape rules the gateway doesn't express in `connections-metadata.json`, kept in one file so they don't scatter across pages. Each predicate takes a connection object (or `{ type, subtype }`) and mirrors a CLJS predicate — the source is cited inline next to each set.
```js
import { canOpenWebTerminal, canHoopCli, canAccessNativeClient } from '@/utils/connectionPolicy'

canOpenWebTerminal(connection) // browser terminal — what an access request of type "command" runs against
canHoopCli(connection)         // reachable with `hoop connect` (everything but custom RDP)
canAccessNativeClient(connection) // command-palette native-client flow
canTestConnection({ type, subtype }) // Test Connection action
supportsAwsIam(subtype)        // AWS IAM Role auth (postgres/mysql only)
isFreeFormCustomSubtype(subtype) // free-form credentials editor
```
`canAccessNativeClient` uses a deliberately narrower HTTP-proxy set than `canOpenWebTerminal` — the command palette has never offered the native-client flow for `mcp`/`mcpproxy`. Don't unify the two sets without deciding what that menu should show.

- `connectionCredentials.js` — native-access credentials, kept separate from
  `connections.js` (connection CRUD). `listActive()` hits
  `GET /connection-credentials`, which is secret-less; `get()` is the only call
  that returns a plaintext secret, so it runs on demand when a row is expanded.

### `license.js`
Shared by the shell's `LicenseBanner` and the license settings page.
```js
import { LICENSE_STATUS, formatLicenseDate, daysUntilExpiration } from '@/utils/license'

LICENSE_STATUS.VALID | .EXPIRED | .INVALID  // /serverinfo license_info.status
formatLicenseDate(1785333321)               // "Jul 29, 2026"
daysUntilExpiration(1785333321)             // whole days left, negative once past
```
Branch on `license_info.status`, never on a local `expire_at` comparison — the
gateway decides expiry against its own clock, which is the clock that actually
blocks sessions. `daysUntilExpiration` is only for the "expires in N days"
countdown while the status is still `valid`.

### `support.js`
Opens the same destination as "Contact support" in the header user menu.
```js
import { openSupport, GITHUB_DISCUSSIONS_URL } from '@/utils/support'

openSupport('I want to renew my hoop license')
```
Delegates to `useUserStore.showIntercomMessage`, which boots Intercom when the
app-boot init was skipped or shut down, and falls back to the public GitHub
discussions board when it is unavailable (analytics off, widget blocked). Use it
for any support entry point outside the user menu — that one already has
Intercom's launcher listener bound to its `#intercom-support-trigger` id.

---

## Notifications — `showSnackbar`

Use the `showSnackbar` helper from `@/utils/snackbar`. Rules (never
`@mantine/notifications`, single `<Toaster>` in `App.jsx`, never via the CLJS
bridge): see `CLAUDE.md` "Snackbars / Toasts".

```js
import { showSnackbar } from '@/utils/snackbar'

showSnackbar({ level: 'success', text: 'Agent deleted.' })
showSnackbar({ level: 'error',   text: 'Failed to delete agent.', description: err.message })
showSnackbar({ level: 'info',    text: 'Heads up.' })

// Error toasts can expand to show a `details` panel (object → key/value lines)
showSnackbar({
  level: 'error',
  text: 'Validation failed.',
  details: { field: 'name', reason: 'required' },
})
```

Error toasts auto-dismiss after 10 seconds (mirrors v1); other levels use sonner's
default.

---

## Icons

Always `lucide-react`. Never `@tabler/icons-react` or any other library.
```jsx
import { Trash2, Plus, Zap, TriangleAlert } from 'lucide-react'
```
