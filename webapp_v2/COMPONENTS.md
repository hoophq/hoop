# Component Catalog

## Strategy — We own every component

App code **never imports UI primitives directly from Mantine**. Every primitive (Table, Button, TextInput, Badge, …) is wrapped in `src/components/` before being used in pages or layout. This makes the visual layer centrally owned, easy to audit, and Storybook-ready.

**Rule of thumb:** if a component doesn't have a wrapper in this catalog yet, create one before using it in more than one place.

Before creating a new component, check this list. Re-use what already exists.

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

### `EmptyState` (`src/layout/EmptyState/`)
Empty list / zero-data state with icon, title, description, and optional CTA.
```jsx
import EmptyState from '@/layout/EmptyState'
import { Zap } from 'lucide-react'

<EmptyState
  icon={Zap}
  title="No agents yet"
  description="Set up your first agent to connect resources."
  action={{ label: 'Setup new Agent', onClick: () => navigate('/agents/new') }}
/>
```
`action` is optional — omit when the user has no permission to create.

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
Styles: `1px solid gray.3` border with `border-radius`, subtle gray.1 header background, row separators, `verticalSpacing="sm"` / `horizontalSpacing="md"`. Defined in `components/Table/Table.module.css`.

> **Note on `Table.Th`**: The wrapper's CSS already sets `font-size: xs` and `font-weight: 600` on all `th` cells. Do not wrap the content in `<Text size="xs">` — it's redundant.

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
Differs from `MethodCard` by accepting a lucide icon component instead of image sources, and rendering the icon in a `ThemeIcon` rather than an `Avatar`.

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
Props on `ActionMenu.Item`: `onClick`, `disabled`, `danger` (red color).

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

// Sizes match Mantine inputs — default `sm`, accepts xs/sm/md/lg/xl:
<SourcedInput size="md" {...props} />
```
Supports `type="text" | "password" | "textarea"` and `size="xs" | "sm" | "md" | "lg" | "xl"` (default `sm`, Mantine's input default). Heights track Mantine's `--input-height-*` variables so a `size="md"` SourcedInput lines up with a `size="md"` TextInput on the same form. Renders the field description through `MarkdownText` so inline `[label](url)` links work. Textareas render the picker stacked above instead of inline — multi-line + horizontal picker doesn't read cleanly.

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
<CopyButton value={key} label="Copy API Key" size="md" />
```

### `DatePickerInput`
Date or date range picker. Requires `@mantine/dates` (already installed). Styles imported in `main.jsx`.
```jsx
import DatePickerInput from '@/components/DatePickerInput'

// Single date:
<DatePickerInput label="Start date" value={date} onChange={setDate} />
// Date range:
<DatePickerInput type="range" label="Period" value={[start, end]} onChange={setRange} w={220} size="sm" />
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

### `MarkdownText`
Mantine `<Text>` drop-in that renders inline markdown links (`[label](url)`) as anchors. Used for catalog field descriptions sourced from `connections-metadata.json`, where helper text occasionally points to external docs. Only inline links are interpreted — bold, italics, lists, code, and other markdown stay verbatim.
```jsx
import MarkdownText from '@/components/MarkdownText'

<MarkdownText>{'Read more in [our docs](https://hoop.dev/docs/postgres).'}</MarkdownText>

// Override defaults like a regular Text:
<MarkdownText size="sm" c="gray.7">{description}</MarkdownText>
```
Links open in a new tab with `rel="noopener noreferrer"`. Default `size="xs" c="dimmed"` matches helper-text styling.

---

### `AsyncValueFilter`
Async counterpart of `ValueFilter` for **paginated, server-searched** option sources (e.g. orgs with thousands of connections). Same trigger/skeleton, but the option list is fed page-by-page and infinite-scrolls (Mantine `useIntersection` sentinel). Presentational/controlled — pair it with a data hook like `usePaginatedConnections`. `onSelect` receives the chosen option's **label** (so it plugs into name-based row filtering).
```jsx
import AsyncValueFilter from '@/components/AsyncValueFilter'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import { Shapes } from 'lucide-react'

const roles = usePaginatedConnections({ pageSize: 50 })

