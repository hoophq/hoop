# hoop control plane

Manages a fleet of hoop sidecars, the `sidecar/` module in this repository.
An operator decides a config once, here, and it is distributed everywhere.

```
        ┌──────────────────────┐
        │  Admin UI (frontend) │
        └──────────┬───────────┘
                   │ HTTPS, JSON
        ┌──────────┴───────────┐
        │  control plane       │
        │  (backend)           │
        │                      │
        │  desiredstate  what should run
        │  inventory     what IS running
        │  adminauth     who may change it
        │  sidecarauth   who a sidecar is
        └──────────┬───────────┘
                   │ WebSocket, always dialled by the sidecar
      ┌────────────┼────────────┐
      │            │            │
 ┌────┴────┐  ┌────┴────┐  ┌────┴────┐
 │ sidecar │  │ sidecar │  │ sidecar │   sidecar/, on customer infra
 └────┬────┘  └────┬────┘  └────┬────┘
      │            │            │
   database     database     database    traffic never passes through us
```

The control plane is not on the data path. If this process is down, sidecars
keep enforcing the last config they accepted.

## Layout

Two independent applications, one per directory.

```
controlplane/
  backend/    Go API server, its own module and go.mod
  frontend/   React admin UI, its own package.json and CLAUDE.md
```

They are deployed separately and neither serves the other. The backend is a
JSON API with a `default-src 'none'` CSP and no `NoRoute` handler.

## Status

Scaffold. `/healthz` and `/readyz` work; every other endpoint answers
`501 Not Implemented` and names the ticket that owns it.

| Component | Ticket | State |
|---|---|---|
| `desiredstate` | EVL-231 | stub |
| `inventory` | EVL-232 | stub |
| `adminauth` | EVL-233 | stub |
| `sidecarauth` | EVL-234 | stub, design open |

`adminauth.RequireAdmin` is mounted and answers 501, so every protected route
is closed rather than open. Test component handlers directly until EVL-233
lands.

## Run it

Postgres is the only requirement. From the repository root:

```sh
make run-dev-postgres                        # or use your own
POSTGRES_DB_URI=postgres://... go run ./controlplane/backend/cmd/controlplane
```

Migrations run at boot unless `CONTROLPLANE_AUTO_MIGRATE=false`. Then:

```sh
curl localhost:8020/healthz
curl localhost:8020/readyz
```

## Commands

```
controlplane serve              start the API server (default)
controlplane migrate up         apply pending migrations
controlplane migrate down [n]   roll back n migrations, default 1
controlplane migrate version    print the applied schema version
```

`migrate` is a command, not only a boot side effect, so a deploy pipeline can
run the schema change as its own step and then start replicas with
`CONTROLPLANE_AUTO_MIGRATE=false`.

## Build

```sh
make build-controlplane
```

Output is `dist/release-binaries/controlplane-${GOOS}-${GOARCH}`. Every build
target in the root Makefile defaults to `linux/amd64`, so building for the
machine you are sitting at needs the override:

```sh
GOOS=darwin GOARCH=arm64 make build-controlplane
```

## Configuration

Environment only. `POSTGRES_DB_URI` is required; everything else has a
default.

| Variable | Default | Meaning |
|---|---|---|
| `POSTGRES_DB_URI` | *required* | Postgres connection URI. Must be a `postgres://` or `postgresql://` URL. Tables live in the `controlplane` schema. |
| `CONTROLPLANE_LISTEN_ADDR` | `0.0.0.0:8020` | HTTP listen address. Not 8009: the gateway owns that and the two run side by side. |
| `CONTROLPLANE_DEPLOYMENT` | `development` | `development` or `production`. Only ever unlocks refusals, so an unset value must not be the permissive one. |
| `CONTROLPLANE_CORS_ALLOWED_ORIGINS` | empty | Comma-separated exact origins. Empty means no cross-origin request is allowed. Set `http://localhost:5173` for local frontend work. |
| `CONTROLPLANE_MAX_OPEN_CONNS` | unlimited | Postgres pool ceiling. |
| `CONTROLPLANE_MIGRATION_PATH_FILES` | embedded | Read migrations from this directory instead of the binary. |
| `CONTROLPLANE_AUTO_MIGRATE` | `true` | Run pending migrations at boot. Set `false` when a deploy pipeline owns the `migrate` step. |
| `CONTROLPLANE_SHUTDOWN_GRACE` | `15s` | How long in-flight requests get to finish on SIGTERM. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. `trace` maps to `debug`. |
| `LOG_FORMAT` | `json` | `json` or `text`. |

There is no helm chart for this service yet.

## Backend layout

```
backend/
  cmd/controlplane/          entrypoint: serve and migrate
  internal/config/           environment config, fails fast on anything malformed
  internal/logging/          stdlib slog setup
  internal/database/         pool lifecycle and shared column types
  internal/migrations/       numbered SQL, embedded, plus the runner
  internal/wire/             control plane <-> sidecar message vocabulary
  internal/api/              Gin engine, middleware, routes, health
  internal/api/apierr/       error response shapes
  internal/api/desiredstate/ EVL-231
  internal/api/inventory/    EVL-232
  internal/api/adminauth/    EVL-233
  internal/api/sidecarauth/  EVL-234
```

One package per feature, not per layer. A feature package owns its model, its
store and its handlers together, so adding a field touches one directory
rather than four.

Everything is under `internal/`. Nothing outside this module imports it, and
`internal/` is the compiler enforcing that rather than a convention asking for
it.

## Develop

```sh
cd controlplane/backend
go build ./... && go vet ./... && go test ./...
```

CI runs these through `make test-oss` from the repository root:
`go test github.com/hoophq/hoop/...` matches this module because it is in
`go.work`.

New migration:

```sh
migrate create -ext sql -dir controlplane/backend/internal/migrations -seq <description>
```

Read the next sequence number from the directory listing, not from a count,
and write both the `.up.sql` and the `.down.sql`. A test fails if either is
missing.

## Read before changing anything

`controlplane/CLAUDE.md`. It carries the five non-negotiables, the decided and
open questions, the wire contract, and the constraint list for each component.
`frontend/` has its own.
