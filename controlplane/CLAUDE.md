# CLAUDE.md: controlplane

Where an operator manages a fleet of `hoopinspect` sidecars. Config decided
once and distributed everywhere, sidecars registered and identified.

The root `CLAUDE.md` does not govern this directory. It describes the hoop 1.0
gateway and its modules. This file governs everything under `controlplane/`,
and each component below carries its own.

## This is hoop 2.0

The control plane plus the sidecar **replaces** the gateway. `gateway/`,
`agent/` and `client/` are maintenance and bug fix only.

**Reuse ideas from the old code, not blocks of it.** Those packages carry
patterns this product exists to drop: a Postgres dependency, an agent that
dials a bidirectional gRPC stream, end users who authenticate with us. Copying
a block brings the pattern with it. Read the old implementation, understand
what it solved, then write the version this product wants.

## Non-negotiables

Five rules. Each one has a failure mode you can point at in review.

**1. The control plane going down must not stop traffic.** Sidecars keep
enforcing the last config they received. This is the single most important
property of the system and it needs a test, not a comment. A customer whose
database access dies because our process restarted does not buy the next
renewal.

**2. Nothing here dials a sidecar.** The sidecar always opens the connection.
Customers run sidecars behind NAT and a firewall. A control plane that
initiates the connection needs an inbound rule per sidecar, which does not
work at fleet scale. Teleport agents, Boundary workers and Envoy all dial out
for this reason.

**3. Never fall back to the bootstrap file after the first sync.** If a
sidecar loses the socket and reverts to `bootstrap.yaml`, every rule the
control plane pushed is silently discarded. An admin tightens a masking rule,
the link drops, and enforcement loosens. The attack is cutting the network.
The sidecar caches the last known control plane config and keeps enforcing
that. The file on disk is bootstrap only, forever.

**4. The sidecar's zero-dependency invariant is not ours to break.**
`github.com/hoophq/hoopinspect` at the root is stdlib-only, enforced by test
(`GOWORK=off go list -m all` prints one line, no `go.sum`). It exists to be
vendored without a supply-chain review. Anything the sidecar must link to talk
to us goes in a **nested module** under `hoopinspect/`, the way `config/yaml`,
`store/sqlite` and `pii/alcatraz` already do. Never in the root.

**5. Version skew is permanent.** Customers do not upgrade a fleet in
lockstep. Assume the sidecar on the other end is older than you. New fields
are optional with a safe default. A sidecar that does not understand a message
says so instead of failing quietly. See `transport/CLAUDE.md`.

## Decided

From the architecture session, 2026-08-26.

| Question | Decision | Why |
|---|---|---|
| Transport | WebSocket | gRPC rejected: HTTP/2 through enterprise L7 proxies and load balancers turns into two weeks of work inside the customer. WebSocket rides 443 and traverses almost everything. SSE down plus POST up is the fallback. |
| Config delivery | Database entities over the socket | Files are not a distribution mechanism. The operator writes `bootstrap.yaml` by hand once; the control plane manages the sidecar from then on. |
| Repo | This folder, in the public `hoophq/hoop` repo | Enterprise-only is a runtime license check (`common/license` has `EnterpriseType` and a `Features` list), not a repo boundary. |
| Deployment | Self-hosted first | Most of the ICP self-hosts, and it is easier to control and test. SaaS is a later phase. |
| Binary distribution | Not now | Own binary or `hoop` subcommand is deferred. Do not build for either. |
| Audit ingestion | Out of MVP | Sidecars still spool locally to sqlite. See the gotcha below. |

## Open

- **Sidecar identity bootstrap.** The murkiest part of the design, so
  `sidecarauth/` answers the questions before it writes the code. It still
  ends in shipped code. It blocks nothing else in the MVP: a named development
  token is enough to get the other four components talking. Read
  `sidecarauth/CLAUDE.md` before proposing anything.
- **Whether this folder becomes its own Go module.** A separate `go.mod` added
  to `go.work` keeps the new product off the gateway's dependency tree, which
  is most of the point of 2.0. The cost is a second module to keep in sync.
  Recommended, not decided.
- **License reissue.** Existing customers get a newly generated license with
  no backward compatibility. That needs an owner, a window, and a plan for
  what a customer on an old license sees in the meantime. Not a code task.
- **Who starts sidecars.** Not on the MVP component list. Until someone
  decides otherwise the control plane **adopts** sidecars that already exist
  and never creates them. Confirm before building anything that assumes a
  scheduler.

## Layout

| Path | Owns |
|---|---|
| `transport/` | the WebSocket channel and the message contract every other component speaks |
| `desiredstate/` | what config each sidecar should run, and the generation number |
| `inventory/` | what is actually running: version, applied generation, last check-in |
| `sidecarauth/` | how a sidecar proves who it is, and how it knows we are genuine |
| `adminauth/` | admin signup and signin, humans only |

`transport/` is the contract. Read it before starting any of the other four,
because all of them cross it.

## Gotchas

- **Two sources of truth is the failure this product already knows about.**
  Config lives in the database. Do not add a file-watching path alongside it
  "just for convenience". The bootstrap file is read once at sidecar startup
  and never again.

- **The config document is not ours to invent.** It is
  `hoopinspect/sidecar.Config`, which already exists and already validates
  exhaustively. `desiredstate/` produces one of those. A second schema means
  two validators that disagree, and the disagreement surfaces as a sidecar
  that refuses a config the UI accepted.

- **Ship JSON, not YAML.** `sidecar.Config` is JSON-native; YAML arrives
  through the nested `hoopinspect/config/yaml` module specifically to keep the
  parser out of anything that does not ask for it. Sending YAML over the wire
  forces every sidecar to link that module for no gain.

- **Audit is out of the MVP but the sidecars still write local sqlite.**
  Nothing truncates that spool, so it grows without bound on a long-lived
  sidecar. If sidecars end up as per-user pods, a pod dying also takes
  un-ingested audit with it. Neither is a bug today. Both become one the day
  ingestion ships, so do not let the local writer assume a reader exists.

- **A false positive in an inspection rule is an outage, not a warning.** This
  control plane pushes those rules to the whole fleet at once. Anything that
  can distribute a rule needs a way to distribute it to one sidecar first.
  Staged rollout is not in the MVP; not designing it out is.

## Conventions

- Structured logging through `common/log`. Not `fmt.Println`, not stdlib
  `log`.
- Model and store functions take their handle as a parameter. Do not add a
  package-level database global; the gateway has 44 files doing that and it is
  one of the patterns 2.0 drops.
- Propagate not-found to the caller. The caller decides whether it is an
  error.
- Tests live beside the source. A behaviour an operator can see needs a test
  that fails without it.
- Comments say why. Where the reasoning is not recoverable from the code,
  say it at length; `hoopinspect` sets the register to match.
