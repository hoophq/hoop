# CLAUDE.md: controlplane

Where an operator manages a fleet of `hoopinspect` sidecars. Config decided
once and distributed everywhere, sidecars registered and identified.

This file governs the control plane backend: everything under `controlplane/`
except `frontend/`, which carries its own. The root `CLAUDE.md` does not
govern here. It describes the hoop 1.0 gateway.

One file on purpose. Split it per component once the components exist in code
and the split earns itself.

## This is hoop 2.0

The control plane plus the sidecar **replaces** the gateway. `gateway/`,
`agent/` and `client/` are maintenance and bug fix only.

**Reuse ideas from the old code, not blocks of it.** Those packages carry
patterns this product exists to drop: a Postgres dependency, an agent that
dials a bidirectional gRPC stream, end users who authenticate with us. Copying
a block brings the pattern with it. Read the old implementation, understand
what it solved, then write the version this product wants.

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
sidecar loses the socket and reverts to `bootstrap.yaml`, every rule the
control plane pushed is silently discarded: an admin tightens a masking rule,
the link drops, enforcement loosens. The attack is cutting the network. The
sidecar caches the last known control plane config and keeps enforcing that.
The file on disk is bootstrap only, forever.

**4. The sidecar's zero-dependency invariant is not ours to break.**
`github.com/hoophq/hoopinspect` at the root is stdlib-only, enforced by test
(`GOWORK=off go list -m all` prints one line, no `go.sum`). It exists to be
vendored without a supply-chain review. Anything the sidecar must link to talk
to us goes in a **nested module** under `hoopinspect/`, the way `config/yaml`,
`store/sqlite` and `pii/alcatraz` already do. Never in the root.

**5. Version skew is permanent.** Customers do not upgrade a fleet in
lockstep. Assume the sidecar on the other end is older than you. New fields
are optional with a safe default. A sidecar that does not understand a message
says so instead of failing quietly.

## Decided

From the architecture session, 2026-08-26.

| Question | Decision | Why |
|---|---|---|
| Transport | WebSocket | gRPC rejected: HTTP/2 through enterprise L7 proxies and load balancers turns into two weeks of work inside the customer. WebSocket rides 443 and traverses almost everything. SSE down plus POST up is the fallback. |
| Config delivery | Database entities over the socket | Files are not a distribution mechanism. The operator writes `bootstrap.yaml` by hand once; the control plane manages the sidecar from then on. |
| Repo | This folder, in the public `hoophq/hoop` repo | Enterprise-only is a runtime license check (`common/license` has `EnterpriseType` and a `Features` list), not a repo boundary. |
| Deployment | Self-hosted first | Most of the ICP self-hosts, and it is easier to control and test. SaaS is a later phase. |
| Binary distribution | Not now | Own binary or `hoop` subcommand is deferred. Do not build for either. |
| Audit ingestion | Out of MVP | Sidecars still spool locally to sqlite. See the gotchas. |

## Open

- **Sidecar identity bootstrap.** The murkiest part of the design. It blocks
  nothing else: a named development token is enough to get everything else
  talking. See the `sidecarauth` section below.
- **Whether this folder becomes its own Go module.** A separate `go.mod` added
  to `go.work` keeps the new product off the gateway's dependency tree, which
  is most of the point of 2.0. The cost is a second module to keep in sync.
  Recommended, not decided.
- **License reissue.** Existing customers get a newly generated license with
  no backward compatibility. Needs an owner, a window, and a plan for what a
  customer on an old license sees meanwhile. Not a code task.
- **Who starts sidecars.** Until someone decides otherwise the control plane
  **adopts** sidecars that already exist and never creates them. Confirm
  before building anything that assumes a scheduler.

## The wire contract

**Still a proposal. Sign it off or change it before building against it.**

One WebSocket per sidecar, opened by the sidecar. Every message is a JSON
object with the same envelope:

```json
{"v": 1, "type": "config.apply", "id": "01J...", "re": "01J...", "payload": {}}
```

`re` is the id being answered, and is present only on replies.

