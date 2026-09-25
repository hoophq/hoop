# ADR-0021: A held statement answers an agent at once, and the sidecar tells it when the review settles

- **Status:** Proposed
- **Date:** 2026-09-25
- **Author:** @p3rotto
- **Deciders:** TBD
- **Linear:** EVL-318 (project: Reviews over MCP)
- **Code:** [`sidecar/analyzer/review.go`](../../sidecar/analyzer/review.go), [`sidecar/daemon/review.go`](../../sidecar/daemon/review.go), [`gateway/api/sidecar/reviews.go`](../../gateway/api/sidecar/reviews.go)
- **Related:** [ADR-0013](0013-gateway-control-plane-mode.md) (control-plane mode), [ADR-0015](0015-listener-analyzer-block.md) (listener analyzer block), [ADR-0017](0017-control-plane-composes-sidecar-rules.md) (composed rules), ADR-0019 "sidecar identity from the network" (proposed, PR #1851)
- **Supersedes / Superseded by:** none

## Context

A listener whose analyzer answers `require_review` files the statement with
the control plane and holds the connection (EVL-307). `Evaluator.hold` asks
`POST /api/sidecars/reviews/:id/claim` every 5 seconds for up to 30 minutes
and sends nothing to the client in the meantime.

That fits a human at `psql`. It does not fit an AI agent:

- The agent sees a statement that hangs, with no review id and no reason.
- Agent tool calls end long before 30 minutes. The gateway's own MCP server
  measured 60 to 120 seconds before clients drop a call, and caps its wait
  tools at 300 seconds for that reason (`gateway/api/mcpserver/poll.go`).
  When the client gives up, the result is lost even if a human approves.

The control plane already supports an answer that does not wait.
`POST /api/sidecars/reviews` matches a statement to the live review for the
same bytes (`models.HashStatement`), returns PENDING, REJECTED or REVOKED as
they stand, and spends an APPROVED review once. A retry of identical bytes
is therefore a status check, and after approval it is the release.

Four facts limit the design:

- **The sidecar cannot tell a human from an agent on the wire.** An agent
  that runs `psql` through a shell sends the same bytes as a person. A
  connection carries a peer address and, on HTTP and gRPC, a subject from
  `identity_header`. ADR-0019 proposes more, and it is not accepted.
- **Claim spends the approval.** Any status path that calls
  `POST .../claim` burns the approval, and the real retry then reads
  EXECUTED.
- **A sidecar review has no requester.** Every one is owned by
  `hoop@hoop.dev`, so "my pending reviews" cannot be answered.
- **Approval state lives in the control plane, and the sidecar dials out to
  it.** The control plane never learns the sidecar's address.

## Options considered

### How the agent learns the review exists

1. **Keep the hold; send progress to the client.** Postgres can send
   NoticeResponse during a statement. An agent's shell tool returns output
   only when the command ends, so it sees nothing more than today. Most
   other protocols have no in-band notice at all.
2. **Hold briefly, then deny.** One wait budget below agent timeouts, for
   everyone. No detection needed, but every human loses the 30-minute hold
   that EVL-307 introduced.
3. **Deny at once with the review id, per listener or per connection.**
   Chosen. Humans keep the hold; agents get an answer they can act on.

### How a connection is marked as an agent

4. **Detect agents automatically** (client name, user agent, timing).
   Unreliable, and a wrong guess changes behavior silently.
5. **Identity-based** (a principal or group marks a workload). The right
   long-term answer, and it depends on ADR-0019 or a successor. Deferred.
6. **Listener default plus client opt-in.** Chosen. Works on every protocol
   now through the listener setting, and a shared listener serves both kinds
   of client once the protocol has an opt-in signal.

### Where the agent asks about the review

7. **The control plane's `/mcp`.** Served in control-plane mode today, with
   `reviews_get` and `reviews_wait`. The agent would need a control plane
   identity, and the same toolset has `reviews_list` (every statement in the
   organization) and `reviews_update` (an agent in a reviewer group can
   approve its own statement). It also adds an inbound path from agents to
   the control plane.
8. **Retry the statement; no MCP.** Works with any client. Each poll runs
   the analyzer and a control plane call. An agent that reformats the
   statement files a new review and pages the approvers again.
9. **A small MCP server in the sidecar.** Chosen. It asks the control plane
   with the sidecar token it already holds, so it sees only this sidecar's
   reviews and keeps the outbound-only network shape.
10. **MCP Tasks.** The protocol's answer to long tool calls, an extension
    since the 2026-07-28 specification. Client support is uneven, and it
    needs MCP to carry the statement, which is MCP Bridge scope.

## Decision

We add a second way to resolve `require_review`, and a status channel for
it.

**Review mode.** The listener analyzer block gets
`review_mode: hold | return`. `hold` is the default and is today's
behavior. In `return` mode the lane files the review and denies at once
when it is PENDING, with the review id and the reason in the message. The
agent resends the identical statement after approval, and the control plane
releases it once. No control plane change is needed for this part.

**Client opt-in.** A client may request `return` for its own connection.
Neither mode releases a statement without an approval, so the choice
moves the wait and never weakens the gate. HTTP and gRPC use the header (metadata key)
`x-hoop-review-mode: return`. Signals for the SQL protocols are decided per
protocol in their tickets. The audit record stores the resolved mode and
whether the listener or the client chose it.

**Read-only status route.** The control plane adds
`GET /api/sidecars/reviews/:id`, scoped to the calling sidecar by its
token. It never changes the review and returns no statement text.

**Sidecar MCP server.** An optional `mcp:` block starts a streamable-HTTP
MCP server with two tools: `review_status` and `review_wait`. The wait
polls every 2 seconds, defaults to 60 seconds, stops at 300, and sends
progress notifications, the same numbers as the gateway's `reviews_wait`.
Each result says what to do next: wait again, resend the identical
statement, or stop. The server lives in its own nested module, because the
sidecar root module depends on libhoop only. It has no authentication, the
same as the listener ports.

**No approving, listing or executing over this MCP.** Approval stays with
humans in the control plane and Slack. Executing statements over MCP is MCP
Bridge scope. There is no list tool until a sidecar review has a requester.

## Consequences

- An agent gets an answer in one round trip and can follow the review
  without a human relaying it.
- Humans on a `hold` listener see no change.
- **`review_mode: return` applies to every client on the listener, humans
  included.** Operators give agents their own listener, or keep `hold` and
  have agents opt in. Until a protocol has an opt-in signal, the separate
  listener is the only choice on it.
- **The retry must be byte-identical.** A reformatted statement is a new
  review and a new Slack message. We keep the exact-bytes binding, because
  it is what stops an approval of `id = 1` from releasing `id = 999`. The
  deny message and every tool result say so.
- **A deny on a relayed SQL lane closes the connection** (`proxy.DenyWriter`
  writes the frame, then closes). An open transaction is lost, and the agent
  reconnects to retry. Return mode fits autocommit statements.
- The MCP endpoint answers anybody who can reach it. It exposes review
  status and the rule and listener names for a known review id, and no
  statement text. Operators bind it where only agents reach it.
- A sidecar newer than its control plane gets 404 from the status route. The
  tool must report an old control plane, not a missing review.
- MCP Bridge phase 2 plans a sidecar-native MCP. Its server should extend
  this module and this `mcp:` block, not add a second one. The Deploy team
  reviews that seam in this ADR.
- If ADR-0019 or a successor gives the sidecar a verified workload
  identity, a principal or group can select `return` instead of the client.
  That is a new ADR amending the opt-in rule, not a change to review mode.
