# Control Plane Frontend

The web UI for hoop's control plane — sidecar fleet, reviews, and the features
configured once for every sidecar. Administration only.

Coding rules, styling hierarchy and the colour-scheme rule: [CLAUDE.md](./CLAUDE.md).

## Running it

There is no separate control plane backend: the API is the gateway, booted into
its control-plane surface.

Add this to the repo-root `.env` **before** starting the gateway:

```
APP_MODE=control-plane
```

It has to go in the file, not on the command line — `scripts/dev/run.sh` runs
the gateway in a container with `--env-file=.env`, so a shell prefix never
reaches it.

```bash
make run-dev          # gateway on :8009, from the repo root
npm install
npm run dev           # Vite on :5173
```

Without `APP_MODE` the gateway registers all 264 of its routes, so a call to
something the control plane blocks works here and 404s in production. The
routes the control plane serves are listed in
`buildControlPlaneRoutes` in `gateway/api/server.go`, and an amber banner appears at the top of
every page (dev builds only) when the backend is answering in gateway mode.

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
