# Individual Feature Posts — Ready to Copy/Paste

These are standalone posts for each major feature, sourced directly from PR descriptions. Schedule per the posting-schedule.md cadence or use ad-hoc.

---

## 1. RDP Session Recording

### Twitter/X
> We integrated a Rust runtime (wazero) inside Go and built an RDP session recorder.
>
> Open an RDP session, do your work, close the window — then replay the full session with timeline scrubbing and playback controls.
>
> 3,200 lines of code across 37 files. Also upgraded to Go 1.24.
>
> Available in Hoop v1.52.0.

### LinkedIn
> We shipped RDP session recording in Hoop.
>
> Under the hood: we integrated a Rust runtime (wazero) inside Go to handle the recording, built a web player with timeline scrubbing, playback speed controls, and fullscreen support, and upgraded to Go 1.24 in the process.
>
> The workflow: open an RDP session, do your work, close it, then find the recording in your session list and replay it. Compliance teams get complete visual evidence of what happened during any remote desktop session.
>
> No third-party recording tools. No "trust me, I just checked." A recording.
>
> PR #1314 — 3,200+ lines, 37 files, 16 commits.

---

## 2. AI Session Analyzer

### Twitter/X
> Hoop's AI Session Analyzer can now block dangerous commands before they execute.
>
> We built a provider-agnostic AI client layer (OpenAI, Azure OpenAI, Anthropic) with tool-calling and structured output.
>
> Configure rules per role. The AI evaluates every session. Risk maps to actions — including "block."
>
> 3,640 lines across 46 files. Not just logging. Enforcement.

### LinkedIn
> We built an AI session analysis engine into Hoop that doesn't just report on sessions — it can block them.
>
> The architecture: a provider-agnostic AI client layer supporting OpenAI-compatible APIs and Anthropic, with tool-calling and structured-output support. The analyzer integrates into the session lifecycle at two points — session creation and audit plugin receive.
>
> How it works:
> 1. Configure analysis rules per role
> 2. Every session runs through the AI analyzer
> 3. Risk results map to actions (allow, flag, block)
> 4. Block actions stop execution before it happens
> 5. Results persist as ai_analysis JSON in the session record
>
> This is enforcement, not just observability.
>
> PR #1306 — 46 files, 22 commits.

---

## 3. ABAC (Attribute-Based Access Control)

### Twitter/X
> Hoop now has ABAC — but not the kind you might expect.
>
> We built an "Attributes" system that sits between connections and policy rules. Create an attribute, link it to connections AND to guardrail/access/masking rules. Policies become reusable and composable.
>
> No attributes assigned? Everything works exactly as before. Fully backward-compatible.
>
> 2,755 lines, 51 files.

### LinkedIn
> We shipped Attribute-Based Access Control in Hoop — but we designed it as a grouping layer, not a traditional ABAC engine.
>
> The problem: assigning guardrail rules, access request rules, and data masking rules directly to individual connections doesn't scale. You end up with the same policy applied to 50 connections, managed 50 times.
>
> The solution: Attributes. Create an attribute (e.g., "production-databases"), link it to connections and to rules. When Hoop resolves policies, it looks up attributes first, then falls back to direct connection-based lookup.
>
> It's fully backward-compatible — connections with no attributes work exactly as before. The attribute layer is additive only.
>
> Full CRUD API, junction tables with foreign keys, inline attribute creation in the UI, searchable filter popovers.
>
> PR #1319 — 51 files, 2,755 lines.

---

## 4. Audit Logs

### Twitter/X
> "Who changed that permission last Tuesday?"
>
> Hoop now has audit logs with automatic HTTP middleware.
>
> Phase 1: Event-based logging on every admin mutation handler.
> Phase 2: HTTP middleware that captures ALL write ops (POST/PUT/PATCH/DELETE) asynchronously.
>
> Full UI with expandable rows, actor, operation, HTTP path, outcome badge, IP, and redacted payloads.

