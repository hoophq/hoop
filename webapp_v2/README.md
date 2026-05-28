# Hoop WebApp V2

Modern React-based web application for Hoop.

## Tech Stack

- **React 19** - UI framework
- **Vite** - Build tool and dev server
- **Mantine v8** - Component library
- **Zustand** - State management
- **React Router v7** - Client-side routing
- **Axios** - HTTP client

## Getting Started

### Prerequisites

- Node.js 18+ and npm

### Installation

```bash
npm install
```

### Development

The React shell proxies `/js`, `/css`, `/images`, `/data` and `/icons` to the
ClojureScript dev server (shadow-cljs on :8280), and `/api` to the gateway
(:8009). Both need to be running for the legacy routes to render — when only
Vite is up, you'll see "Loading…" on any CLJS route because `/js/app.js`
returns 502.

**Single-command dev (recommended):**

```bash
npm run dev:full
```

This runs Vite and shadow-cljs side by side under `npm-run-all`. Logs are
prefixed (`dev` = Vite, `dev:cljs` = shadow-cljs). The `--race` flag tears
both down together if either exits, so Ctrl+C cleans everything up.

**Vite only** (when shadow-cljs is already running elsewhere, or you only
need React-owned routes):

```bash
npm run dev
```

Access the app at `http://localhost:5173`.

#### Hot reload caveats

- **Vite HMR** updates React/Mantine source under `webapp_v2/src` instantly.
- **shadow-cljs HMR** rebuilds `/js/app.js` in `webapp/resources/public`.
  Because Vite only **proxies** that path, it does NOT see the file change
  and will NOT hot-swap the CLJS bundle inside the React page. After a CLJS
  edit, **hard-reload the browser tab** (Cmd+Shift+R) for the new bundle to
  load. The shadow-cljs terminal will still show "watch compilation finished"
  — that's expected.
- Editing `webapp/resources/public/css/site.css` (Tailwind) has the same
  caveat: PostCSS rebuilds it, Vite proxies the next request, you need a
  reload.

### Build

```bash
npm run build
```

### Preview Production Build

```bash
npm run preview
```

## Configuration

### Environment Variables

A `.env` file is **optional**. All variables documented in `.env.sample` have
working defaults baked into `vite.config.js`, so the dev server runs out of
the box with no setup. Only create a `.env` if you need to override one of:

| Variable | Default | Purpose |
|----------|---------|---------|
| `VITE_API_URL` | `/api` (relative) | Custom backend base URL at runtime |
| `VITE_GATEWAY_URL` | `http://localhost:8009` | Vite dev proxy target for `/api` |
| `VITE_CLJS_URL` | `http://localhost:8280` | Vite dev proxy target for CLJS assets |

## Authentication

This app supports two authentication methods:

1. **Local Auth**: Email/password authentication via `/localauth/login`
2. **OAuth/IDP**: SSO authentication via configured identity providers

The authentication method is automatically detected from the gateway configuration.

### Auth Flow

- Token is stored in localStorage as `jwt-token`
- Token can be received via cookies (`hoop_access_token`) or query params (`?token=xxx`)
- On 401 responses, the app saves the current URL and redirects to login
- After successful auth, redirects back to the saved URL

## Project Structure

See [CLAUDE.md](./CLAUDE.md) for detailed architecture guidelines.

```
src/
├── components/       # Reusable components
├── layout/           # Layout infrastructure (Sidebar, Header, EmptyState)
├── features/         # Complex features (CommandPalette)
├── pages/            # Page components
├── stores/           # Zustand state stores
├── services/         # API services
├── hooks/            # Custom hooks
└── utils/            # Utility functions
```

## Development Guidelines

Read [CLAUDE.md](./CLAUDE.md) for:
- Architecture patterns
- Code style conventions
- Routing structure
- State management approach
- Authentication implementation details
