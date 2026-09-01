# ADR-0013: The control plane is a second boot path in the gateway binary

- **Status:** Proposed
- **Date:** 2026-09-01
- **Author:** @p3rotto
- **Linear:** EVL-237
- **Code:** [`gateway/main.go`](../../gateway/main.go), [`gateway/api/server.go`](../../gateway/api/server.go), [`gateway/appconfig/appconfig.go`](../../gateway/appconfig/appconfig.go), [`gateway/api/healthz/healthz.go`](../../gateway/api/healthz/healthz.go)
- **Related:** PR #1754 (the control plane frontend), PR #1773 (implements this ADR), PR #1772 (open draft proposing the route-allowlist mechanism this ADR's option 3 records)
- **Supersedes / Superseded by:** —

## Context

`controlplane/frontend` shipped in PR #1754. It is an admin-only React app for
administering a fleet of sidecars, and its own `CLAUDE.md` is explicit that the
control plane backend does not exist yet, so the API comes from the gateway.

The control plane has no agents, no connections and no sessions of its own. A
sidecar is a library that turns database wire bytes into verdicts; it opens no
gRPC stream to a gateway and registers nothing. So a deployment that exists to
manage sidecars needs none of the machinery the gateway exists to run.

Booted today, it would get all of it: **264** HTTP routes, the gRPC transport on
`:8010` accepting agent and client streams, the Postgres, SSH, RDP and HTTP
protocol proxies, the six transport plugins, the agent controller and the
connection-status conciliation loop. Runbook execution, agent registration, API
keys and webhooks are all reachable with an admin token in a product that cannot
use them.

Three constraints shape the answer. The gateway ships to customers and its
behaviour must not change. Agents run on customer infrastructure and stay old
for a long time, so nothing here may touch the gateway↔agent contract. And the
reason to stay inside the gateway binary at all is reuse: auth, users, reviews,
guardrails, masking and the analyzer already exist here with their tables and
migrations.

One thing we do not know is which routes the control plane will need. The
frontend's information architecture is new and still moving, and no route has
yet been reviewed against a deployment that has no agents and no sessions.

## Options considered

1. **A separate `controlplane/backend` Go module.** Its own `go.mod`, port,
   migrations and admin auth. The right end state, and the one `go.work` should
   eventually list. It loses *for now* because reusing those six subsystems is
   the entire point of an interim step, and a second module forks all of them
   before anything ships. No such module exists in the repo today.
2. **A per-org feature flag.** `common/featureflag` is already wired to
   Settings → Experimental. It loses because the root `CLAUDE.md` defines a flag
   as a kill switch an admin flips in a browser, and a deployment topology is
   not something a browser toggles.
3. **One route tree, narrowed by an allowlist.** A `"METHOD /path"` list
   consulted at registration, with `apiroutes.Router` changed so the compiler
   refuses a registration that skips the list — the design in the open draft
   #1772. Its central property is real: one route tree, and a test can diff the
   two surfaces in both directions. It loses as the *first* step for two
   reasons. It presumes we know the control plane's route list, and we do not.
   And it narrows only the HTTP surface, leaving the gRPC transport, the proxies
   and the plugins running in a deployment that has no agents to serve.
4. **Two boot paths, starting from a near-empty route set.** Chosen.

## Decision

We will run the control plane as **the gateway binary booted with
`APP_MODE=control-plane`**, split at two seams: the process boot path and the
engine builder.

**`APP_MODE` is typed and defaults to the gateway.** `appconfig` parses it into
an `AppMode` case-insensitively; empty means `gateway`, and an unrecognised
value stops startup rather than guessing. A zero-value `Config` reads as the
gateway, so the mode is never the empty string and an existing deployment that
never heard of the variable is unaffected.

**The boot seam is in `Run()`.** Shared bootstrap runs first — config, TLS,
migrations, the default organization, auth, Sentry — then `Run` dispatches to
`runControlPlane()` or `runGateway()`. Everything that carries agent or client
traffic lives in `runGateway`: the transport plugin chain, the agent controller,
the protocol proxies, connection-status conciliation, the gRPC server. A future
addition there is off in control-plane mode by construction, without anyone
remembering this ADR.

**The engine seam is in `BuildEngine()`.** Control-plane mode returns early into
`buildControlPlaneEngine` before the static UI, the SPA fallback, the MCP
well-known handlers, `/ssm` and `/rdpproxy`. Both modes share one middleware
chain through `newEngine` and `newAPIRouter`, so the control plane gets the same
recovery, proxy policy, security headers, CORS and Sentry wiring the gateway
gets.

