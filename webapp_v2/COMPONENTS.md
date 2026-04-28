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
Scrollable code block with copy-to-clipboard button.
```jsx
import CodeSnippet from '@/components/CodeSnippet'

<CodeSnippet code="docker run ..." />
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

---

## Page Patterns

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
| `connections.js` | GET `/connections` list |
| `search.js` | GET `/search?term=` |
| `infrastructure.js` | GET/PUT `/serverconfig/misc` |
| `license.js` | GET `/serverinfo` (extracts `license_info`), PUT `/orgs/license` |

When adding a new service file, follow the pattern in `services/agents.js`.

---

## Notifications

Use `@mantine/notifications` directly in page components after async actions:
```js
import { notifications } from '@mantine/notifications'

notifications.show({ message: 'Agent deleted.', color: 'green' })
notifications.show({ message: 'Failed to delete agent.', color: 'red' })
```

---

## Icons

Always `lucide-react`. Never `@tabler/icons-react` or any other library.
```jsx
import { Trash2, Plus, Zap, TriangleAlert } from 'lucide-react'
```