| Type | Direction | Means |
|---|---|---|
| `hello` | up | sidecar identifies itself: credential, version, current generation |
| `hello.ok` | down | accepted, session open |
| `hello.reject` | down | refused, with a reason |
| `config.apply` | down | here is your config document |
| `config.ack` | up | applied, and the generation now running |
| `config.nack` | up | refused, with a reason, still running the previous config |
| `status` | up | heartbeat: alive, current generation, counters |
| `unsupported` | both | I do not know this type, naming the id |

Reserved, do not reuse for anything else: `approval.request`,
`approval.result`, `audit.batch`.

Rules:

- **Full documents, not deltas.** A delta stream needs both ends to agree on
  the base, and they will not after a reconnect. Whole config every time.
- **NACK is mandatory.** A sidecar that cannot apply a config says so, with a
  reason, and keeps running the previous one. Silence is the failure mode this
  exists to prevent.
- **Unknown fields are ignored; unknown types get `unsupported`.** This is
  non-negotiable 5 on the wire.
- **`hello` gates everything.** No config flows before `hello.ok`.
- **Reconnect is catch-up, not replay.** The sidecar sends its current
  generation in `hello`; the control plane sends the current config only if it
  differs. Do not queue missed messages.

## Components

### desiredstate

What **should** be running. Done means: an admin creates and edits a config
for a named sidecar, the sidecar receives it, and the store knows which
generation each sidecar was last sent.

- **The document is `hoopinspect/sidecar.Config`.** Do not invent a schema. It
  already exists in `hoopinspect/sidecar/config.go` and already validates
  exhaustively rather than fail-fast. A second definition means two validators
  that disagree, and the disagreement surfaces as a sidecar refusing a config
  the UI accepted.
- **Validate at write time, not at push time.** Otherwise the admin learns
  about the problem from a `config.nack`, late.
- **Ship JSON.** JSON is native to the sidecar at zero dependency cost. YAML
  arrives through the nested `hoopinspect/config/yaml` module specifically to
  keep the parser out of anything that does not ask for it.
- **Generation is monotonic per sidecar.** Bump on every change, never reuse.
- MVP is one config per sidecar, direct mapping. Groups, labels, inheritance
  and staged rollout are later.

### inventory

What is **actually** running: version, applied generation, last check-in.
`desiredstate` holds what should be true. The two disagreeing is the normal
condition, and showing the difference honestly is the product.

Done means: an admin lists every connected sidecar with its version, applied
generation, state and last check-in, and a sidecar reconnecting after a
control plane restart reappears with no manual action.

| State | Means |
|---|---|
| `connected` | socket open, heartbeat current, acked generation equals issued |
| `stale` | socket open, acked generation behind the issued one past the heartbeat window |
| `rejected` | sidecar sent `config.nack`; carry the reason |
| `disconnected` | socket closed, record retained with a last-seen timestamp |

- **Storage is memory.** After a restart the view is empty until sidecars
  reconnect, roughly one backoff window. Say so in the UI rather than showing
  an empty list that reads as an outage. It also rules out two replicas, which
  is fine for single-instance self-hosted and must be revisited before HA.
- **Liveness is the socket, not a poller** (rule 2). The heartbeat is still
  required: TCP holds a half-open connection for a long time, so a vanished
  sidecar looks connected until a `status` fails to arrive.
- **Never report a generation you were not told.** It comes from `config.ack`
  and `hello`, nowhere else. Inferring it from "we sent N so it runs N" is
  exactly what `config.nack` exists to break.
- **NACK reasons are the highest-value field here and the easiest to drop into
  a log line.** They belong in the fleet view.
- **Retain disconnected records briefly.** A sidecar vanishing the instant its
  socket closes makes a restart look like a deletion.

### adminauth

Signup and signin for the humans who administer the control plane. Done means:
on a first run with an empty database the operator creates the first admin,
that admin signs in and out, and no unauthenticated request reaches
`desiredstate` or `inventory`.

**Administration only, never end users.** In hoop 2.0 the end user does not
authenticate with us at all: they connect to their database the way they
already do and the sidecar is transparent on the path. A requirement starting
with "when the end user logs in" is in the wrong product.

