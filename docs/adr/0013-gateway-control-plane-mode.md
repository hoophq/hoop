# ADR-0013: The control plane is a second boot path in the gateway binary

- **Status:** Proposed
- **Date:** 2026-09-03
- **Author:** @p3rotto
- **Linear:** EVL-237, EVL-245
- **Code:** [`client/cmd/start.go`](../../client/cmd/start.go), [`gateway/main.go`](../../gateway/main.go), [`gateway/api/server.go`](../../gateway/api/server.go), [`gateway/appconfig/appconfig.go`](../../gateway/appconfig/appconfig.go), [`gateway/api/healthz/healthz.go`](../../gateway/api/healthz/healthz.go), [`gateway/api/controlplane_routes_test.go`](../../gateway/api/controlplane_routes_test.go)
- **Related:** PR #1754 (the control plane frontend), PR #1773 (the two boot paths), PR #1775 (`hoop start control-plane`), PR #1770 (the 66-route allowlist this ADR retires), PR #1772 (closed draft of a registration-time allowlist), PR #1785 (one route tree for both modes)
- **Supersedes / Superseded by:** —

## Context

`controlplane/frontend` shipped in PR #1754. It is an admin-only React app for
administering a fleet of sidecars, and its own `CLAUDE.md` is explicit that the
control plane backend does not exist yet, so the API comes from the gateway.

The control plane has no agents, no connections and no sessions of its own. A
sidecar is a library that turns database wire bytes into verdicts; it opens no
gRPC stream to a gateway and registers nothing. So a deployment that exists to
manage sidecars needs none of the machinery the gateway exists to run.

Booted as a gateway, it would get all of it: the gRPC transport on `:8010`
accepting agent and client streams, the Postgres, SSH, RDP and HTTP protocol
proxies, the six transport plugins, the agent controller and the
connection-status conciliation loop.

Three constraints shape the answer. The gateway ships to customers and its
behaviour must not change. Agents run on customer infrastructure and stay old
for a long time, so nothing here may touch the gateway↔agent contract. And the
reason to stay inside the gateway binary at all is reuse: auth, users, reviews,
guardrails, masking and the analyzer already exist here with their tables and
migrations.

The HTTP surface was the open question. PR #1773 started it near-empty and
PR #1770 grew it to the 66 routes the shipped frontend calls. The EVL-245
discussion settled it the other way: a feature the gateway already has should
work in the control plane from the first version, and the UI hides what the
product does not expose. Users, audit logs, server logs, analytics mode, API
keys, IdP integration and license management all work that way with no port.

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
   refuses a registration that skips the list: the design in the closed draft
   #1772. It loses because it presumes we know the control plane's route list,
   and because it narrows only the HTTP surface, leaving the gRPC transport, the
   proxies and the plugins running in a deployment that has no agents to serve.
4. **Two boot paths, starting from a near-empty route set.** What PR #1773 and
   PR #1770 shipped: `buildControlPlaneRoutes` listed each route by hand, a test
   pinned the list, and `gateway/api/plugins` hid every plugin but Slack. It
   loses under EVL-245. Every route was a decision to make, a test to edit and a
   frontend change to coordinate, and what it prevented, a route answering in a
   deployment that cannot use it, turned out cheaper to accept than to prevent:
   23 of the gateway's 271 routes need the transport, and each of them fails
   with an error rather than silently.
5. **Two boot paths, one route tree.** Chosen.

## Decision

We will run the control plane as **the gateway binary started with
`hoop start control-plane`**, split at two seams: the process boot path and the
engine builder. The boot seam removes the data plane. The engine is the same
in both modes; one handler, `/healthz`, differs.

**The subcommand picks the mode, not the environment.** `gateway.Run` takes an
`appconfig.AppMode` and hands it to `appconfig.Load`. There is no variable to
set, so no environment and command line can disagree with each other, and a
deployment picks a component the same way it already does: by picking a
container command. An unrecognised mode stops startup rather than guessing, and
the zero value reads as the gateway, so a caller that leaves it unset keeps the
shipping behaviour.

PR #1773 first shipped this as an `APP_MODE` environment variable. PR #1775
removed it in favour of the subcommand, because a second way to say the same
thing is a second way to get it wrong.

**The boot seam is in `Run()`.** Shared bootstrap runs first: config, TLS,
migrations, the default organization, auth, Sentry. Then `Run` dispatches to
`runControlPlane()` or `runGateway()`. Everything that carries agent or client
traffic lives in `runGateway`: the transport plugin chain, the agent controller,
the protocol proxies, connection-status conciliation, the gRPC server. A future
addition there is off in control-plane mode by construction, without anyone
remembering this ADR.

**The engine seam is in `BuildEngine()`.** Both modes build one engine from one
route tree: the `/api` tree, the MCP well-known handlers, `/ssm`, `/rdpproxy`,
and the gateway's web app with its SPA fallback. Both modes share one middleware
chain through `newEngine` and `newAPIRouter`, so the control plane gets the
same recovery, proxy policy, security headers, CORS and Sentry wiring the
gateway gets. The web app served at `/` is the gateway's; `controlplane/frontend`
is a separate app that is not embedded in the binary, and which of the two a
browser reaches is a deployment question this ADR does not settle.

`buildRoutes` reads the mode for one route. The gateway's `/healthz` dials
`127.0.0.1:8010` and returns 400 when the port is closed; control-plane mode
never opens it, so its handler answers without dialing. `/api/healthz` is what
the helm service, the AWS load balancer template and the docker-compose
healthcheck all probe, so a control plane with the gateway's handler would never
become healthy.

**No handler hides a feature by mode.** PR #1770 answered 404 for every plugin
but Slack inside `gateway/api/plugins`; that is gone. A control plane that
should not expose a gateway feature hides it in the UI, not in the handler.

