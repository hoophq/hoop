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
│   └── Layout/          # App shell: Sidebar, Header
├── stores/              # Zustand global stores (cross-route state)
├── services/            # Axios API calls (one file per domain)
├── hooks/               # Reusable custom hooks
├── utils/               # Pure utility functions
├── routes/              # Route-based pages (each route = folder)
│   ├── [Route]/
│   │   ├── index.jsx        # Page component
│   │   ├── components/      # Components scoped to this route
│   │   ├── store.js         # Local store (only if state is route-specific)
│   │   └── [SubRoute]/
│   │       └── index.jsx
├── App.jsx              # Root component + providers
├── Router.jsx           # Route definitions
└── main.jsx             # Entry point
```

## Architecture Rules

### Stores (Zustand)
- **Global stores** (`src/stores/`): State consumed by multiple routes (auth, user, resources, connections, agents, UI)
- **Local stores** (`src/routes/[Route]/store.js`): State that only exists in that specific page (form wizard steps, local filters)
- Stores access services for API calls. Components access stores for state.
- Access store state outside React with `useStore.getState()`

### Services (Axios)
- Base instance in `services/api.js` with auth interceptor and 401 handling
- One file per domain: `services/agents.js`, `services/resources.js`, etc.
- Services return axios promises. Stores handle the response.

### Components
- `src/components/` = Reusable across the whole app. Receive props, no direct store access preferred.
- `src/routes/[Route]/components/` = Scoped to that route/domain only.

### Routes
- Each route is a folder. Sub-routes are sub-folders.
- Shared files for a route and its sub-routes live at the route's root folder.
- Entry point is always `index.jsx`.

### Imports
- Use `@/` alias for absolute imports from src (e.g., `import { useAuthStore } from '@/stores/useAuthStore'`)
- Group: external libraries first, then `@/` imports, then relative imports

### UI Components
- Use Mantine components exclusively. No custom CSS unless absolutely necessary.
- Use Mantine's built-in props for styling (size, variant, color, etc.)

### Code Style
- JavaScript only (no TypeScript)
- Functional components with hooks
- Named exports for stores, default exports for page components
- Keep components small and focused

## Reference Implementation
- `routes/Agents/` is the reference route showing the full pattern: store + service + list page + create page
