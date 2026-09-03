# Control Plane Frontend

The web UI for hoop's control plane — sidecar fleet, reviews, and the features
configured once for every sidecar. Administration only.

Coding rules, styling hierarchy and the colour-scheme rule: [CLAUDE.md](./CLAUDE.md).

## Running it

There is no separate control plane backend: the API is the gateway binary
started with `hoop start control-plane`. It serves every route the gateway
serves, the gateway's web UI at `/` included, and starts none of the data
plane: no gRPC transport, no protocol proxies and no transport plugins
(ADR-0013). This app is not that web UI; it runs on its own through Vite.

```bash
make run-dev-postgres        # once, from the repo root
make run-dev-control-plane   # control plane on :8019, reads the repo-root .env
npm install
API_URL=http://localhost:8019 npm run dev   # Vite on :5173
```

`make run-dev`, the gateway on :8009 and the default proxy target, works as a
backend too: both modes answer the same routes. `/api/serverinfo` reports
which one is answering as `application_mode`.

Only `/api` is proxied. There is no ClojureScript dev server and no asset proxy —
everything under `/images`, `/icons` and `/data` is served from `public/`.

| Script | |
|---|---|
| `npm run dev` | Vite dev server |
| `npm run build` | Checks assets and routes, then builds into `dist/` |
| `npm run lint` | ESLint |
| `npm run check:assets` | Verifies every referenced image exists in `public/` |
| `npm run check:routes` | Verifies every literal navigation target is a real route |
| `npm run preview` | Serve the production build |

## Environment

A `.env` is optional — `vite.config.js` has working defaults.

| Variable | Default | Purpose |
|---|---|---|
| `API_URL` | `http://localhost:8009` | Vite dev proxy target for `/api` |
| `VITE_API_URL` | `/api` | Runtime API base URL (`services/api.js`) |
| `SEGMENT_WRITE_KEY` | hoop.dev production key | Baked in at build time |

## Where this came from

Extracted from `webapp_v2/` — 177 of its 371 source files, the closure of the pages the
control plane keeps. Left behind: the ClojureScript bridge, the Native Connections
drawer (nobody reaches a resource through this app), the onboarding checklist, and
every page outside the control plane's scope.

`webapp_v2` is frozen and serves the gateway until it is retired. The two diverge on
purpose; there is no shared package and no imports across the boundary. The duplication
is only acceptable while `webapp_v2` is in end-of-life — if the gateway is not retired,
the answer is to promote a shared package, not to let the copies drift for years.
