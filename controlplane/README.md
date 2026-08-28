# hoop control plane

Where an operator will manage a fleet of hoop sidecars, the `sidecar/` module
in this repository.

**Scaffold.** The backend answers a health check and nothing else. Almost every
question about how the control plane works is still open and under discussion.
This README records what the code does today. Anything under TBD is undecided,
so do not infer an answer to it from the scaffold.

## Layout

Two independent applications, one per directory.

```
controlplane/
  backend/    Go API. Its own module and go.mod. See backend/CLAUDE.md.
  frontend/   React admin UI. Its own package.json. See frontend/README.md.
```

## Backend

### Run it

No database and no external service. It starts with no configuration at all.

```sh
cd controlplane/backend
go run ./cmd/controlplane

curl -s localhost:8020/healthz
# {"status":"ok","version":"dev"}
```

### Commands

| Task | Command | From |
|---|---|---|
| Run | `go run ./cmd/controlplane` | `controlplane/backend` |
| Test | `go test ./...` | `controlplane/backend` |
| Vet | `go vet ./...` | `controlplane/backend` |
| Build | `make build-controlplane` | repository root |

The build output is `dist/release-binaries/controlplane-${GOOS}-${GOARCH}`.
Every build target in the root Makefile defaults to `linux/amd64`, so building
for the machine you are sitting at needs the override:

```sh
GOOS=darwin GOARCH=arm64 make build-controlplane
```

The module is listed in `go.work`, so `make test-oss` at the repository root
runs these tests too.

### Configuration

Environment only. Everything has a default.

| Variable | Default | Meaning |
|---|---|---|
| `CONTROLPLANE_LISTEN_ADDR` | `0.0.0.0:8020` | HTTP listen address. Not 8009: the gateway owns that and the two run side by side. |
| `CONTROLPLANE_SHUTDOWN_GRACE` | `15s` | How long in-flight requests get to finish on SIGTERM. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. `trace` maps to `debug`. |
| `LOG_FORMAT` | `json` | `json` or `text`. |

A malformed value stops the process at startup.

### Layout

```
backend/
  cmd/controlplane/    entry point
  internal/api/        Gin engine, HTTP server, GET /healthz
  internal/config/     environment loading
  internal/logging/    *slog.Logger constructor
```

One direct dependency, `github.com/gin-gonic/gin`. No database, no ORM, no
migrations. Everything is under `internal/`, so nothing outside this module can
import it.

## TBD

Undecided, and deliberately absent from the code:

- Transport between the control plane and sidecars
- API shape, route prefixes, versioning
- Datastore, schema, migrations
- Admin authentication and authorization
- Sidecar identity, enrollment, credential rotation and revocation
- Config delivery, and what a sidecar config document is
- Inventory and status reporting
- Relationship to `gateway/`, and whether this stays a separate deployment
- Packaging, helm chart, release
- Multi-instance and HA
- Licensing

## Read before changing anything

`controlplane/backend/CLAUDE.md` for the backend.
`controlplane/frontend/CLAUDE.md` for the frontend.