| | gateway (default) | control-plane |
|---|---|---|
| HTTP routes | 389 | 389 |
| `/api/healthz` | probes the gRPC port | answers 200 without dialing |
| gRPC `:8010` | open | closed |
| Protocol proxies, transport plugins, agent controller | started | not started |
| Static UI, SPA fallback, `/.well-known/*`, `/ssm`, `/rdpproxy` | served | served |
| Bootstrap: migrations, default org, auth | runs | runs |

**What does not work, and how it fails.** 23 of the 271 distinct routes (389
counting the `HEAD` twin gin registers for every `GET`) need the transport this
mode never starts. Each fails per request with an HTTP error; none panics.

| Group | Routes | Failure |
|---|---|---|
| Exec and schema browsing | `POST /sessions`, `POST /sessions/:id/exec`, `POST /runbooks/exec`, `POST /plugins/runbooks/connections/:name/exec`, `GET /connections/:id/{test,databases,tables,columns}`, `POST /federation/test` | `clientexec` dials `127.0.0.1:8010`, which is closed |
| Resource plan, apply and health; `POST /dbroles/jobs` | 7 | no agent stream is connected |
| `POST /proxymanager/{connect,disconnect}` | 2 | no client stream is connected |
| `/ssm/*`, `/rdpproxy/*` | 5 | proxy to the transport |

`/mcp` answers; three of its tools (exec, schema exec, review execute) fail the
same way. `PUT /serverconfig/misc` succeeds and starts native proxy listeners
inside the control plane process; any session through them fails at the gRPC
dial. Connection and resource writes report `offline`, and a feature-flag update
pushes to zero agents.

`PUT /reviews/:id` and `PUT /sessions/:id/review` work. Approving a review
releases the gRPC stream waiting on the verdict; `runControlPlane` passes a
no-op release callback because this mode holds no stream, so the verdict is
complete once written.

`/serverinfo` reports `application_mode`, so a client can tell which product it
is talking to.

## Consequences

**HTTP is fail-open and the process is fail-closed.** A route added to
`buildRoutes` appears in the control plane without anyone deciding so; a
subsystem added to `runGateway` does not. Whoever adds a route that needs the
transport gets a control plane that answers it with an error, and that is
accepted.

**`gateway/api/controlplane_routes_test.go` asserts parity, not a list.** It
builds both engines in one process and checks, in both directions, that the
two modes register the same routes. A test that pins a hand-written list is
what this replaces.

**Part of the control plane frontend still describes the allowlist.** PR #1785
updates `controlplane/frontend`'s `README.md`, `CLAUDE.md` and `ModeBanner`.
Comments in its services and user store still say the group and attribute
routes answer 404, which is no longer true; they are owed a follow-up.

**The served OpenAPI spec describes what is served.** It is generated from the
handlers' swagger annotations, which know nothing about the mode; with one
route tree that is accurate, including the 23 routes that fail.

**The bootstrap/`runGateway` boundary is not obviously in the right place.**
Migrations, the default organization, default runbooks and rulepack seeding all
still run in control-plane mode. `ProvisionOrgAgentKey` and the default
connection tags are agent- and connection-shaped and arguably belong on the
gateway side of the split. Left as-is because provisioning an unused key is
harmless and moving it is a behaviour change.

**The control plane has no chart.** PR #1775 added a skeleton chart and removed
it in the same PR; `make run-dev-control-plane` runs the mode on the host
against the dev database. A chart is owed before anything points at this mode
in production. The gateway chart is untouched, so no value on it can produce a
control plane.

**No agent sees any of this.** No packet type, spec key or payload changed, so
the gateway↔agent contract is untouched and an old agent is unaffected. This is
a deployment-topology decision, not a wire one.

**Revisit if the gateway is not retired.** Two products in one binary told apart
by a subcommand is cheap for a transition and expensive as a permanent
condition. If the transition stalls, the answer is to finish option 1, not to
keep growing the second boot path.

### How this was verified

`gateway/api/controlplane_routes_test.go` builds both engines in one process
and diffs them; `make test-oss` passes with no failures.

Both modes booted from one binary against embedded PGlite with
`GIN_MODE=debug`, counting unique `METHOD /path` pairs from gin's registration
log. Both register 389 (271 without the `HEAD` twins) and a `diff` of the two
lists is empty. The dev binary embeds no web UI build; with `STATIC_UI_PATH`
pointing at a directory holding an `index.html`, both modes register
`GET /index.html` and serve it at `/`.

`hoop start control-plane`: `GET /api/healthz` → `200 {"liveness":"OK"}`;
`/api/publicserverinfo` → 200; `/api/serverinfo` and `/api/users` → 401
without a token; `/` → 404 without a UI build; TCP `127.0.0.1:8010` closed.
After `POST /api/localauth/register` and a login, `GET /api/users`,
`/api/serverinfo`, `/api/plugins` (every plugin, not only slack),
`/api/api-keys`, `/api/audit/logs`, `/api/server-logs`, `/api/agents` and
`/api/serverconfig/auth` answer 200. With an agent and a connection created
through the API, `POST /api/sessions` and `GET /api/connections/:name/databases`
answer 500 from `clientexec` (on a macOS host it fails preparing `/opt/hoop`
before the dial), `POST /api/proxymanager/connect` answers 400
`proxy manager state ... not found`, `PUT /api/reviews/:id` answers
`review not found`, and the process stays up with no panic logged.

`hoop start gateway`, with `PLUGIN_AUDIT_PATH` pointed at a writable directory:
389 routes; `/api/healthz` → 200; TCP `127.0.0.1:8010` open.

`gateway/appconfig/appmode_test.go` covers the mode table, including that an
unrecognised value errors and a zero-value `Config` reads as the gateway.
