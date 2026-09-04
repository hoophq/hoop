# ADR-0013: The control plane is a second boot path in the gateway binary

- **Status:** Accepted
- **Date:** 2026-09-03
- **Author:** @p3rotto
- **Linear:** EVL-237, EVL-245
- **Code:** [`client/cmd/start.go`](../../client/cmd/start.go), [`gateway/main.go`](../../gateway/main.go), [`gateway/api/server.go`](../../gateway/api/server.go), [`gateway/appconfig/appconfig.go`](../../gateway/appconfig/appconfig.go), [`gateway/api/healthz/healthz.go`](../../gateway/api/healthz/healthz.go), [`gateway/api/controlplane_routes_test.go`](../../gateway/api/controlplane_routes_test.go)
- **Related:** PR #1754 (control plane frontend), PR #1773 and #1775 (boot paths, `hoop start control-plane`), PR #1770 (the route allowlist this ADR retires), PR #1772 (closed allowlist draft), PR #1785 (one route tree)
- **Supersedes / Superseded by:** —

## Context

`controlplane/frontend` (PR #1754) administers a fleet of sidecars and has no
backend of its own; the API comes from the gateway. A sidecar opens no gRPC
stream and registers nothing, so that deployment needs none of the gateway's
data plane: the gRPC transport on `:8010`, the protocol proxies, the six
transport plugins, the agent controller, the connection-status loop.

Three constraints: the gateway's behaviour must not change, the gateway↔agent
contract must not change, and the point of staying in one binary is reusing
auth, users, reviews, guardrails, masking and the analyzer with their tables.

The open question was the HTTP surface. PR #1773 started it near-empty and
PR #1770 grew it to 66 hand-picked routes. EVL-245 settled it: a feature the
gateway has works in the control plane from day one, and the UI hides what the
product does not expose.

## Options considered

1. **A separate `controlplane/backend` module.** The right end state. Loses for
   now: a second module forks the six reused subsystems before anything ships.
2. **A per-org feature flag.** Loses: a flag is a kill switch an admin flips in
   a browser, and a deployment topology is not that.
3. **One route tree narrowed by an allowlist** (closed draft #1772). Loses: it
   presumes the route list is known, and it narrows only HTTP while the
   transport, proxies and plugins keep running.
4. **Two boot paths with a near-empty route set that grows** (what #1773 and
   #1770 shipped). Loses under EVL-245: every route was a decision, a test edit
   and a frontend change, to prevent something cheaper to accept. 21 of 271
   routes need the transport, and each fails with an error, not silently.
5. **Two boot paths, one route tree.** Chosen.

## Decision

The control plane is **the gateway binary started with
`hoop start control-plane`**. The boot path drops the data plane; the HTTP
engine is the gateway's, with one handler that differs.

**The subcommand picks the mode.** `gateway.Run` takes an `appconfig.AppMode`
from the command that started the process. An unknown mode stops startup; the
zero value is the gateway. PR #1773 used an `APP_MODE` variable and PR #1775
removed it: a second way to say the same thing is a second way to get it wrong.

**The boot seam is `Run()`.** Shared bootstrap (config, TLS, migrations,
default org, auth, Sentry) runs first, then `runControlPlane()` or
`runGateway()`. Everything that carries traffic lives in `runGateway`, so a
future addition there is off in control-plane mode by construction.

**The engine is shared.** `BuildEngine` builds one engine for both modes: the
`/api` tree, the MCP well-known handlers, `/ssm`, `/rdpproxy`, the web app and
its SPA fallback, behind one middleware chain. The web app is the gateway's;
`controlplane/frontend` is a separate app, and which one a browser reaches is a
deployment question.

**`/healthz` is the one mode-aware handler.** The gateway's probe dials
`127.0.0.1:8010` and returns 400 when it is closed. The control plane never
opens that port, so its probe answers without dialing; otherwise the helm, AWS
and docker-compose health checks would never pass.

**No handler hides a feature by mode.** The Slack-only plugin filter from
PR #1770 is gone. The UI hides what the product does not expose.

| | gateway | control-plane |
|---|---|---|
| HTTP routes, web UI, `/.well-known/*`, `/ssm`, `/rdpproxy` | 389, served | 389, served |
| `/api/healthz` | probes the gRPC port | answers without dialing |
| gRPC `:8010` | open | closed |
| Proxies, transport plugins, agent controller | started | not started |
| Bootstrap: migrations, default org, auth | runs | runs |

**What fails, and how.** 21 of the 271 distinct routes (389 with gin's `HEAD`
twins) need the gRPC transport and fail per request with an HTTP error:

| Routes | Why |
|---|---|
| `POST /sessions`, `POST /sessions/:id/exec`, `POST /runbooks/exec`, `POST /plugins/runbooks/connections/:name/exec`, `GET /connections/:id/{test,databases,tables,columns}`, `POST /federation/test` | `clientexec` dials the closed port |
| Resource plan, apply and health (6); `POST /dbroles/jobs` | no agent stream |
| `POST /proxymanager/{connect,disconnect}` | no client stream |
| `/ssm/*` (3) | dials the closed port per session |

`/mcp` answers; its exec, schema and review-execute tools fail the same way.
`PUT /serverconfig/misc` starts native proxy listeners in the control plane
process. Connection and resource writes report `offline`.

**Two routes work without gRPC.** `GET /api/ws` registers a WebSocket agent in
the in-process broker and `/rdpproxy/*` relays RDP through it, so such an agent
turns a control plane into an RDP data plane. Recorded, not prevented: a
deployment that must carry no traffic must not expose `/api/ws`.

**Reviews run the gateway's code.** `runControlPlane` wires
`ReleaseConnectionOnReview` from a transport server it builds and never starts.
With no stream in the process it only logs, so a verdict written here does not
signal a session waiting on another gateway.

`/serverinfo` reports `application_mode`.

## Consequences

- **HTTP is fail-open, the process is fail-closed.** A route added to
  `buildRoutes` reaches the control plane; a subsystem added to `runGateway`
  does not.
- **`controlplane_routes_test.go` asserts parity**, in both directions, and
  replaces the pinned list.
- **The OpenAPI spec is accurate**: one route tree, the 21 failing routes
  included.
- **The bootstrap boundary is debatable.** `ProvisionOrgAgentKey` and the
  default connection tags still run in control-plane mode; an unused key is
  harmless, and moving it is a behaviour change.
- **No chart yet.** PR #1775 added and removed a skeleton chart;
  `make run-dev-control-plane` runs the mode on the host. A chart is owed
  before production use.
- **No agent sees any of this.** No packet type, spec key or payload changed.
- **Revisit if the gateway is not retired.** Two products in one binary is
  cheap for a transition and expensive as a permanent condition; the answer is
  option 1, not a longer second boot path.

### How this was verified

`controlplane_routes_test.go` diffs both engines; `make test-oss` passes.

Both modes booted from one binary on embedded PGlite with `GIN_MODE=debug`
register the same 389 routes, and with `STATIC_UI_PATH` set both serve
`index.html` at `/`. Control plane: `/api/healthz` 200, `/api/serverinfo` 401
without a token, port 8010 closed; with an admin token, users, plugins (all of
them), API keys, audit logs, server logs, agents and auth config answer 200,
while `POST /api/sessions`, `GET /api/connections/:name/databases` and
`POST /api/proxymanager/connect` return errors and the process stays up.
Gateway (`PLUGIN_AUDIT_PATH` on a writable directory): 389 routes,
`/api/healthz` 200, port 8010 open. `appmode_test.go` covers the mode table.
