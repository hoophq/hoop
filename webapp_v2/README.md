# Hoop WebApp V2

Modern React-based web application for Hoop.

## Documentation map

| File | Read it for |
|------|-------------|
| `README.md` | Setup, dev servers/ports, HMR caveats, env vars (this file) |
| `CLAUDE.md` | Coding + styling rules, auth flow, snackbar/CSS-layer rules |
| `CONTEXT_MIGRATION.md` | Shell/bridge architecture, routing split, migration status |
| `COMPONENTS.md` | Catalog of components/hooks + non-obvious store/service notes |
| `MIGRATION_CHECKLIST.md` | Step-by-step process for migrating one CLJS page |
| `CLJS_PATTERNS.md` | CLJS → React rosetta stone (incl. reusable building blocks) |
| `MIGRATION_ROADMAP.md` | Wave plan — what's left, in what order |

Each topic has exactly one owner file (the one listed above); everything else
is a pointer. When updating docs, edit the owner — don't re-duplicate content.

## Tech Stack

- **React 19** - UI framework
- **Vite** - Build tool and dev server
- **Mantine v8** - Component library
- **Zustand** - State management
- **React Router v7** - Client-side routing
- **Axios** - HTTP client
- **lucide-react** - Icons (the only icon library)

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

**Against the control plane** (the same bundle, rendered as the control plane
because `/api/publicserverinfo` reports `application_mode: "control-plane"`):

```bash
make run-dev-control-plane                                 # repo root, control plane on :8019
npm --prefix ../webapp run download-connection-metadata    # once; the catalog JSON is gitignored
API_URL=http://localhost:8019 npm run dev                  # no shadow-cljs needed
```

The control plane never loads the CLJS bundle, so shadow-cljs can stay down.
`/images`, `/icons` and `/data` are served by Vite straight from
`webapp/resources/public` (see `cljsStaticAssets` in `vite.config.js`); only
`/js` and `/css` are proxied to :8280.

**OIDC (Auth0 and friends):** the script sets `API_URL` to the port it listens
on, and the OIDC callback is derived from `API_URL`. If your IdP only allows
`http://localhost:8009/api/callback`, run the control plane on that port with
the gateway stopped (the two cannot share it), and drop the `API_URL` override
on the Vite side:

```bash
PORT=8009 make run-dev-control-plane
npm run dev
```

Or add `http://localhost:8019/api/callback` to the IdP's allowed callback URLs
and keep the two ports.

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

A `.env` file is **optional** — `vite.config.js` bakes in working defaults for
everything, so the dev server runs out of the box with no setup (see also
`.env.sample`). Only create a `.env` if you need to override one of:

| Variable | Default | Purpose |
|----------|---------|---------|
| `VITE_API_URL` | `/api` (relative) | Custom backend base URL at runtime (`services/api.js`) |
| `API_URL` | `http://localhost:8009` | Vite dev proxy target for `/api` — same env var the CLJS build reads via shadow-cljs closure-defines |
| `VITE_CLJS_URL` | `http://localhost:8280` | Vite dev proxy target for CLJS assets (`/js`, `/css`, `/images`, …) |
| `SEGMENT_WRITE_KEY` | hoop.dev production key | Build-time: Segment write key baked into the bundle at `npm run build`. Same env var as the CLJS bundle, so one setting controls both webapps — override only when pointing a build at a different Segment workspace |

## Authentication

Supports local auth (email/password) and OAuth/IDP, auto-detected from the
gateway configuration. The token lives in `localStorage.jwt-token` (shared
with the legacy CLJS app). Full flow, key files, and 401 handling:
`CLAUDE.md` "Authentication Flow".

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
