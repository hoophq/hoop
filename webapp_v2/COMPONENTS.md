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
Styles: bordered container with `border-radius`, subtle gray header background, row separators. Defined in `components/Table/Table.module.css`.

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
