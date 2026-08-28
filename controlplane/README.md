# Hoop Control Plane

Centralizes hoop sidecars and delivers rule sets to all of them. One sidecar
reads its config from a file; twenty sidecars mean twenty files and twenty
deploys to change one rule. The control plane is the answer to that.

**Scaffold.** The backend in this directory answers a health check and nothing
else. The product docs at <https://hoop.dev/docs/core-concepts/control-plane>
are the source of truth for behaviour. This README repeats only what those
docs already state and marks everything else TBD. Do not infer an answer to a
TBD from the scaffold.

## Layout

Two independent applications, one per directory.

```
controlplane/
  backend/    Go API. Its own module and go.mod. See backend/CLAUDE.md.
  frontend/   React admin UI. Its own package.json. See frontend/README.md.
```

## What the docs already decide

### Behaviour

- Configuration is written once and delivered to every sidecar. Rule sets
  bundle a Guardrail, an Analyzer and Data Masking so one action applies a
  whole posture to a resource.
- The fleet view shows which sidecars run, what each one resolved, and the
  last check-in time.
- Enterprise feature. It requires a license key. A sidecar without a control
  plane keeps working and reads its config from disk.
- Run one control plane per environment.
- The control plane and the sidecars are versioned independently, but stay
  within a minor release of each other.

### How a sidecar connects

Source: <https://hoop.dev/docs/control-plane/connect-sidecar>.

- The sidecar dials out. The control plane never dials in, so the sidecar
  needs no inbound firewall rule and no NAT traversal.
- Ordinary HTTP request and response. Nothing is tunnelled and no
  bidirectional connection stays open.
- A per-sidecar token is the whole credential. There is no second credential
  and no certificate exchange. One token per sidecar: sharing one token
  between two is not a supported shape.
- Pull, not push. The sidecar picks up configuration on its ping, holds it in
  memory, and keeps serving with the loaded config when the control plane is
  down. A failed fetch falls back to its local file.
- Listeners stay local-file driven. Rule sets, masking policies and analyzer
  settings arrive over the ping.

The docs give the sidecar side of the contract as `control_plane.url` and
`control_plane.token_file` in `config.yaml`, or `HOOP_CONTROL_PLANE_URL` and
`HOOP_SIDECAR_TOKEN_FILE` in the environment. The page marks these names as
placeholders pending the release, so treat them as not final.

### Requirements and deployment

Source: <https://hoop.dev/docs/control-plane/install>.

- A license key.
- A hostname the sidecars can reach over HTTPS.
- A PostgreSQL database. The Kubernetes chart and the AWS stack can provision
  one.

Admin sign-in is local by default. The control plane owns its users, their
passwords and the token signing, so the first account is created after
startup and there are no seeded credentials.

| Target | Entry point |
|---|---|
| Docker Compose | `curl -sL https://hoop.dev/docker-compose.yml > docker-compose.yml && docker compose up`, then `API_URL` in a `.env` beside it |
| Kubernetes | `oci://ghcr.io/hoophq/helm-charts/hoop-chart`, values under `config:` map to environment variables |
| AWS | A per-region CloudFormation stack. `AwsCertificateArn` and `AppPublicDNS` are the inputs |

Those published deployments serve HTTP on 8009 and read `POSTGRES_DB_URI`,
`API_URL`, `TLS_KEY`, `TLS_CERT` and `TLS_CA`. Postgres holds the state in the
`private` schema. Whether the Go module below is what those deployments run is
TBD, and until it is settled do not treat the two as the same service.

## Backend

Scaffold only. What it does today, not what it should do.

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
| `CONTROLPLANE_LISTEN_ADDR` | `0.0.0.0:8020` | HTTP listen address. The published deployments serve 8009, but the gateway owns 8009 on a development machine and the two run side by side. Reconciling the two is TBD. |
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

Not answered by the docs, and deliberately absent from the code:

- Whether this module is what the published deployments run, or a second
  service beside the gateway
- Every route path, prefix and version on the control plane side. The docs
  publish `/healthz` and `/config` on the sidecar's own admin API at
  `localhost:19000`, not on this service
- The ping interval, and what a ping carries in each direction
- The shape of a config document and of a rule set on the wire
- Token issuance, rotation, revocation and expiry
- The API an admin uses to issue a token
- Datastore, schema and migrations for this module
- Its environment variable names, packaging, helm chart and release
- Multi-instance and HA
- How licensing is checked

## Read before changing anything

The product docs first. Then `controlplane/backend/CLAUDE.md` for the backend
and `controlplane/frontend/CLAUDE.md` for the frontend.
