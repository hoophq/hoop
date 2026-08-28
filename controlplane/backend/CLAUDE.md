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

From the architecture session:

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

## Open

- **Sidecar identity bootstrap.** The murkiest part of the design. It blocks
  nothing else: a named development token is enough to get everything else
  talking.
- **License reissue.** Existing customers get a newly generated license with
  no backward compatibility. Needs an owner, a window, and a plan for what a
  customer on an old license sees meanwhile. Not a code task.
- **Who starts sidecars.** Until someone decides otherwise the control plane
  **adopts** sidecars that already exist and never creates them. Confirm
  before building anything that assumes a scheduler.
- **HA.** Inventory is in-memory and migrations run in-process at boot, so
  this is single-instance today. Both have to change together; changing one
  alone buys nothing. Polling already removed the third blocker: with no
  session there is no affinity, so any replica can answer any request.

## The sidecar contract

**Still a proposal. Sign it off or change it before building against it.**

Two endpoints, both called by the sidecar, both ordinary HTTP. Nothing is
pushed.

```
GET  /v1/desiredstate    If-None-Match: "<generation>"    200 + config + ETag, or 304
POST /v1/status          roughly every 30s, jittered       204
```

Both carry the sidecar's credential. Both take the sidecar's name **from that
credential**, never from a path, a query or a body: `sidecarauth.Anchor.Verify`
returns a name for this reason, and a handler that reads one from the request
lets any enrolled sidecar read or overwrite any other one's state.

### Why polling, after two other answers

gRPC went first. HTTP/2 through enterprise L7 proxies and load balancers turns
into two weeks of work inside the customer.

WebSocket replaced it and held the decision for a while: one long-lived socket
per sidecar, riding 443, traversing almost everything.

Polling replaced that, and this is the part worth keeping. A socket buys push
latency, and it costs session affinity, reconnect and backoff handling on both
ends, a heartbeat to detect the half-open connections TCP hides, and a server
that is stateful for as long as the fleet is connected. None of that is paid
back where a config change landing within 30 seconds is already faster than
the human who made it. A stateless request also survives a load balancer, a
restart and a second replica with no work at all, which is most of what stands
between this design and HA.

Push is not gone forever. An approval flow needs it, and long-poll or SSE over
this same prefix is the upgrade path. It is not in the MVP and the scaffold
does not pretend it is.

### Rules

- **The generation is the ETag, and lives nowhere else.** Not a key inside the
  config document. `LoadConfigBytes` calls `DisallowUnknownFields`
  (`sidecar/daemon/config.go:334`) so a typo cannot silently disable a control,
  which means one extra key fails the whole document and leaves the sidecar
  running the previous config until somebody notices. The document we serve is
  the document the sidecar's own parser accepts, byte for byte.
- **Full documents, never deltas.** A delta needs both ends to agree on a base,
  and after a restart on either side they do not.
- **Refusal is reported, never silent.** A sidecar that cannot apply a config
  says so in its next status, with a reason, and keeps running the previous
  one. Silence is the failure this contract exists to prevent, and the reason
  is the highest-value field in the fleet view.
- **Unknown fields are ignored.** Non-negotiable 5, on the request and on the
  response.
- **Catch-up, not replay.** The sidecar always asks with the generation it is
  running. There is no queue of missed messages to get wrong, which is the one
  thing polling makes free.
- **Jitter the interval.** Sidecars started by one rollout otherwise align and
  arrive as a single spike per interval, and the spike grows with the fleet.
- **An error is not a config change.** A 5xx, a timeout or a 401 means the
  sidecar keeps running what it has and retries with backoff. It never falls
  back to `bootstrap.yaml`; that is non-negotiable 3.

### What this costs

Written down because both are real and neither should have to be rediscovered.

- **A change reaches the fleet in up to one interval.** 30 seconds, plus
  however long the sidecar takes to apply it. The UI must not report a write as
  deployed; it reports it as issued, and the fleet view is what says whether it
  landed.
- **Authentication runs once per request, not once per session.** Strictly more
  verification work than a socket that checks a credential at connect. If the
  anchor is a network call to a platform API, cache the result with a short TTL.
  Do not skip the check.

