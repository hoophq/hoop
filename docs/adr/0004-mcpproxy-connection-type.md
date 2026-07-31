# ADR-0004: Protocol-aware MCP gateway as a first-class connection type

- **Status:** Proposed
- **Date:** 2026-07-27
- **Related:** `libhoop/agent/httpproxy` (current MCP path), `gateway/api/connections/connection_mcp_oauth.go` (existing OAuth brokering), external module `github.com/hoophq/mcpproxy`

## Context

### How hoop proxies MCP today

The existing `mcp` connection type (`common/proto/const.go:52`) is an alias of
httpproxy: `connection_credentials.go` folds `ConnectionTypeMcp` into the
`ConnectionTypeHttpProxy` family for credential issuance, revocation and
listen-address resolution, and the agent serves it through
`libhoop/agent/httpproxy` — a byte-level HTTP relay.

That path treats MCP as opaque HTTP. Consequences:

1. **Guardrails see strings, not semantics.** `httpproxy/guardrails.go`
   flattens method + URL + headers + body into one string and validates that.
   It cannot distinguish "the model is calling `delete_issue`" from the same
   text appearing inside a tool result — either false positives or misses.
2. **No tool-level control.** Allow/deny lists, per-tool rate limits, or
   holding a specific `tools/call` for review are not expressible over an
   undifferentiated byte stream.
3. **Audit is request/response blobs.** Session review shows HTTP exchanges,
   not "called `create_issue` with these arguments, result 2.3 KB, 840 ms".
4. **MCP-specific attacks pass through.** Tool poisoning (injection payloads
   in tool descriptions), rug pulls (a tool's description/schema silently
   changing mid-session), and server-initiated `sampling/createMessage` /
   `elicitation/create` requests (a compromised server driving the user's LLM
   or phishing via elicitation dialogs) are invisible to a byte relay.
5. **Frozen OAuth tokens.** `connection_mcp_oauth.go` runs a full
   discovery + DCR + PKCE flow at connection-creation time, but freezes only
   the resulting access token into `HEADER_AUTHORIZATION`. When it expires,
   the connection breaks; the refresh token is discarded.

### What exists to build on

- **`github.com/hoophq/mcpproxy`** — a standalone MCP gateway module. It
  terminates MCP streamable-HTTP on the front, parses every JSON-RPC message
  in both directions through an ordered check pipeline
  (allow/deny/hold/kill), and bridges to backends over three transports:
  stdio subprocess (process-group lifecycle), streamable-HTTP, and legacy
  HTTP+SSE. Built-in checks: tool allow/deny with catalog filtering (denied
  tools are removed from `tools/list` before the model sees them),
  approval holds, rug-pull fingerprinting (SHA-256 over name +
  description + input schema), server-request gating (sampling/elicitation
  denied by default), budgets (calls/session, per-tool rate, result-size
  truncation), catalog overrides (rename/rewrite descriptions). Outbound
  auth: static headers, per-request passthrough, and the full MCP
  authorization flow (RFC 9728/8414 discovery, RFC 7591 DCR, PKCE,
  persistent refresh, RFC 8693 token exchange). It also ships a curated
  catalog of ~32 public remote MCP servers (`catalog/catalog.yaml`).
- Every hoop-shaped concern in mcpproxy is an interface, not a dependency:
  `inspect.Hooks` (guardrails / masking / analysis callbacks), `audit.Sink`
  (event stream), `gateway.HeldStore` (approval workflow),
  `gateway.IdentityResolver` (caller identity), `gateway.Observer`
  (telemetry). The standalone daemon wires its own implementations; a host
  replaces them without touching gateway code.
- **Hoop precedents that de-risk the integration:**
  - The agent already handles blocking upstreams without stalling the recv
    loop via the per-`(session, connection)` `packetQueue` FIFO
    (`agent/controller/httpproxy.go:34-76`). Held tool calls (minutes-long
    human approvals) need exactly this.
  - `libhoop.NewHttpProxy` (`libhoop/libhoop.go:224`) shows the constructor
    shape: redactor client built from opts, AI analyzer injected by the
    controller (libhoop cannot import `common/aianalyzer`), DEP-48
    fail-closed guardrail check at the point of enforcement.
  - `gateway/proxyproto/httpproxy` frames raw HTTP bytes into packets — the
    same wire shape the new type needs, reusable by parameterizing the
    packet type.
  - `connection_mcp_oauth.go` + `models/mcp_oauth_flow.go` already implement
    gateway-side OAuth brokering with a browser callback; they need refresh
    persistence, not reconstruction.

