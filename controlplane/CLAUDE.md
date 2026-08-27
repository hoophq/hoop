# CLAUDE.md: controlplane

Where an operator manages a fleet of hoop sidecars, the `sidecar/` module in
this repository. Config decided once and distributed everywhere, sidecars
registered and identified.

This file governs `controlplane/backend`. `controlplane/frontend` carries its
own. The root `CLAUDE.md` does not govern here: it describes the gateway, and
several of its conventions are ones this product exists to drop. Where the two
disagree, the disagreement is deliberate and the reason is written down below.

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
sidecar loses the socket and reverts to `bootstrap.yaml`, every rule the
control plane pushed is silently discarded: an admin tightens a masking rule,
the link drops, enforcement loosens. The attack is cutting the network. The
sidecar caches the last known control plane config and keeps enforcing that.
The file on disk is bootstrap only, forever.

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
repository. This is why the control plane does not import it. See
"Module boundary".

**5. Version skew is permanent.** Customers do not upgrade a fleet in
lockstep. Assume the sidecar on the other end is older than you. New fields
are optional with a safe default. A sidecar that does not understand a message
says so instead of failing quietly.

## Decided

From the architecture session, 2026-08-26, plus the scaffold decisions in
EVL-230.

| Question | Decision | Why |
|---|---|---|
| Transport | WebSocket | gRPC rejected: HTTP/2 through enterprise L7 proxies and load balancers turns into two weeks of work inside the customer. WebSocket rides 443 and traverses almost everything. SSE down plus POST up is the fallback. |
| Config delivery | Database entities over the socket | Files are not a distribution mechanism. The operator writes `bootstrap.yaml` by hand once; the control plane manages the sidecar from then on. |
| Repo | This folder, in the public `hoophq/hoop` repo | Enterprise-only is a runtime license check (`common/license` has `EnterpriseType` and a `Features` list), not a repo boundary. |
| Deployment | Self-hosted first | Most of the ICP self-hosts, and it is easier to control and test. SaaS is a later phase. |
| Binary distribution | Not now | Own binary or `hoop` subcommand is deferred. `make build-controlplane` exists for developers; it is not wired into a release. |
| Audit ingestion | Out of MVP | Sidecars still spool locally to sqlite. See the gotchas. |
| Own Go module | Yes | Was open. Resolved in EVL-230, see "Module boundary". |
| Where the code lives | `controlplane/backend`, beside `controlplane/frontend` | Two applications, deployed separately, neither serving the other. A `go.mod` at `controlplane/` with a React app inside it says otherwise. |
| Datastore | Postgres, GORM over pgx/v5 | Same engine and driver as the gateway, so it is one database to operate during the transition and a stack the team already runs. The patterns around it are not the gateway's: see "Persistence". |
| Schema changes | golang-migrate, numbered SQL, embedded | No `AutoMigrate`, anywhere. See "Schema". |
| Serving the UI | Not from this binary | `controlplane/frontend` is served separately. This is a JSON API with a `default-src 'none'` CSP, and it stays that way until someone decides otherwise. |

## Open

- **Sidecar identity bootstrap.** The murkiest part of the design. It blocks
  nothing else: a named development token is enough to get everything else
  talking. See the `sidecarauth` section below.
- **License reissue.** Existing customers get a newly generated license with
  no backward compatibility. Needs an owner, a window, and a plan for what a
  customer on an old license sees meanwhile. Not a code task.
- **Who starts sidecars.** Until someone decides otherwise the control plane
  **adopts** sidecars that already exist and never creates them. Confirm
  before building anything that assumes a scheduler.
- **HA.** Inventory is in-memory and migrations run in-process at boot, so
  this is single-instance today. Both have to change together; changing one
  alone buys nothing.

## The wire contract

**Still a proposal. Sign it off or change it before building against it.**

It is written as Go in `backend/internal/wire`, not as prose, so the four
component workstreams share one definition instead of four compatible
guesses. Change it there, once.

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
- **Generation travels in the envelope, never inside the config document.**
  `LoadConfigBytes` calls `DisallowUnknownFields`
  (`sidecar/daemon/config.go:334`) so a typo cannot silently disable a
  control. One extra key therefore fails the whole document. Nobody had
  written this down; `wire.ConfigApplyPayload` now encodes it and
  `wire/envelope_test.go` fails if it regresses.

