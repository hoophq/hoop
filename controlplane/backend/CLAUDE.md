# Control Plane Backend

Manages a fleet of hoop sidecars (`sidecar/` in this repo): config decided
once, distributed everywhere; sidecars registered and identified.

This file governs `controlplane/backend`. The root `CLAUDE.md` describes the
gateway and does not apply here; disagreements are deliberate and explained
below. One file on purpose — split only when components exist in code.

## Relationship to the gateway

Separate products. Nothing here imports `gateway/`, `agent/` or `client/`.

**Reuse ideas, not blocks of code.** Those packages carry patterns this
product exists to drop (bidirectional gRPC agent, end-user auth, a four-year
dependency tree). Copying a block brings the pattern with it.

## Conventions

- Structured logging via injected `*slog.Logger`. No `fmt.Println`, stdlib
  `log`, or package-level loggers (`internal/logging` explains the
  divergence from root).
- Store functions take their handle as a parameter; no package-level DB
  global.
- Config is a value passed down from `main`; no global.
- Propagate not-found; the caller decides if it is an error.
- Tests live beside the source. Operator-visible behaviour needs a test that
  fails without it.
- Keep code comments short and direct; prefer concise comments over long explanatory blocks.

## Non-negotiables

**1. Control plane down must not stop traffic.** Sidecars keep enforcing the
last config received. Needs a test, not a comment.

**2. Nothing here dials a sidecar.** Sidecars dial out (NAT/firewall; same as
Teleport, Boundary, Envoy).

**3. Never fall back to the bootstrap file after the first sync.** Reverting
to `bootstrap.yaml` silently discards every pushed rule — cutting the network
becomes the attack. The sidecar caches the last control plane config and
keeps enforcing it. The file on disk is bootstrap only, forever.

**4. Don't break the sidecar's dependency invariant.**
`github.com/hoophq/hoop/sidecar` root has exactly one dependency,
`github.com/hoophq/libhoop` — a ceiling, not a starting point. Heavy deps
live in nested modules (`config/yaml`, `store/sqlite`, `pii/alcatraz`,
`analyzer/vertex`, `cmd`, `lexer/conformance`). Anything the sidecar links to
talk to us goes in a nested module, never the root.
Corrections to an earlier draft:

- The root module is not stdlib-only and does have a `go.sum` (libhoop
  arrived with the protocol codecs).
- No test enforces the ceiling — only `go.mod` and review. Write that test if
  you want it; don't assume it exists.

libhoop is private: importing `sidecar/` needs
`GOPRIVATE=github.com/hoophq/libhoop` plus credentials. That is why the
control plane does not import it.

**This backend is its own module for the same reason.** `gateway/go.mod`
reaches the Anthropic SDK, OpenAI SDK, `k8s.io/api`, go-git — none needed by
a config distributor. New deps in `backend/go.mod` need a written reason.
`github.com/hoophq/hoop/common` is deliberately absent; `internal/logging`'s
package comment explains the stdlib logger.

**5. Version skew is permanent.** Assume the sidecar is older than you. New
fields optional with safe defaults; an uncomprehending sidecar says so
instead of failing quietly.

## Decided

| Question | Decision | Why |
| --- | --- | --- |
| Transport | HTTP, polled by the sidecar | The sidecar uses the API endpoints to communication |
| API shape | `/api` admins, `/v1` sidecars | Two audiences, two credentials; the guard is readable from the path. `/v1` versioned (fleet skew); `/api` not (frontend ships with backend). |
| Config delivery | Database entities | Files are not a distribution mechanism. `bootstrap.yaml` is written once by hand in the sidecar. |
| Repo | This folder, public `hoophq/hoop` | Enterprise-only is a runtime license check (`common/license`), not a repo boundary. |
| Deployment | Self-hosted first | Most of the ICP self-hosts. SaaS later. |
| Binary distribution | Not now | `make build-controlplane` is developer-only, not wired into a release. |
| Audit ingestion | Out of MVP | Sidecars still spool to local sqlite. See gotchas. |
| Own Go module | Yes | |
| Datastore | Postgres, GORM over pgx/v5 | Same engine/driver as the gateway — one database during the transition. Not the gateway's patterns. |
| Schema changes | golang-migrate, numbered SQL, embedded | No `AutoMigrate`: no down migrations, silent no-op on column rename. |
| Sidecar identity | Token, presented by the sidecar on connect | Fits rule 2: the sidecar dials out with its credential. |
| License | Reissued | New license, no backward compatibility. Rollout is operational, not a code task. |
| Who starts sidecars | A person | No scheduler. The control plane adopts existing sidecars, never creates them. |

## Gotchas

- **Two sources of truth is the known failure.** Config lives in the
  database. No file-watching path "for convenience"; bootstrap is read once
  at startup.
- **Audit is out of MVP but sidecars still write local sqlite.** Nothing
  truncates the spool; per-user pods dying lose un-ingested audit. Not bugs
  today — both become bugs when ingestion ships. Don't let the writer assume
  a reader.
- **A false positive in an inspection rule is an outage.** Rules hit the
  whole fleet at once. Staged rollout is out of the MVP; designing it out is
  not.