## The API

### Conventions

- `gin.New()`, never `gin.Default()`. Default installs Gin's own unstructured
  logger and recovery alongside ours.
- All routes in one `routes` method, read top to bottom. The gateway does the
  same at roughly 1250 lines; this stays readable much longer.
- **Two prefixes, `/api` and `/v1`, and every route is under one of them.** The
  guard follows from the prefix: `RequireAdmin` on `/api`, `RequireSidecar` or
  `RequireBootstrap` on `/v1`. A route outside both fails a test.
- **A route is closed unless it is in `api`'s `unauthenticatedRoutes`.**
  That set is data, not prose, because the test that asserts every other
  route is closed derives its list from `engine.Routes()` minus this set. A
  route registered outside a guarded group therefore fails a test instead
  of quietly joining the open set. Four entries, and enrolment is not one of
  them: it authenticates with a bootstrap credential, which is authenticated
  differently, not unauthenticated.
- **`api.Deps` is the whole dependency graph, built in `main`.** Constructor
  injection, assembled in one place, no container and no reflection. `routes`
  mounts handlers it was given rather than constructing them, so giving a
  component a store changes `main` and no signature under `internal/api`.
  `api.New` refuses a `Deps` with a hole in it and names the field, because
  every one of them is otherwise a nil dereference inside gin on whichever
  request arrives first.
- **A consumer declares the interface it needs.** `api.Readiness` is one
  method, `Ping`, satisfied by `database.Pinger`. Holding that instead of a
  `*gorm.DB` keeps gorm out of `internal/api` and is what makes the `/readyz`
  503 branch testable at all: a `*gorm.DB` cannot be made to fail a ping
  without a real database.
- **CORS is a strict allow list, empty by default.** The gateway answers
  `Access-Control-Allow-Origin: *` with `Allow-Credentials: true`, a pairing
  browsers reject and which would be unsafe if they honoured it. A development
  machine opts in with `CONTROLPLANE_CORS_ALLOWED_ORIGINS`. There is a test
  that fails if a wildcard ever appears.
- **Never log a query string or a request body.** Both routinely carry
  credentials here, and a log line is the easiest place in a system to retain
  one forever. A payload type that could be logged whole must redact itself
  through `slog.LogValuer`. There is no worked example in the module yet; the
  first type that carries a credential writes one.
- **All four server timeouts are set**, plus a per-request deadline on both
  route groups. Every request this service serves has a defined length. An
  earlier version left `ReadTimeout` and `WriteTimeout` unset to protect a
  long-lived WebSocket, since a hijacked connection inherits the deadlines
  net/http already set on it; there is no socket now, and keeping them unset
  without that reason is just a slow-client hole. If a later endpoint streams,
  long-poll or SSE for approvals is the candidate, it clears its own deadline
  with `http.NewResponseController` rather than unsetting these for everyone.
- Liveness and readiness are separate endpoints and mean opposite things.
  Never put a database check in liveness: a database blip then restarts every
  replica at once, turning a recoverable dependency failure into an outage.
- No `NoRoute` handler. This binary serves no UI, so an unmatched path is a
  mistake and must look like one.

### The 501 contract

A stub in this scaffold returns `501 Not Implemented` naming the behaviour
that is missing. It does not return an empty list, a zero struct, or
`nil, nil`.

This is test 3 from the root `CLAUDE.md`, and the reason is that an empty
result is indistinguishable from a working feature with nothing in it. A fleet
view that returns `[]` because nobody wrote it yet reads as "no sidecars are
checking in", and somebody acts on that.

Use `apierr.NotImplemented(c, what)`. When you implement a stub, delete the
call. Do not leave it behind a flag.

`what` describes the missing behaviour and nothing else. No ticket ID: the
response is public, it is read by an operator who cannot open our tracker, and
the description is the part that tells them whether they misconfigured
something or hit an unbuilt route. Ticket IDs stay in the package comments,
which is where somebody working on the code will look.

The two route tests compare that description exactly, so it also identifies
which handler answered. A test fails if two handlers share one.

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