## Components

Each has a stub package under `backend/internal/`, whose package comment
carries the constraints below in the place an implementer will actually read
them. Every handler answers 501 naming its ticket. See "The 501 contract".

A component package is one directory holding its model, its store and its
handlers together. Not a `models/` directory, a `services/` directory and an
`api/` directory that each grow a file per feature. Adding a field to a
sidecar record touches `internal/inventory` and nothing else, and the compiler
tells you when a component reaches into another one's internals because it has
to do it through an exported name.

### desiredstate (EVL-231)

What **should** be running. Done means: an admin creates and edits a config
for a named sidecar, the sidecar receives it, and the store knows which
generation each sidecar was last sent.

- **The document is `sidecar/daemon.Config`.** Do not invent a schema. It
  already exists in `sidecar/daemon/config.go` and already validates
  exhaustively rather than fail-fast. A second definition means two validators
  that disagree, and the disagreement surfaces as a sidecar refusing a config
  the UI accepted.
- **Validate at write time, not at push time.** Otherwise the admin learns
  about the problem from a `config.nack`, minutes later, from a different
  screen.
- **But `Config.Validate` is not a pure function**, and an earlier draft of
  this file implied it was. It calls `BuildDownstreamTLS`, which reads
  certificate and key files from disk (`config.go:443`), against paths that
  resolve on the **sidecar's** filesystem and not on ours. Server-side
  validation cannot be a straight call to it. Working out what the control
  plane can honestly check is part of EVL-231, not an afterthought.
- **Ship JSON.** JSON is native to the sidecar at zero dependency cost. YAML
  arrives through the nested `sidecar/config/yaml` module specifically to keep
  the parser out of anything that does not ask for it.
- **Generation is monotonic per sidecar.** Bump on every change, never reuse.
- MVP is one config per sidecar, direct mapping. Groups, labels, inheritance
  and staged rollout are later.

### inventory (EVL-232)

What is **actually** running: version, applied generation, last check-in.
`desiredstate` holds what should be true. The two disagreeing is the normal
condition, and showing the difference honestly is the product.

Done means: an admin lists every connected sidecar with its version, applied
generation, state and last check-in, and a sidecar reconnecting after a
control plane restart reappears with no manual action.

The four states are `inventory.State` in Go, defined in the stub rather than
deferred, because the frontend and the wire layer both name them and three
components agreeing on four strings by convention is three chances to
disagree.

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

### adminauth (EVL-233)

Signup and signin for the humans who administer the control plane. Done means:
on a first run with an empty database the operator creates the first admin,
that admin signs in and out, and no unauthenticated request reaches
`desiredstate` or `inventory`.

**Administration only, never end users.** The end user does not authenticate
with us at all: they connect to their database the way they already do and the
sidecar is transparent on the path. A requirement starting
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
  it. Note that "check the table is empty, then insert" is two statements and
  therefore a race: it needs a constraint the database enforces, not a check
  the handler performs.
- **No roles yet.** One admin role. Auditor and read-only were a gateway
  concept and should be re-derived from what this product needs, not
  inherited.
- **Sessions do not live in memory** even though inventory does, or every
  restart signs every admin out mid-task.
- `gateway/idp/` is worth reading for provider resolution and cached
  verifiers, but it is wired to the gateway's Postgres models, request context
  and role middleware. Read it, do not copy it.
- SSO and OIDC come later, and stay administration-only when they land.

`RequireAdmin` is mounted today and answers 501, so every protected route is
**closed** rather than open while this ticket is outstanding. That is
deliberate and it does mean EVL-231 and EVL-232 cannot be exercised through
the router until this lands: test those handlers directly. Do not "unblock"
yourself by making the middleware call `c.Next()`. That version compiles,
passes every test, and ships an unauthenticated config store.

### sidecarauth (EVL-234)

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

The seam is declared: `sidecarauth.Anchor`. `Verify` returns the sidecar name
rather than a boolean, because a sidecar **asserts** its name in `hello` and a
verifier that only says yes or no lets any authenticated sidecar claim any
other's config.