<AsyncValueFilter
  icon={Shapes}
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
Props: `icon`, `label`, `placeholder`, `selected` (label | null), `onSelect(label)`, `onClear()`, `options` (`[{value,label}]`), `loading`, `hasMore`, `onLoadMore()`, `searchValue`, `onSearchChange(term)`, `onOpen()`.

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
Split-screen promotion panel (marketing copy + feature highlights left, illustration right) shown when a feature is empty or gated. Faithful port of the CLJS generic `feature-promotion`, reused across feature migrations (Live Data Masking, and future Access Control / Guardrails / Runbooks / etc.). Wrap it in `FullBleed` to fill the screen.
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

### `usePaginatedConnections({ pageSize = 50 })`
Page-local paginated connection (resource role) option source with server-side search and infinite scroll — the data layer behind `ConnectionsMultiSelect` and the paginated Resource Role filter. Each call site gets independent state.
```jsx
const roles = usePaginatedConnections({ pageSize: 50 })
// roles.options, roles.loading, roles.hasMore, roles.searchValue
// roles.setSearch(term), roles.loadMore(), roles.ensureLoaded(), roles.reset()
```
Returns `{ options ([{value,label}]), loading, hasMore, searchValue, setSearch, loadMore, ensureLoaded, reset }`. Search is debounced 300ms and only hits the server for an empty term or >2 chars.

---

## Stores (`src/stores/`)

| Hook | Responsibility | Key state / actions |
|------|---------------|---------------------|
| `useAuthStore` | JWT token lifecycle | `token`, `setToken()`, `logout()`, `redirectUrl` |
| `useUserStore` | Current user data | `user`, `isAdmin`, `isFreeLicense`, `fetchUser()` |
| `useUIStore` | Sidebar open/collapsed | `sidebarOpened`, `toggle()`, `pendingSection` |
| `useAgentStore` | Agents CRUD | `agents`, `loading`, `error`, `agentKey`, `fetchAgents()`, `createAgent()`, `deleteAgent()` |
| `useCommandPaletteStore` | Command palette state | `page`, `context`, `searchStatus`, `results`, `search()` |

Access store state outside React (e.g., inside another store action):
```js
useAuthStore.getState().token
```

---

## Services (`src/services/`)

| File | What it wraps |
|------|--------------|
| `api.js` | Base Axios instance — adds Bearer token, handles 401 logout |
| `auth.js` | Login, register, OAuth, user info, server info |
| `agents.js` | CRUD `/agents` and `/agents/:id` |
| `connections.js` | GET `/connections` (full list) + `getConnectionsPaginated({page,pageSize,search,connectionIds})` for infinite-scroll dropdowns |
| `connections.js` | GET/PATCH/DELETE `/connections`, POST `/connections/:name/test` |
| `guardrails.js` | GET `/guardrails` |
| `jiraTemplates.js` | GET `/integrations/jira/issuetemplates` |
| `attributes.js` | CRUD `/attributes` |
| `search.js` | GET `/search?term=` |
| `infrastructure.js` | GET/PUT `/serverconfig/misc` |
| `license.js` | GET `/serverinfo` (extracts `license_info`), PUT `/orgs/license` |

When adding a new service file, follow the pattern in `services/agents.js`.

---

## Notifications — `showSnackbar`

Use the `showSnackbar` helper from `@/utils/snackbar`. It is backed by `sonner` — the
same library the legacy CLJS app uses — and renders through
`src/components/Snackbar/Toast.jsx`, a one-to-one port of
`webapp.components.toast` so toasts look identical across React and CLJS routes.
The single `<Toaster>` is mounted in `src/App.jsx`.

```js
import { showSnackbar } from '@/utils/snackbar'

showSnackbar({ level: 'success', text: 'AI Agent deactivated.' })
showSnackbar({ level: 'error',   text: 'Failed to update.', description: err.message })
showSnackbar({ level: 'info',    text: 'Heads up.' })

// Error toasts can expand to show a `details` panel (object → key/value lines)
showSnackbar({
  level: 'error',
  text: 'Validation failed.',
  details: { field: 'name', reason: 'required' },
})
```

Error toasts auto-dismiss after 10 seconds (mirrors v1); other levels use sonner's
default. Do NOT import `notifications` from `@mantine/notifications` in new code — it
renders a completely different visual and breaks parity with v1. Pre-existing pages
that still use it should be migrated opportunistically. See the "Snackbars / Toasts"
section of `CLAUDE.md` for the full rule.

---

## Icons

Always `lucide-react`. Never `@tabler/icons-react` or any other library.
```jsx
import { Trash2, Plus, Zap, TriangleAlert } from 'lucide-react'
```