### LinkedIn
> We built Hoop's audit logging system in two phases:
>
> Phase 1 added explicit audit events to every admin mutation handler — users, connections, resources, service accounts, guardrails, data masking, agents, org keys, and server config.
>
> Phase 2 replaced those inline calls with an HTTP middleware that automatically captures all write operations. The middleware wraps the response writer, captures request bodies, and writes entries asynchronously to avoid latency impact.
>
> The frontend: a full /audit-logs page with expandable rows showing timestamp, actor, operation, HTTP path, outcome badge, and a detail panel with IP address, redacted payload, and error messages.
>
> PRs #1279 and #1305.

---

## 5. JIT Review Gates for Native Clients

### Twitter/X
> Found a security gap in our own native client flow and fixed it.
>
> Before: credentials were handed out before any approval could intervene.
> After: POST /credentials creates a session immediately. If review is required, returns HTTP 202 (no credentials). Resume endpoint releases them after approval.
>
> All 5 proxy daemons (postgres, ssh, http-proxy, rdp, ssm) now share a single audited session per credential.

### LinkedIn
> We found and fixed a security gap in Hoop's native client credential flow.
>
> Previously, native client credentials were issued before any approval could intervene — sessions were only created later when the client connected. This meant credentials could be handed out without a review gate.
>
> The fix: session creation now happens at the credential request. If a connection has reviewers or a JIT access request rule, the endpoint returns HTTP 202 with a pending review instead of credentials. A new resume endpoint releases credentials after approval.
>
> All five proxy daemons (postgres, ssh, http-proxy, rdp, ssm) now read session_id from the credential and reuse the pre-existing session — so audit trails are consistent.
>
> PR #1317 — 25 files, 829 lines.

---

## 6. Streaming Large Sessions

### Twitter/X
> "Why did my browser tab die?"
>
> Because your session returned 500MB and we were loading it all into memory.
>
> New streaming endpoint: GET /sessions/{id}/result/stream
> - Filters by event type (input, output, error)
> - Virtualized rendering (only visible rows in the DOM)
> - Download button for sessions over 4MB
>
> The old large-payload-warning component is gone. Now it just works.

---

## 7. Claude Code Integration

### Twitter/X
> AI coding assistants are accessing your infrastructure. Are you auditing that access?
>
> Hoop now supports Claude Code as a first-class resource type. Same access controls, approval workflows, session recording, and audit logging as your databases and servers.
>
> Configure with Anthropic API credentials. Native client instructions show how to set up ~/.claude/settings.json to route through Hoop.

---

## 8. SSH Large File Fix

### Twitter/X
> We fixed a bug where SCP failed on large files.
>
> Root cause: the SSH proxy closed gRPC streams before data flushed, agent-side had 2-second timeouts that dropped data under backpressure, and processPacket() spawned unbounded goroutines (each holding 32KB), consuming hundreds of MBs of RAM.
>
> Fix: synchronous data processing, removed timeouts, proper gRPC flow control propagation. Tested with 6GB SCP transfers.
>
> Sometimes the best features are bugs that stop happening.

---

## 9. Mandatory Metadata

### Twitter/X
> "Why did you access production?"
>
> Hoop now lets admins require metadata fields (Jira ticket, access justification, etc.) before a terminal session or runbook can run.
>
> Validated server-side. Shows a proactive callout in the UI. Can't skip it.
>
> Simple feature, but it closes a real compliance gap.

---

## 10. Connection Filter + Infinite Scroll

### Twitter/X
> Shipped a reusable connection filter across 7 features at once:
>
> Access control, AI session analyzer, access request, runbooks, data masking, guardrails, Jira templates.
>
> Radix UI, 300ms debounced search, infinite scroll for 1000+ connections, visual filter badge.
>
> Also migrated from Headless UI to Radix UI. Fixed an infinite render loop with r/with-let along the way.
