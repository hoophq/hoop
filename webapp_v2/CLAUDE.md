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

## Styled Components

When Mantine's built-in props and theme tokens are not enough for a visual requirement:
- Create a dedicated component in `src/components/` (not inline in the page).
- Use Mantine's `createPolymorphicComponent` / `Box` with `style` prop, or a CSS Module scoped to that component.
- Never add global CSS or unscoped styles.

## Reference Implementation
- `pages/Agents/` is the reference page showing the full pattern: store + service + list page + create page

## Migration Rule
This project is a migration of `../webapp/` (ClojureScript) to React — not a greenfield build.
Before implementing any behavior (mobile nav, modals, transitions, keyboard handling, etc.),
check how it works in the original app first (`../webapp/src/webapp/`). Replicate the behavior
using Mantine/React equivalents. Do not invent new patterns when the original already has one.
