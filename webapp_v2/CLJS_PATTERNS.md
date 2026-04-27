# ClojureScript → React Pattern Mapping

When migrating a page, read the CLJS source first (`../webapp/src/webapp/`).
This file maps CLJS patterns to their React/Zustand equivalents.

---

## State Management

### Global state (Re-frame → Zustand)

```cljs
;; CLJS — read global state via subscription
(let [agents @(rf/subscribe [:agents/list])]
  ...)

;; CLJS — write global state via event
(rf/dispatch [:agents/fetch])
```

```js
// React — read from Zustand store
const { agents, fetchAgents } = useAgentStore()

// React — trigger action
useEffect(() => { fetchAgents() }, [])
```

### Local component state (Reagent atom → useState)

```cljs
;; CLJS
(let [name (r/atom "")
      loading? (r/atom false)]
  [:input {:value @name :on-change #(reset! name (-> % .-target .-value))}])
```

```jsx
// React
const [name, setName] = useState('')
const [loading, setLoading] = useState(false)
<input value={name} onChange={e => setName(e.target.value)} />
```

---

## HTTP Calls

### Re-frame http-xhrio → Axios service

```cljs
;; CLJS — http effect inside an event handler
{:http-xhrio {:method          :get
               :uri             "/api/agents"
               :response-format (ajax/json-response-format {:keywords? true})
               :on-success      [:agents/fetch-success]
               :on-failure      [:agents/fetch-failure]}}
```

```js
// React — service file (services/agents.js)
export const agentsService = {
  list: () => api.get('/agents'),
  create: (data) => api.post('/agents', data),
  delete: (id) => api.delete(`/agents/${id}`),
}

// Store action
fetchAgents: async () => {
  set({ loading: true, error: null })
  try {
    const { data } = await agentsService.list()
    set({ agents: data, loading: false })
  } catch {
    set({ error: 'Failed to load agents', loading: false })
  }
}
```

---

## Lifecycle

### component-did-mount → useEffect

```cljs
;; CLJS
{:component-did-mount #(rf/dispatch [:agents/fetch])}
```

```jsx
// React
useEffect(() => {
  fetchAgents()
}, [])
```

### component-will-unmount → useEffect cleanup

```cljs
;; CLJS
{:component-will-unmount #(rf/dispatch [:agents/clear])}
```

```jsx
// React
useEffect(() => {
  return () => clearAgents()
}, [])
```

---

## Routing / Navigation

```cljs
;; CLJS — Pushy navigate
(pushy/set-token! "/agents/new")

;; CLJS — read current route param
(:id (:route-params @(rf/subscribe [:route])))
```

```jsx
// React — React Router v7
import { useNavigate, useParams } from 'react-router-dom'

const navigate = useNavigate()
navigate('/agents/new')

const { id } = useParams()
```

---

## UI / Rendering

### Hiccup → JSX

```cljs
;; CLJS — Hiccup syntax
[:div.flex.flex-col.gap-4
  [:h1.text-lg "Agents"]
  [:button {:on-click #(rf/dispatch [:modal/open :create-agent])} "New Agent"]]
```

```jsx
// React — Mantine (no Tailwind, no className for layout)
<Stack gap="md">
  <Title order={3}>Agents</Title>
  <Button onClick={() => navigate('/agents/new')}>New Agent</Button>
</Stack>
```

### Conditional rendering

```cljs
;; CLJS
(when is-admin
  [:button "Delete"])

(if loading?
  [:div "Loading..."]
  [:div "Content"])
```

```jsx
// React
{isAdmin && <Button>Delete</Button>}

{loading ? <PageLoader /> : <Content />}
```

### `callout-link` → `DocsBtnCallOut`

```cljs
;; CLJS — webapp.components.callout-link
[callout-link/main {:href "https://hoop.dev/docs/..." :text "Learn more about gRPC"}]
```

```jsx
// React — already exists in @/components/DocsBtnCallOut
import DocsBtnCallOut from '@/components/DocsBtnCallOut'

<DocsBtnCallOut href="https://hoop.dev/docs/..." text="Learn more about gRPC" />
```

### List rendering

```cljs
;; CLJS
(for [agent agents]
  ^{:key (:id agent)}
  [AgentRow agent])
```

```jsx
// React
{agents.map(agent => (
  <AgentRow key={agent.id} agent={agent} />
))}
```

---

## Modals & Dialogs

CLJS uses a global modal system (`rf/dispatch [:modal->open :modal-name]`). In React this is not yet migrated. Use Mantine's `useDisclosure` for local modal state:

```jsx
import { useDisclosure } from '@mantine/hooks'
import { Modal } from '@mantine/core'

const [opened, { open, close }] = useDisclosure(false)

<Button onClick={open}>Delete</Button>
<Modal opened={opened} onClose={close} title="Confirm deletion">
  <Button color="red" onClick={() => { handleDelete(); close() }}>Delete</Button>
</Modal>
```

---

## Auth / Permissions

```cljs
;; CLJS — read from re-frame db
@(rf/subscribe [:user/is-admin?])
@(rf/subscribe [:user/is-free-license?])
```

```js
// React — Zustand
const { isAdmin, isFreeLicense } = useUserStore()
```

---

## Styling: Tailwind → Mantine

```cljs
;; CLJS — Tailwind classes
[:div.flex.flex-col.gap-4.p-6.text-sm.text-gray-500 "..."]
```

```jsx
// React — Mantine style props (never use Tailwind in webapp_v2)
<Stack gap="md" p="md">
  <Text fz="sm" c="dimmed">...</Text>
</Stack>
```

Common mappings:

| Tailwind | Mantine equivalent |
|----------|-------------------|
| `flex flex-col gap-4` | `<Stack gap="md">` |
| `flex flex-row gap-4` | `<Group gap="md">` |
| `p-4` / `p-6` | `p="md"` / `p="lg"` |
| `text-sm` | `fz="sm"` |
| `text-gray-500` | `c="dimmed"` |
| `font-semibold` | `fw={600}` |
| `text-red-500` | `c="red"` |
| `w-full` | `w="100%"` |
| `max-w-md` | `maw={448}` |

---

## Finding CLJS Source Files

Common locations:
- Pages: `../webapp/src/webapp/views/` or `../webapp/src/webapp/pages/`
- Shared UI: `../webapp/src/webapp/shared_ui/`
- Re-frame events: `../webapp/src/webapp/events/`
- Re-frame subscriptions: `../webapp/src/webapp/subs/`
- HTTP calls: `../webapp/src/webapp/fx/http.cljs` or inline in events

Search by route name to find the right file:
```bash
grep -r "\"connections\"" ../webapp/src --include="*.cljs" -l
```