## Decision

Add a new connection type `mcpproxy` that embeds the mcpproxy gateway in the
agent, keeping the existing `mcp` (httpproxy-alias) type untouched until the
new path is proven.

### Topology

```
MCP client → hoop gateway (auth, RBAC, proxyproto) → gRPC packets
  → agent controller (packetQueue per sid:connID)
  → libhoop/agent/mcpadapter (byte stream ⇄ in-memory HTTP listener)
  → mcpproxy gateway (pipeline: policy, budgets, rug-pull, S2C gate, hooks)
  → backend: stdio child | streamable-http remote | legacy SSE remote
```

The adapter bridges hoop's `Write([]byte)` proxy contract to mcpproxy's
`http.Handler` with a `net.Pipe`-backed listener. The wire format between
gateway and agent stays raw HTTP bytes, so the gateway side reuses the
httpproxy proxyproto with a new packet type rather than a new framing layer.

### Feature mapping (hoop feature → mcpproxy seam)

| Hoop feature | Seam | Adapter implementation |
|---|---|---|
| Guardrails | `inspect.Hooks.GuardInput` | `redactor.ValidateGuardRailRules` over `tools/call` arguments (input) and tool descriptions (output; a poisoned catalog kills the session) |
| Data masking | `inspect.Hooks.Redact` | existing masking applied per `result.content[].text` leaf — the JSON-RPC envelope cannot be corrupted |
| AI analyzer | `inspect.Hooks.Analyze` | injected by the controller as for httpproxy; receives `(tool, args)` instead of `(method, url, body)`; fail-open, blocks single requests only |
| Session audit | `audit.Sink` | `mcp.*` events (tool_call, tool_denied, tools_changed, approval_*, redacted…) serialized as session events; hoop's session ID injected via the gateway's session-ID factory |
| Reviews | `gateway.HeldStore` | a matched `tools/call` parks; the agent raises a review (new type `mcp-tool-call` in `models/reviews.go`); resolution flows back and the call proceeds or the client receives JSON-RPC `-32011` — the session survives either way |
| Identity | `gateway.IdentityResolver` | returns the session's user from connection opts; mcpproxy's own inbound-auth stack is not used (hoop authenticated the caller already) |

DEP-48 is honored at the same point as every other proxy: `NewMCPProxy`
refuses a session with `guard_rail_rules` configured when no enforcement path
exists (`CheckGuardRailEnforcement(rules, "mcpproxy")`).

### Configuration surface (webUI)

Connection envs follow existing conventions (`parseConnectionEnvVars` already
collects `HEADER_*` prefixes and b64 `envvar:` entries):

| Env | Meaning |
|---|---|
| `MCP_TRANSPORT` | `stdio` \| `streamable-http` \| `sse` |
| `COMMAND`, `envvar:*` | stdio: child command + environment (secrets via secrets manager) |
| `REMOTE_URL`, `HEADER_*` | remote endpoints + static credentials |
| `MCP_AUTH` | `none` \| `static` \| `passthrough` \| `oauth` |
| `MCP_ALLOWED_TOOLS`, `MCP_DENIED_TOOLS`, `MCP_APPROVAL_TOOLS` | tool globs (deny > allow; approval matches hold for review) |
| `MCP_MAX_CALLS`, `MCP_MAX_RESULT_KB`, `MCP_ON_RUG_PULL`, `MCP_BLOCK_SAMPLING`, `MCP_BLOCK_ELICITATION` | budgets and protocol gates |

A small gateway endpoint exposes mcpproxy's embedded catalog so the
connection form can open with a server picker (Linear, Stripe, Notion, …)
that pre-fills URL / transport / auth mode; "custom" falls back to the raw
form.

### OAuth

`connection_mcp_oauth.go` is extended rather than replaced: persist the
refresh token keyed by connection (today only the access token is frozen into
a header), and let the agent pull/refresh tokens through mcpproxy's
`outbound.TokenStore` seam instead of receiving a static header. Per-user
grants later reuse the same flow keyed `(connection, user)`.

## Consequences

### Positive

- Tool-level policy, approvals on individual tool calls, rug-pull detection,
  and sampling/elicitation gating become available to every MCP connection —
  none of which are expressible on the byte-relay path.
- Session review gains a structured tool-call timeline (which tools, what
  arguments digest, what was blocked and by which rule) instead of HTTP
  blobs.
- Guardrails and masking operate on exactly the free-text fields (arguments,
  descriptions, result text), reducing false positives and making masking
  envelope-safe.