**The route surface starts near-empty and grows.** `buildControlPlaneRoutes`
registers `/healthz` and nothing else. Each route is added only after it has
been reviewed against a deployment with no agents, no connections and no
sessions.

| | gateway (default) | control-plane |
|---|---|---|
| HTTP routes | 264 | 2 (`GET`/`HEAD /api/healthz`) |
| gRPC `:8010` | open | closed |
| Protocol proxies, transport plugins, agent controller | started | not started |
| Static UI, SPA fallback, `/.well-known/*`, `/ssm`, `/rdpproxy` | served | absent |
| Bootstrap: migrations, default org, auth | runs | runs |

`/healthz` gets its own handler rather than a shared one. The gateway's
`LivenessHandler` dials `127.0.0.1:8010` and returns 400 when the port is
closed; control-plane mode never opens it, so reusing that handler would fail
every health check — and `/api/healthz` is what the helm service, the AWS load
balancer template and the docker-compose healthcheck all probe.

`/serverinfo` reports `application_mode`, so a client can tell which product it
is talking to without inferring it from a 404.

## Consequences

**The default is now fail-closed, and that is the property worth keeping.** A
new proxy, plugin or route added to `runGateway` or `buildRoutes` does not
appear in control-plane mode unless someone puts it there. The alternative
designs all default the other way: they start from everything and subtract.

**The route set is a migration ledger that grows.** This is the inverse of
#1772, whose list shrinks as domains leave the gateway. Both describe the same
transition from opposite ends, and they are not compatible mechanisms — if the
control plane ends up serving most of the gateway's routes, the allowlist
becomes the better tool and this ADR should be amended rather than quietly
worked around.

**The control plane frontend has no backend until routes are ported.** PR #1754
shipped a UI that calls the gateway; in control-plane mode it now reaches a
process that answers `/api/healthz`. That is deliberate and it is a real gap,
not a soft launch. Nothing should be pointed at this mode in production until
its routes exist.

**`gateway/api/controlplane_routes_test.go` asserts the exact route list.** A
route added to `buildControlPlaneRoutes` without being listed in the test fails
the build. The test is the review gate, not a description of one.

**The served OpenAPI spec still describes all 264 routes in control-plane
mode.** It is generated from swagger annotations on the handlers, which know
nothing about the mode. Filtering it needs its own change.

**The bootstrap/`runGateway` boundary is not obviously in the right place.**
Migrations, the default organization, default runbooks and rulepack seeding all
still run in control-plane mode. `ProvisionOrgAgentKey` and the default
connection tags are agent- and connection-shaped and arguably belong on the
gateway side of the split. Left as-is because provisioning an unused key is
harmless and moving it is a behaviour change; revisit when the first real
control-plane route lands.

**The helm chart now refuses one combination.** `defaultAgent.enabled=true` with
`APP_MODE=control-plane` renders an agent pointed at `127.0.0.1:8010`, which
this mode never opens, so the chart fails rendering unless
`defaultAgent.grpcHost` names an external gateway. Two products in one chart
means every value that assumes a data plane needs this question asked of it.

**No agent sees any of this.** No packet type, spec key or payload changed, so
the gateway↔agent contract is untouched and an old agent is unaffected. This is
a deployment-topology decision, not a wire one.

**Revisit if the gateway is not retired.** Two products in one binary told apart
by an env var is cheap for a transition and expensive as a permanent condition.
If the transition stalls, the answer is to finish option 1, not to keep growing
the second boot path.

### How this was verified

Against `main` at `10f45d22`, both modes booted against embedded PGlite.

Control plane (`APP_MODE=control-plane POSTGRES_DB_URI=pglite:///tmp/cp`): gin
registered 2 routes; `GET /api/healthz` → `200 {"liveness":"OK"}`;
`/api/serverinfo`, `/` and `/.well-known/oauth-protected-resource` → 404; TCP
`127.0.0.1:8010` closed.

Gateway regression (`POSTGRES_DB_URI=pglite:///tmp/gw`, `APP_MODE` unset): gin
registered 264 routes; `/api/healthz` → 200; `/api/publicserverinfo` → 200;
`/api/serverinfo` → 401; TCP `127.0.0.1:8010` open.

`gateway/appconfig/appmode_test.go` covers the parse table, including that an
unrecognised value errors and a zero-value `Config` reads as the gateway. The
helm guard was checked with helm 3.16.2 across all four combinations of
`APP_MODE` and `defaultAgent.enabled`: the three legal ones render, the broken
one fails with the incompatibility message. `make test-oss` passes.
