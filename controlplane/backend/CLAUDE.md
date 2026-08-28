# Control Plane Backend

Scaffold for the hoop control plane API. It serves `GET /healthz` and nothing
else.

**Almost nothing is decided.** How the control plane works, what it stores, how
sidecars reach it and how admins authenticate are all open and under
discussion. This file records what the code does today. Where a decision has
not been made it says TBD, and TBD means do not guess: raise it before writing
code that assumes an answer.

## What exists

| Path | What |
|---|---|
| `cmd/controlplane/main.go` | Entry point. Loads config, builds the logger, runs the server, drains on SIGTERM. |
| `internal/api/server.go` | Gin engine, `http.Server` with all four timeouts, the route table, request logging. |
| `internal/api/health.go` | `GET /healthz`. |
| `internal/config/config.go` | Environment loading. |
| `internal/logging/logging.go` | `*slog.Logger` constructor. |

One direct dependency, `github.com/gin-gonic/gin`. No database, no ORM, no
migrations.

## Commands

| Task | Command | From |
|---|---|---|
| Run | `go run ./cmd/controlplane` | `controlplane/backend` |
| Test | `go test ./...` | `controlplane/backend` |
| Vet | `go vet ./...` | `controlplane/backend` |
| Build | `make build-controlplane` | repository root |

`make build-controlplane` targets linux/amd64. For the machine you are sitting
at, `GOOS=darwin GOARCH=arm64 make build-controlplane`.

The module is listed in `go.work`, so `make test-oss` runs these tests too.

## Configuration

| Variable | Default | What |
|---|---|---|
| `CONTROLPLANE_LISTEN_ADDR` | `0.0.0.0:8020` | Bind address. 8020 because the gateway owns 8009 and both run on a development machine. |
| `CONTROLPLANE_SHUTDOWN_GRACE` | `15s` | Drain time for in-flight requests on SIGTERM. |
| `LOG_LEVEL` | `info` | `trace`, `debug`, `info`, `warn` or `error`. |
| `LOG_FORMAT` | `json` | `json` or `text`. |

A malformed value fails at startup. A missing optional value gets the default.

## TBD

Undecided. Do not infer an answer from the scaffold, and do not add one without
agreeing it first.

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

## Conventions

True of the code today, and small enough to change if a discussion goes the
other way.

- Structured logging through an injected `*slog.Logger`. No package-level
  logger and no wrapper type.
- Config is a value passed down from `main`. No package-level config global.
- `gin.New()`, not `gin.Default()`. Default installs gin's own unstructured
  logger and recovery alongside ours.
- Never log a query string, a raw path or a request body. All three routinely
  carry credentials in this product.
- Liveness carries no dependency check. A dependency blip must not restart
  every replica at once.
- No `NoRoute` handler. This binary serves no UI, so an unmatched path is a
  mistake and must look like one.
- Tests live beside the source.