Prior art here: `gateway/cmd/spiffe-mint/`, `scripts/local-spiffe-agent.sh`,
`scripts/local-spiffe-kubernetes.sh`, and `common/dsnkeys/`.

Ship in order: **(1)** the written decision, covering which anchor, who issues
it, rotation, what happens when a credential expires mid-connection, and how a
sidecar is revoked (revocation is the one usually forgotten and the first
thing a security review asks about). It picks between real alternatives and is
expensive to reverse, so check `docs/adr/README.md` and land it as an ADR if
it fits. **(2)** The credential path with a **named development anchor**: one
static shared token behind `Anchor`, so the real one swaps in without touching
callers. It must announce what it is, with a config key that says `dev_token`,
a warn-level startup log line, and a refusal to run when the deployment is
marked production. `config.Config.IsProduction` exists for that refusal and
for nothing else; it is already wired. Not a silent default that quietly
becomes the shipping behaviour, which is how a placeholder turns into a CVE.
**(3)** The real anchor, after sign-off, as its own change.

## The Go scaffold

```
controlplane/
  backend/                          the Go module, everything below is inside it
    go.mod                          see "Module boundary"
    cmd/controlplane/               entrypoint: serve and migrate
    internal/config/                env-first config, fails fast
    internal/logging/               slog setup, see "Logging"
    internal/database/              pool lifecycle and shared column types
    internal/migrations/            numbered SQL, go:embed, and the runner
    internal/wire/                  the message vocabulary, no transport
    internal/httpapi/               engine, middleware, routes, health
    internal/apierr/                error shapes, own package to avoid a cycle
    internal/desiredstate/          EVL-231
    internal/inventory/             EVL-232
    internal/adminauth/             EVL-233
    internal/sidecarauth/           EVL-234
  frontend/                         React app, governed by its own CLAUDE.md
```

Two rules hold this shape up.

**Package by feature, not by layer.** A component owns its model, its store
and its handlers in one directory. The gateway splits the same feature across
`models/`, `services/` and `api/`, so a field addition is a four-file change
and every one of those directories grows without bound. The counter-argument
is that a layer split makes the layers swappable; nobody has ever swapped
them.

**Everything is under `internal/`.** Nothing outside this module can import
any of it, and that is the compiler enforcing the boundary rather than a
convention asking for it. Move a package out of `internal/` only when
something outside genuinely imports it, and expect to be asked what.

Run it: `POSTGRES_DB_URI=... go run ./cmd/controlplane`, or
`make build-controlplane`. Nothing else is required; every other variable has
a documented default. Full list in `controlplane/README.md`.

Verify a change with
`cd controlplane/backend && go build ./... && go vet ./... && go test ./...`.
CI reaches these tests through `make test-oss`, because
`go test github.com/hoophq/hoop/...` matches this module now that it is in
`go.work`. No separate Makefile test target, and do not add one.

### Module boundary

Its own `go.mod`, in `go.work`. This was the open question in the draft; it is
now decided, for two reasons that turned out to be the same reason.

The gateway's module pulls in the Anthropic and OpenAI SDKs, the Mongo driver,
`k8s.io/api`, go-git, Sentry and Honeycomb. Staying off that tree is most of
the point of a separate module, and sharing one makes it impossible by
construction.

**This module deliberately does not depend on `github.com/hoophq/hoop/common`,
and must not start.** Not because `common` is bad, but because importing one
package from it drags the whole module's requirements in, and that is the tree
we just left. If you need something from `common`, copy the twenty lines or
write the ten you actually use.

It also does not depend on `sidecar/`, which would transitively require
private libhoop credentials to build the control plane. `wire.ConfigApplyPayload.Config`
is `json.RawMessage` for exactly this reason. EVL-231 owns validation and may
decide to pay that cost; the envelope does not have to.

### Dependencies

Five direct requirements: Gin, GORM, the GORM Postgres driver, pgx/v5 and
golang-migrate. Everything else is stdlib.

A new direct dependency needs a reason in the commit message. That is the only
mechanism keeping this list short, and the gateway is what the list looks like
without one.

### Logging

Stdlib `log/slog`, and no wrapper package. `internal/logging` builds a
`*slog.Logger` from `LOG_LEVEL` and `LOG_FORMAT`; that is all it does.

