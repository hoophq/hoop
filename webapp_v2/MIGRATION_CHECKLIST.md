# Page Migration Checklist

Use this for every CLJS → React page migration. The reference implementation is `src/pages/Agents/`.

---

## Step 0 — Understand the CLJS page

Before writing any React code, read the original:

1. Find the CLJS view file (search by route name):
   ```bash
   grep -r "/connections" ../webapp/src --include="*.cljs" -l
   ```
2. Identify:
   - What data is loaded (which `rf/subscribe` calls)
   - What actions exist (which `rf/dispatch` calls)
   - Which API endpoints are called (`:http-xhrio` effects)
   - What permissions gate UI elements (`is-admin?`, `is-free-license?`)
   - What modals/dialogs are used
   - What empty/loading/error states exist

See `CLJS_PATTERNS.md` for the full mapping of CLJS patterns to React equivalents.

---

## Step 1 — Service file

Create `src/services/featureName.js`:

```js
import api from './api'

export const featureService = {
  list: () => api.get('/feature-name'),
  get: (id) => api.get(`/feature-name/${id}`),
  create: (data) => api.post('/feature-name', data),
  update: (id, data) => api.put(`/feature-name/${id}`, data),
  delete: (id) => api.delete(`/feature-name/${id}`),
}
```

Only add the methods the page actually uses. Check `CONTEXT_MIGRATION.md` for existing services before creating a new file.

---

## Step 2 — Store

**Global store** (`src/stores/useFeatureNameStore.js`) if state is shared across pages.
**Local store** (`src/pages/FeatureName/store.js`) if state is only used by this page and its sub-pages.

Minimum shape:
```js
import { create } from 'zustand'
import { featureService } from '@/services/featureName'

export const useFeatureStore = create((set) => ({
  items: [],
  loading: false,
  error: null,

  fetchItems: async () => {
    set({ loading: true, error: null })
    try {
      const { data } = await featureService.list()
      set({ items: data, loading: false })
    } catch {
      set({ error: 'Failed to load items.', loading: false })
    }
  },
}))
```

---

## Step 3 — Page component

Structure of `src/pages/FeatureName/index.jsx`:

```jsx
import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Stack, Title, Button, Text } from '@mantine/core'
import { Plus } from 'lucide-react'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import useMinDelay from '@/hooks/useMinDelay'
import { useUserStore } from '@/stores/useUserStore'
import { useFeatureStore } from '@/stores/useFeatureStore'

export default function FeaturePage() {
  const navigate = useNavigate()
  const { isAdmin } = useUserStore()
  const { items, loading, error, fetchItems } = useFeatureStore()
  const showLoader = useMinDelay(loading, 500)

  useEffect(() => { fetchItems() }, [])

  if (showLoader) return <PageLoader h={400} />
  if (error) return <Text c="red">{error}</Text>
  if (!items.length) return (
    <EmptyState
      icon={SomeIcon}
      title="No items yet"
      action={isAdmin ? { label: 'Create', onClick: () => navigate('/feature/new') } : undefined}
    />
  )

  return (
    <Stack p="md" gap="md">
      <Title order={3}>Feature Name</Title>
      {items.map(item => <ItemRow key={item.id} item={item} />)}
    </Stack>
  )
}
```

**Loading → Empty → List → Error** is the expected rendering order.

---

## Step 4 — Sub-pages (Create / Edit)

```
src/pages/FeatureName/
├── index.jsx           # list page
├── store.js            # (if local state needed)
└── Create/
    └── index.jsx       # create/edit form
```

For wizards or multi-step flows, use `StepAccordion` from `src/components/StepAccordion/`. See `pages/Agents/Create/` as reference.

---

## Step 5 — Route

In `src/Router.jsx`, add the new route **above** the `/*` catch-all:

```jsx
<Route path="/feature-name" element={<FeaturePage />} />
<Route path="/feature-name/new" element={<CreateFeaturePage />} />
```

---

## Step 6 — Sidebar link

Check `src/layout/Sidebar/` — the nav link for this route may already exist. Confirm the `to` prop matches the route you defined. If the nav item is missing, add it following the existing pattern.

---

## Step 7 — Verify behavior parity

Compare with the running CLJS app:
- [ ] Same data shown in the same order
- [ ] Empty state matches (same icon/message)
- [ ] Loading state doesn't flash (useMinDelay working)
- [ ] Admin-only actions hidden for non-admins
- [ ] Free-tier gates respected
- [ ] Create/delete actions produce the same result
- [ ] Navigation after create/delete matches the original flow

---

## Step 8 — Update CONTEXT_MIGRATION.md

After completing the migration:
1. Update the **Routing Split** table — change the route status to `Done`
2. Update **What's Done vs Pending** section
3. If any global CLJS component was removed, update the **Global Components** table
