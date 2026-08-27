# Control Plane Frontend

The web UI for hoop's control plane — sidecar fleet, reviews, and the features
configured once for every sidecar. Administration only.

Coding rules, styling hierarchy and the colour-scheme rule: [CLAUDE.md](./CLAUDE.md).

## Running it

The control plane backend does not exist yet, so the API comes from the gateway.

```bash
make run-dev          # gateway on :8009, from the repo root
npm install
npm run dev           # Vite on :5173
```

Only `/api` is proxied. There is no ClojureScript dev server and no asset proxy —
everything under `/images`, `/icons` and `/data` is served from `public/`.

| Script | |
|---|---|
| `npm run dev` | Vite dev server |
| `npm run build` | Checks assets, then builds into `dist/` |
| `npm run lint` | ESLint |
| `npm run check:assets` | Verifies every referenced image exists in `public/` |
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
purpose; there is no shared package and no imports across the boundary.

The reasoning behind the split is recorded in
[`docs/adr/0006-control-plane-frontend.md`](../../docs/adr/0006-control-plane-frontend.md).
