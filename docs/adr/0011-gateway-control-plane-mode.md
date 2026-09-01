# ADR-0011: The control plane is the gateway with a narrowed API surface

- **Status:** Proposed
- **Date:** 2026-08-31
- **Author:** @rogefm
- **Code:** [`gateway/api/apiroutes/controlplane.go`](../../gateway/api/apiroutes/controlplane.go), [`gateway/api/apiroutes/router.go`](../../gateway/api/apiroutes/router.go), [`gateway/appconfig/appconfig.go`](../../gateway/appconfig/appconfig.go)
- **Related:** [ADR-0009](0009-guardrails-and-masking-architecture.md) (the two enforcement engines this transition merges the management of)
- **Supersedes / Superseded by:** —

## Context

`controlplane/frontend` shipped (PR #1754). It is an admin-only React app with
its own information architecture, and it calls the gateway on `:8009` for
everything — there is no control-plane backend behind it.

The gateway is being turned into the control plane in place, feature by
feature. Until that finishes, one binary has to serve two products, and today
it serves the wrong one: a control-plane deployment answers all **253** gateway
routes. Runbook execution, agent registration, API keys, webhooks, Jira, AWS,
the MCP server and the `/api/ws` agent transport are all reachable there with
an admin token, for a product that has no agents and no sessions of its own.

Two needs, and they are separate. **A surface**: something that says what the
control plane's API is, and holds. **A flag**: a way for business rules to
differ where the route list cannot express the difference.

Constraints worth naming. The gateway ships to customers and must not change
behaviour. `buildRoutes` is one flat sequence with no conditionals, and
`gateway/integration/testutil` boots the same `BuildEngine`, so there is
exactly one route tree in the repo — a property worth keeping. And the reason
to stay in the gateway at all is reuse: reviews, auth, users, guardrails,
masking and the analyzer already exist here, with their tables and migrations.

## Options considered

1. **A separate `controlplane/backend` module.** Half-built on
   `origin/perotto/evl-230-…`: own `go.mod`, own port, own migrations, own
   admin auth. The right end state, and its own `CLAUDE.md` is explicit that
   nothing there imports the gateway. It loses *for now* because reusing those
   six subsystems is the entire reason for the interim step, and a second
   module forks all of them before anything ships.
2. **A per-org feature flag.** `common/featureflag` already exists. It loses
   because the root `CLAUDE.md` defines a flag as a kill switch an admin flips
   at Settings → Experimental, and a deployment topology is not something a
   browser toggles.
3. **Build tags, or a second `buildControlPlaneRoutes`.** Clear separation. It
   loses because it forks the one route tree, which is the property that makes
   a test able to diff the two surfaces at all.
4. **A 404 stub handler on blocked routes.** Simpler than not registering. It
   loses because both modes then have identical route trees, so the test has to
   compare handler names instead of route sets.

## Decision

We will run the control plane as **the gateway process booted with
`APP_MODE=control-plane`**, and narrow it in three places.

**One list, as data.** `gateway/api/apiroutes/controlplane.go` holds every route the
control plane serves, as `"METHOD /path"` strings. A route not on it is **never
registered with gin** and answers `404 {"message":"resource not found"}` — the
body `gateway/api/rulepacks` already uses to hide a disabled feature, so a
blocked route is indistinguishable from a path nobody wrote.

**The compiler holds the seam.** `apiroutes.Router` stops embedding
`*gin.RouterGroup` and its registration methods return nothing, so an inherited
method or a chained call cannot register a route without passing the list.
Routes registered on the engine instead of the Router — `/ssm`, `/rdpproxy`,
the MCP well-known set and the DCR shim at `<base>/api/mcp/oauth/register` —
escape it, and are wrapped in an explicit `if !isControlPlane`. Only the DCR
shim then answers 404: the other three sit outside `/api`, where an
unregistered path falls to the SPA handler and returns 200 with `index.html`.
The handler is gone either way, but "blocked" does not mean "404" off `/api`.

**Gateway mode never reads the list.** `Router.allow` returns before touching
it. An edit to `controlplane.go` cannot change what the shipping product serves.

Where the list cannot express a rule, `appconfig.Get().IsControlPlane()` does.
`/plugins` is the worked example: one route serves every plugin and the name
arrives in the body, so it is filtered in the handler.

## Consequences

**Adding a route to `buildRoutes` now has a second question** — does the
control plane serve it? `gateway/api/controlplane_test.go` fails if you never
answer: it diffs the list against `engine.Routes()` in both modes and both
directions, so a typo or a renamed path is a red test, not a page whose backend
silently disappeared.

**The list is the migration ledger.** As each domain moves out of the gateway,
its entries leave the file. When the file is empty the gateway is retired, and
option 1 above is what it is retired into. This ADR is the interim step, not a
rejection of that module.

**Indistinguishability has a debugging cost, and it is the point.** A blocked
route and a handler answering "no such record" return the same status and the
same body — `gateway/api/ai` already uses that body for a missing provider. So
a 404 does not tell you which one you hit. Send the request without a token:
a registered route answers 401 from the auth middleware, an unregistered one
still answers 404.

**Two things get worse and are accepted.** The served OpenAPI spec still
describes all 253 routes in control-plane mode until it is filtered. And
approval deep links break: seven sites build `<apiURL>/sessions/<sid>`
(Slack, Jira, webhooks, audit, access requests) and the control-plane router
does not claim `/sessions/:id`, so every approval link lands on a 404 until a
link helper branches on the mode.

**Revisit if the gateway is not retired.** The narrowing is cheap to carry for
a transition and expensive as a permanent condition — two products in one
binary, told apart by an env var. If the transition stalls, the answer is to
finish option 1, not to grow this list.