Two populations, never one mechanism. Admins get sessions lasting hours, tied
to a human being present, revoked when the person leaves. Sidecars get
short-lived tokens rotated automatically, revoked when the workload is
decommissioned. Never share a token type across the two: a leaked admin
session becoming a credential that can impersonate a sidecar is the failure
this separation exists to prevent.

- **First-run admin creation is the classic hole.** An endpoint that creates
  an admin while the user table is empty must stop working the instant it is
  not empty, and must not be reachable before the operator has had the chance
  to use it. Get this wrong and the first person to find the deployment owns
  it.
- **No roles yet.** One admin role. Auditor and read-only were a gateway
  concept and should be re-derived from what 2.0 needs, not inherited.
- `gateway/idp/` is worth reading for provider resolution and cached
  verifiers, but it is wired to the gateway's Postgres models, request context
  and role middleware. Read it, do not copy it.
- SSO and OIDC come later, and stay administration-only when they land.

### sidecarauth

How a sidecar proves who it is, and how it knows the control plane is genuine.
This carries more open questions than the rest, so it answers them before it
writes code. It still ends in shipped code.

**The question is two questions.** Discovery, what address do I dial, is an
env var or a line in `bootstrap.yaml`: not hard, not the blocker. Trust, how
each side knows the other is genuine, is the hard one, runs both directions,
and **cannot be bootstrapped from nothing.** There must be an anchor. SPIRE
ships `insecure_bootstrap` for testing only and documents plainly that without
an anchor a machine-in-the-middle controls the whole infrastructure. Any
design claiming to need no pre-shared anything is trusting the network.

So the real question is which anchor, and who issues it.

- **One-time-use bootstrap token.** Issued by the control plane, lands as an
  env var, exchanged at first connect for a short-lived credential the sidecar
  then rotates. Expires the moment it is used. Works anywhere. Weakness, and
  the reason SPIRE recommends against it at scale: one token per sidecar, no
  selectors, somebody tracks which token went where. Painful at a thousand
  per-user pods, fine at fifty.
- **Platform attestation.** The platform vouches for the workload, so there is
  no per-sidecar secret: Kubernetes projected service account tokens through
  TokenReview, AWS instance identity documents, GCE identity tokens. The
  literal answer to "without a pre-agreed key", and SPIRE's default. The
  trade: the anchor becomes the platform, so pick the one whose root of trust
  the customer controls. The control plane must also reach the platform's
  verification API, which is a deployment constraint, not only a code one.

Already settled, do not relitigate: JWT with rotation is the right shape for
the credential **after** bootstrap and does not solve bootstrap; `hello` is
where this hooks into the wire; and it is mutual, not one-way, because a
sidecar that accepts policy from any server answering on that address is a
sidecar an attacker can disarm.

Prior art here: `gateway/cmd/spiffe-mint/`, `scripts/local-spiffe-agent.sh`,
`scripts/local-spiffe-kubernetes.sh`, and `common/dsnkeys/`. Matheus has this
flow in their head already and is the first conversation, not the last.

Ship in order: **(1)** the written decision, covering which anchor, who issues
it, rotation, what happens when a credential expires mid-connection, and how a
sidecar is revoked (revocation is the one usually forgotten and the first
thing a security review asks about). It picks between real alternatives and is
expensive to reverse, so check `docs/adr/README.md` and land it as an ADR if
it fits. **(2)** The credential path with a **named development anchor**: one
static shared token behind the interface the real anchor will implement, so it
swaps in without touching callers. It must announce what it is, with a config
key that says `dev_token`, a warn-level startup log line, and a refusal to run
when the deployment is marked production. Not a silent default that quietly
becomes the shipping behaviour, which is how a placeholder turns into a CVE.
**(3)** The real anchor, after sign-off, as its own change.

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

- Structured logging through `common/log`. Not `fmt.Println`, not stdlib
  `log`.
- Store functions take their handle as a parameter. No package-level database
  global; the gateway has 44 files doing that and it is one of the patterns
  2.0 drops.
- Propagate not-found to the caller. The caller decides whether it is an
  error.
- Tests live beside the source. A behaviour an operator can see needs a test
  that fails without it.
- Comments say why. Where the reasoning is not recoverable from the code, say
  it at length; `hoopinspect` sets the register to match.