- stdio MCP servers become first-class: hoop can run `npx`-style servers
  under agent control with process-group lifecycle, not just remote URLs.
- OAuth-protected remotes survive token expiry once refresh persistence
  lands.

### Negative / risks

- **A second MCP path.** Until the old `mcp` subtype is migrated, two
  implementations coexist. Mitigation: the old type is untouched (zero
  regression risk) and becomes an alias after the new path proves out.
- **Dependency weight.** mcpproxy pulls prometheus/otel into libhoop's
  build. If footprint becomes a concern, the telemetry package can be split
  into a separate Go module; the seams already isolate it.
- **Held calls consume agent resources.** A parked `tools/call` holds a
  worker slot for up to the approval timeout. Bounded by the existing
  packetQueue overflow behavior plus mcpproxy's approval timeout
  (auto-reject).
- **Protocol drift.** MCP revisions ship frequently. mcpproxy parses only
  what it inspects and passes unknown fields/methods through byte-identical,
  so spec bumps degrade to pass-through rather than outages.

### Build order

1. **Done.** `libhoop/agent/mcpadapter` + `libhoop.NewMCPProxy` (DEP-48 gated) —
   libhoop only, provable against a real stdio MCP server in isolation.
2. **Done.** Hooks wiring: guardrails, masking, audit sink. The analyzer is
   *not* wired: `checks.Assemble` never consults `inspect.Hooks.Analyze`, so
   that seam needs a check in mcpproxy before hoop can feed it.
3. **Done.** `common/proto` + `agent/controller` + `gateway`: packet pair
   `MCPProxyConnectionWrite`, `controller/mcpproxy.go` with the packetQueue
   intact, `sessionCleanup` closing the adapter, proxyproto packet-type
   parameterization, credential issuance and response.
4. **Done (gateway half).** `GET /mcp-catalog` serves mcpproxy's embedded
   catalog. The webUI form pre-fill is still to build; see below.
5. Reviews: `mcp-tool-call` review type, request/resolve packets, UI card.
   Until it ships, approval matches degrade to deny (fail closed).
6. OAuth refresh persistence + agent token pull; per-user grants after.
   Until then the agent refuses `MCP_AUTH=oauth|passthrough` rather than
   silently running an unauthenticated backend: hoop brokers OAuth itself
   (`/mcp-oauth/*`) and freezes the result into `HEADER_AUTHORIZATION`,
   which the static path carries.

Each phase is independently shippable; phases 1–2 carry no coordination cost
with gateway owners.

### Parent connection type

An `mcpproxy` connection is filed as subtype `mcpproxy` under the
**`httpproxy`** parent, beside the legacy `mcp` subtype — that is where every
existing MCP surface in the webapp puts it, and it keeps credential issuance
and the proxy-manager path working unchanged. `ToConnectionType` inspects the
subtype under that parent before defaulting to the byte relay; the
`application` and `custom` parents also resolve it, so an API- or CLI-created
connection behaves the same.

### Remaining work before merge

- **`mcpproxy` has no published version.** `agent/go.mod` and `gateway/go.mod`
  both carry `replace github.com/hoophq/mcpproxy => ../../mcpproxy`. Because
  `go.work` lists every module, *no* module resolves without that sibling
  checkout — CI and container builds fail. Either publish and tag the module,
  or adopt the libhoop precedent (gitignored in-repo checkout plus a CI clone
  step).
- **webUI.** The frontend has no `mcpproxy` surface. Registration points: the
  connection catalog metadata entry (`{type: httpproxy, subtype: mcpproxy}`,
  which drives the card, icon and labels in both apps), a create-flow role
  form in CLJS (`resources/setup/roles_step.cljs`), an edit renderer in both
  CLJS (`configure_role/credentials_tab.cljs`) and React
  (`Roles/Configure/sections/credentials/index.jsx`, cloning
  `McpRenderer.jsx`), env emission/hydration for the `MCP_*` keys in
  `process_form.cljs`, and the native-client Connect modal. The existing
  `/mcp-oauth` popup machinery is subtype-agnostic and reusable as-is.
- **Session timeline.** The agent emits one JSON audit line per protocol event
  and the gateway records it, but the `mcp.event` marker is dropped between
  the WAL and both the SSE stream and the persisted blob (`eventbroker.Event`
  and `sessionStreamEvent` are three-field structs). The viewer can either
  gain a fourth field end to end, or sniff the payload — every event name is
  `mcp.`-prefixed, which needs no backend change.
