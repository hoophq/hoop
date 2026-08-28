# Control Plane Backend

Where an operator manages a fleet of hoop sidecars, the `sidecar/` module in
this repository. Config decided once and distributed everywhere, sidecars
registered and identified.

This file governs `controlplane/backend`. The root `CLAUDE.md` does not
govern here: it describes the gateway, and several of its conventions are ones
this product exists to drop. Where the two disagree,
the disagreement is deliberate and the reason is written down below.

One file on purpose. Split it per component once the components exist in code
and the split earns itself.

## Relationship to the gateway

Separate products with separate constraints. `gateway/`, `agent/` and
`client/` are not part of this one, and nothing here imports them.

**Reuse ideas from that code, not blocks of it.** Those packages carry
patterns this product exists to drop: an agent that dials a bidirectional gRPC
stream, end users who authenticate with us, a dependency tree grown over four
years. Copying a block brings the pattern with it. Read the existing
implementation, understand what it solved, then write the version this product
wants.

## Non-negotiables

Five rules. Each has a failure mode you can point at in review.

**1. The control plane going down must not stop traffic.** Sidecars keep
enforcing the last config they received. This needs a test, not a comment. A
customer whose database access dies because our process restarted does not buy
the next renewal.

**2. Nothing here dials a sidecar.** The sidecar always opens the connection.
Customers run sidecars behind NAT and a firewall, so an inbound rule per
sidecar does not work at fleet scale. Teleport agents, Boundary workers and
Envoy all dial out for this reason.

**3. Never fall back to the bootstrap file after the first sync.** If a
sidecar loses its control plane connection and reverts to `bootstrap.yaml`,
every rule the control plane pushed is silently discarded: an admin tightens
a masking rule, the link drops, enforcement loosens. The attack is cutting
the network. The sidecar caches the last known control plane config and
keeps enforcing that. The file on disk is bootstrap only, forever.

**4. The sidecar's dependency invariant is not ours to break.**
`github.com/hoophq/hoop/sidecar` at the root has **exactly one** dependency,
`github.com/hoophq/libhoop`, and that is a deliberate ceiling, not a starting
point. It used to have none. The heavier dependencies live in six nested
modules (`config/yaml`, `store/sqlite`, `pii/alcatraz`, `analyzer/vertex`,
`cmd`, `lexer/conformance`) so that nothing links a YAML parser or a sqlite
driver unless it asks for one. Anything the sidecar must link to talk to us
goes in a nested module, the same way. Never in the root.

Two corrections to what an earlier draft of this file claimed, because both
would send you looking for something that is not there:

- The root module is **not** stdlib-only and **does** have a `go.sum`. It
  gained libhoop when the protocol codecs moved there.
- **No test enforces the ceiling.** The boundary is the `go.mod` file and
  human review. `make test-sidecar` walks each nested module in its own
  directory, which catches a missing `require`, not a new dependency someone
  added on purpose. If you want the invariant enforced, write that test; do
  not assume it exists.

libhoop is private, so building or testing anything that imports `sidecar/`
needs `GOPRIVATE=github.com/hoophq/libhoop` and credentials for that
repository. This is why the control plane does not import it.

**This backend is its own module for the same reason.** It does not inherit
the gateway's dependency graph, and that graph is the thing this product
exists to avoid: `gateway/go.mod` reaches the Anthropic SDK, the OpenAI SDK,
`k8s.io/api` and go-git, none of which a config distributor needs. A
dependency added to `backend/go.mod` needs a reason written down, the same way
the sidecar module guards its own boundary. Notably absent is
`github.com/hoophq/hoop/common`; `internal/logging`'s package comment says why
the logger is stdlib instead.

**5. Version skew is permanent.** Customers do not upgrade a fleet in
lockstep. Assume the sidecar on the other end is older than you. New fields
are optional with a safe default. A sidecar that does not understand a message
says so instead of failing quietly.

## Decided

From the architecture session, plus the follow-up that closed the last four
open items (sidecar identity through HA):

| Question | Decision | Why |
|---|---|---|
| Transport | HTTP, polled by the sidecar | See "The sidecar contract". This reverses an earlier decision for WebSocket, which itself replaced gRPC; the reasoning for all three is in that section, because a reversal nobody can reconstruct gets reversed again. |
| API shape | Two prefixes: `/api` for admins, `/v1` for sidecars | Two audiences holding two kinds of credential. Splitting on the prefix makes the guard readable from the path alone, in a log line and in a proxy rule. `/v1` is versioned because a fleet does not upgrade in lockstep; `/api` is not, because the frontend ships with the backend. |
| Config delivery | Database entities | Files are not a distribution mechanism. The operator writes `bootstrap.yaml` by hand once; the control plane manages the sidecar from then on. |
| Repo | This folder, in the public `hoophq/hoop` repo | Enterprise-only is a runtime license check (`common/license` has `EnterpriseType` and a `Features` list), not a repo boundary. |
| Deployment | Self-hosted first | Most of the ICP self-hosts, and it is easier to control and test. SaaS is a later phase. |
| Binary distribution | Not now | Own binary or `hoop` subcommand is deferred. `make build-controlplane` exists for developers; it is not wired into a release. |
| Audit ingestion | Out of MVP | Sidecars still spool locally to sqlite. See the gotchas. |
| Own Go module | Yes | Was open. |
| Datastore | Postgres, GORM over pgx/v5 | Same engine and driver as the gateway, so it is one database to operate during the transition and a stack the team already runs. The patterns around it are not the gateway's. |
| Schema changes | golang-migrate, numbered SQL, embedded | No `AutoMigrate`, anywhere: it cannot express a down migration and it silently does nothing on a column rename. |
| Sidecar identity | A token, presented by the sidecar when it connects to the control plane | Was the murkiest open item. A token fits rule 2: the sidecar dials out and brings its credential with it, so no inbound trust setup per sidecar. |
| License | Reissued | Existing customers get a newly generated license with no backward compatibility. Rollout (owner, window, what an old license sees meanwhile) is operational, not a code task. |
| Who starts sidecars | A person | No scheduler. The control plane adopts sidecars that already exist and never creates them. |

## Gotchas

- **Two sources of truth is the failure this product already knows about.**
  Config lives in the database. Do not add a file-watching path alongside it
  "just for convenience". The bootstrap file is read once at sidecar startup
  and never again.

- **Audit is out of the MVP but sidecars still write local sqlite.** Nothing
  truncates that spool, so it grows without bound on a long-lived sidecar. If
  sidecars end up as per-user pods, a pod dying also takes un-ingested audit
  with it. Neither is a bug today. Both become one the day ingestion ships, so
  do not let the local writer assume a reader exists.

- **A false positive in an inspection rule is an outage, not a warning.** This
  control plane pushes rules to the whole fleet at once. Staged rollout is not
  in the MVP; not designing it out is.

## Conventions

- Structured logging through an injected `*slog.Logger`. Never `fmt.Println`,
  never stdlib `log`, never a package-level logger. `internal/logging`'s
  package comment says why this differs from the root convention.
- Store functions take their handle as a parameter. No package-level database
  global.
- Config is a value passed down from `main`. No package-level config global.
- Propagate not-found to the caller. The caller decides whether it is an
  error.
- Tests live beside the source. A behaviour an operator can see needs a test
  that fails without it.
