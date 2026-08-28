# Control Plane Backend

Scaffold for the hoop control plane API. It serves `GET /healthz` and nothing
else.

**The product docs are the source of truth**, starting at
<https://hoop.dev/docs/core-concepts/control-plane>. This file repeats only
what those docs already state and marks everything else TBD. TBD means do not
guess: raise it before writing code that assumes an answer, and do not infer an
answer from the scaffold.

## What the docs already decide

Constraints on anything built here.

- The sidecar dials out over ordinary HTTP and the control plane never dials
  in. No tunnel, no bidirectional connection held open, nothing pushed.
- Configuration is pull-based: a sidecar picks it up on its ping.
- A per-sidecar token is the whole credential. No second credential and no
  certificate exchange. One token per sidecar, never shared between two.
- A sidecar survives this service being down. It keeps serving with the config
  it already loaded, and a failed fetch falls back to its local file. So this
  service must not be on the data path.
- Configuration is written once here and delivered to every sidecar. Rule sets
  bundle a Guardrail, an Analyzer and Data Masking into one posture.
- The fleet view reports which sidecars run, what each one resolved, and the
  last check-in time.
- Admin sign-in is local by default: this service owns its users, their
  passwords and the token signing.
- Enterprise feature, gated on a license key.
- PostgreSQL holds the state. The published deployments read `POSTGRES_DB_URI`
  and use the `private` schema.
- Versioned independently from the sidecars, but kept within a minor release
  of them.

The docs give the sidecar side of the contract as `control_plane.url` and
`control_plane.token_file`, or `HOOP_CONTROL_PLANE_URL` and
`HOOP_SIDECAR_TOKEN_FILE`. The page marks those names as placeholders pending
the release, so do not pin anything to them yet.

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
| `CONTROLPLANE_LISTEN_ADDR` | `0.0.0.0:8020` | Bind address. The published deployments serve 8009, but the gateway owns 8009 on a development machine and both run there. Reconciling the two is TBD. |
| `CONTROLPLANE_SHUTDOWN_GRACE` | `15s` | Drain time for in-flight requests on SIGTERM. |
| `LOG_LEVEL` | `info` | `trace`, `debug`, `info`, `warn` or `error`. |
| `LOG_FORMAT` | `json` | `json` or `text`. |

A malformed value fails at startup. A missing optional value gets the default.

These names are this scaffold's own. The docs publish `POSTGRES_DB_URI`,
`API_URL`, `TLS_KEY`, `TLS_CERT` and `TLS_CA` for the deployed service, and
whether this module is that service is TBD.

## TBD

Not answered by the docs. Do not add one without agreeing it first.

- Whether this module is what the published deployments run, or a second
  service beside the gateway
- Every route path, prefix and version. The `/healthz` and `/config` in the
  docs are the sidecar's own admin API on `localhost:19000`, not this service
- The ping interval, and what a ping carries in each direction
- The shape of a config document and of a rule set on the wire
- Token issuance, rotation, revocation and expiry, and the admin API for it
- Datastore access, schema and migrations for this module
- Environment variable names, packaging, helm chart and release
- Multi-instance and HA
- How the license check works

## Conventions

True of the code today, and small enough to change if a discussion goes the
other way.

- Structured logging through an injected `*slog.Logger`. No package-level
  logger and no wrapper type. `common/log` is avoided because it is a separate
  module with a hundred dependencies.
- Config is a value passed down from `main`. No package-level config global.
- `gin.New()`, not `gin.Default()`. Default installs gin's own unstructured
  logger and recovery alongside ours.
- Never log a query string, a raw path or a request body. All three routinely
  carry credentials in this product.
- Liveness carries no dependency check. A dependency blip must not restart
  every replica at once.
- No `NoRoute` handler. This binary serves no UI, so an unmatched path is a
  mistake and must look like one.
- Keep code comments short and direct; prefer concise comments over long explanatory blocks.
- Tests live beside the source.