**This is a deliberate deviation from the root convention of `common/log`.**
`common/log` is zap inside `common`, so importing it re-imports the dependency
tree this module exists to avoid. `sidecar/` passes `*slog.Logger` around, so
slog is also what the sidecar side speaks.

An earlier draft of this file wrapped slog and claimed `tunnel/` as precedent.
The wrapper is gone, and so is the claim: `tunnel/` imports
`github.com/hoophq/hoop/common/log`, the zap one. Do not go looking for a slog
precedent in this repository, because there is not one outside `sidecar/`.

**The logger is a parameter, never a package-level variable.** Every
constructor that logs takes `*slog.Logger`. A package-level logger is a global
with an init order, it cannot be swapped in a test, and it is how a test suite
ends up writing to stderr. `main` calls `slog.SetDefault` once so that a
library logging through the default handler is at least formatted the same
way; nothing in this module reads it.

### Persistence

- GORM over pgx/v5, opened through `database.Open`, which returns the handle.
  `SetMaxOpenConns` is reachable because the pool is configured on the
  underlying `*sql.DB`.
- **No package-level `*gorm.DB` global.** Callers thread the handle through.
  The gateway has 44 files reading a global and the root `CLAUDE.md` lists it
  as a pattern not to copy; there is no global here, so the mistake is not
  available to make.
- **Propagate `gorm.ErrRecordNotFound`.** The caller decides whether a missing
  row is an error. Older helpers in `gateway/models/connections.go` return
  `nil, nil`; do not copy that.
- **No sentinel error for a duplicate row.** GORM's `TranslateError` is on, so
  a unique violation arrives as `gorm.ErrDuplicatedKey` already. Declaring our
  own alongside it gives callers two things to check and one of them will be
  missed.
- Schema is `controlplane`, not the gateway's `private`. The two products
  share a database during the transition and a shared schema means a migration
  in one can collide with a table in the other.
- Use `database.Timestamps`, not `gorm.Model`. The latter also brings a uint
  autoincrement primary key and a `DeletedAt` that turns every query into a
  soft-delete query. No table in this module wants either.
- **`database.ParseURI` is the only place a Postgres URI is parsed.** Both
  `config` and `migrations` call it. It exists because `net/url` puts the
  whole URL, credentials included, into the text of a parse error, so a
  password containing a stray `%` lands in the log of every failed start. A
  test covers that.

### Schema

Numbered SQL under `backend/internal/migrations`, embedded with `go:embed`,
run by golang-migrate.

- **`AutoMigrate` is called nowhere in this repository and must stay that
  way.** It cannot express a down migration and it silently does nothing on a
  column rename, which turns a rename into data loss no review catches.
- Create one with
  `migrate create -ext sql -dir controlplane/backend/internal/migrations -seq <description>`.
- **Read the next number from the directory listing, not from a count.** Both
  `.up.sql` and `.down.sql`, always. A test fails if either is missing, and
  another fails if the sequence gains a gap, because `Verify` compares the
  applied version against the highest embedded one.
- The migrations table is `controlplane_schema_migrations`, set through
  `x-migrations-table`. Not cosmetic: the gateway migrates the same database
  with the same tool and the same default `public.schema_migrations`. Sharing
  it means the second product to run reads the other's version, concludes it
  is ahead, and applies nothing at all, silently.
- **`migrate` is a subcommand, not only a boot side effect.** The gateway
  migrates at boot alone, so a rolling deploy of a schema change has every
  replica racing to apply it. Here a pipeline runs `controlplane migrate up`
  as its own step and starts replicas with `CONTROLPLANE_AUTO_MIGRATE=false`.
  The boot-time run stays the default because a single-instance self-hosted
  deployment has no pipeline.
- Boot-time migration does not survive a rolling deploy. Acceptable at single
  instance; revisit with HA, together with in-memory inventory.

### The 501 contract

A stub in this scaffold returns `501 Not Implemented` naming its ticket. It
does not return an empty list, a zero struct, or `nil, nil`.

This is test 3 from the root `CLAUDE.md`, and the reason is that an empty
result is indistinguishable from a working feature with nothing in it. A fleet
view that returns `[]` because nobody wrote it yet reads as "no sidecars are
connected", and somebody acts on that.

Use `apierr.NotImplemented(c, ticket, what)`. When you implement a stub,
delete the call. Do not leave it behind a flag.

### API conventions

- `gin.New()`, never `gin.Default()`. Default installs Gin's own unstructured
  logger and recovery alongside ours.
- All routes in one `routes` method, read top to bottom. The gateway does the
  same at roughly 1250 lines; this stays readable much longer.
- **A route is closed unless it is in `httpapi.UnauthenticatedRoutes`.** That
  set is exported data, not prose, because the test that asserts every other
  route is closed derives its list from `engine.Routes()` minus this set. A
  route registered outside the protected group therefore fails a test instead
  of quietly joining the open set.
- **CORS is a strict allow list, empty by default.** The gateway answers
  `Access-Control-Allow-Origin: *` with `Allow-Credentials: true`, a pairing
  browsers reject and which would be unsafe if they honoured it. A development
  machine opts in with `CONTROLPLANE_CORS_ALLOWED_ORIGINS`. There is a test
  that fails if a wildcard ever appears.
- **Never log a query string or a request body.** Both routinely carry
  credentials here, and a log line is the easiest place in a system to retain
  one forever. A payload type that could be logged whole redacts itself with
  `LogValue`; `wire.HelloPayload` is the worked example.
- **No `ReadTimeout` or `WriteTimeout` on the server.** They are the usual
  advice and they are wrong here: a hijacked WebSocket connection inherits
  both, so a `WriteTimeout` kills every sidecar session on a fixed schedule.
  `ReadHeaderTimeout` and `IdleTimeout` cover the slow-client attack, and API
  routes get their own deadline from `requestTimeout` middleware. EVL-234's
  socket must be mounted outside the `/api` group for the same reason.
- Liveness and readiness are separate endpoints and mean opposite things.
  Never put a database check in liveness: a database blip then restarts every
  replica at once, turning a recoverable dependency failure into an outage.
- No `NoRoute` handler. This binary serves no UI, so an unmatched path is a
  mistake and must look like one.

### Configuration

`config.Load` returns a `Config` **value**. No package-level instance, no
`Get()`, no `IsLoaded()`. `main` loads it once and passes it down.

The gateway's `appconfig` is a global initialised by a `Load()` that panics if
called twice and a `Get()` that returns a zero struct if called too early.
Every test that touches it fights the global. A value has none of those
problems, and the test file for `config` needs no reset helper at all, which
is the visible proof.

`POSTGRES_DB_URI` is required. Everything else defaults. Listen address is
`0.0.0.0:8020`, deliberately not the gateway's 8009, because the two run side
by side for the whole transition and a colliding default breaks every existing
development machine on first run.

**Malformed input fails at startup, never later.** A bad URI, an unparseable
duration, a negative pool size and an unknown deployment all stop the process
with a message naming the variable. The alternative is a default that silently
replaces the operator's intent.

The full list lives in `controlplane/README.md` and in `internal/config`.
There is no helm chart for this service yet; when one lands, the root
`CLAUDE.md` rule applies and the chart changes in the same PR as the variable.

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

- **`controlplane/frontend` still calls the gateway on :8009.** Its service
  files are byte-identical copies of `webapp_v2`'s. It is not a client of this
  API yet, which is why the route paths here are still cheap to rename. That
  stops being true the day someone repoints it.

## Conventions

- Structured logging through an injected `*slog.Logger`. Never `fmt.Println`,
  never stdlib `log`, never a package-level logger. See "Logging" for why this
  differs from the root convention.
- Store functions take their handle as a parameter. No package-level database
  global.
- Config is a value passed down from `main`. No package-level config global.
- Propagate not-found to the caller. The caller decides whether it is an
  error.
- Tests live beside the source. A behaviour an operator can see needs a test
  that fails without it.
- Comments say why. Where the reasoning is not recoverable from the code, say
  it at length; `sidecar/CLAUDE.md` sets the register to match.
- Reference a ticket by its ID (`EVL-231`), never by URL. This repo is public
  and a Linear URL leaks the workspace and the ticket slug.
