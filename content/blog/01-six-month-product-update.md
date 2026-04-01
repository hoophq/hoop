# 6 Months of Hoop: Everything We Shipped (October 2025 - March 2026)

*Published: April 2026*
*Covers: v1.42.3 → v1.54.1 (70+ releases)*

We've been shipping features, fixing bugs, and improving Hoop for the past 6 months — and we haven't said a word about it. That changes today.

If you're running Hoop self-hosted, this is your guide to what you're missing and why you should upgrade.

---

## RDP Session Recording & Playback

We integrated a Rust runtime (wazero) inside Go and built a full RDP session recorder and web player. Every RDP session can be recorded and replayed — complete with timeline scrubbing, playback speed controls, and fullscreen mode.

Open an RDP session, do your work, close the window, wait for the session to finish, then find it in the session list and hit play.

This was a 3,200+ line change across 37 files. We also upgraded to Go 1.24 as part of this work (wazero required it), which surfaced some stricter `go vet` checks we had to fix.

The IronRDP web proxy gateway (shipped earlier in v1.47.0) provides the underlying WebSocket endpoint (`/iron`) that makes browser-based RDP possible. That was a 5,000+ line change by itself.

**Why it matters:** Compliance teams need evidence of what happened during remote desktop sessions. This gives them a recording instead of a trust exercise.

*PR #1314 by @racerxdl — 37 files, 16 commits*
*PR #1188 by @racerxdl — IronRDP web proxy, 29 files, 12 commits*

---

## AI Session Analyzer

We built a provider-agnostic AI client layer (`gateway/aiclients`) supporting OpenAI-compatible APIs and Anthropic, with tool-calling and structured-output support. On top of that, we built an AI session analysis flow that integrates into the session lifecycle.

Here's the flow:
1. Admin configures analysis rules per role (e.g., "flag any DROP TABLE," "block access to sensitive schemas")
2. When a session runs, Hoop sends it through the AI analyzer
3. The analyzer returns a risk assessment with rule-matched actions
4. If a rule maps to a "block" action, **the session is stopped before execution**
5. Results are persisted as `ai_analysis` JSON in the session record and shown in the UI

This isn't just reporting — it's enforcement. The AI analyzer sits in the session creation path and the audit plugin receive path, meaning it can prevent dangerous actions, not just log them.

We shipped several follow-up improvements:
- **UI refinements** (PR #1340): Simplified AI model doc links, fixed the editor so it doesn't show "success" when a run is blocked by the analyzer
- **Non-admin access fix** (PR #1347): The terminal page was throwing a 403 for non-admin users because the AI analyzer flow depended on admin-only provider APIs. Fixed by removing the `AdminOnlyAccessRole` middleware from the analyzer rule and attributes GET endpoints

**Review focus from the PR:** AI provider compatibility behavior (`openai`, `azure-openai`, `custom`, `anthropic`) and tool-choice mapping; session blocking semantics to ensure no regressions in the session lifecycle.

*PR #1306 by @EmanuelJr — 46 files, 22 commits, 3,640 lines*

---

## Attribute-Based Access Control (ABAC)

We built an **Attributes** management system — an intermediary layer between connections and policy rules (guardrails, access requests, data masking). Instead of assigning rules directly to individual connections, you create Attributes and link them to both connections and rules. This makes policies reusable and composable.

**Backend:**
- Full CRUD REST API at `/attributes` (admin-only)
- GORM models: `Attribute`, `ConnectionAttribute`, `AccessRequestRuleAttribute`, `GuardrailRuleAttribute`, `DatamaskingRuleAttribute`
- Junction tables with foreign key constraints (migration `000065_attributes`)
- New `services` package abstracting attribute-aware rule resolution, with fallback to direct connection-based lookup

**Frontend:**
- Attributes management screens in Settings (list, create, edit)
- Inline attribute creation in Resource Role configuration
- Attribute selection in Access Request rule forms
- Reusable searchable attribute filter popover
- Roles page filtering via backend query param

The system is fully backward-compatible. No attributes? Everything works exactly as before. The attribute layer is additive only.

*PR #1319 by @EmanuelJr — 51 files, 23 commits, 2,755 lines*

---

## Audit Logs

Built in two phases:

### Phase 1: Event-Based Logging (v1.50.1)

Added `audit.NewEvent(...).Log(c)` calls to every admin mutation handler: users, connections, resources, service accounts, guardrails, data masking, agents, org keys, and server config. Each event records the actor, resource type, action, and full payload.

New `security_audit_log` table and `GET /audit/log` API endpoint.

*PR #1279 by @matheusfrancisco — 22 files, 1,265 lines*

### Phase 2: HTTP Middleware (v1.52.0)

Replaced inline audit calls with an HTTP middleware (`AuditMiddleware`) that automatically captures all write operations (POST/PUT/PATCH/DELETE). The middleware wraps the response writer, captures request bodies, and writes entries asynchronously. Added `HttpMethod`, `HttpStatus`, `HttpPath`, `ClientIP` fields with performance indexes.

The frontend ships a full `/audit-logs` page with expandable rows: timestamp, actor, operation, HTTP path, outcome badge, and a detail panel with IP, redacted payload, and error messages. Skips GET/HEAD/OPTIONS and health checks.

The Phase 1 `NewEvent().Log()` API is now deprecated but still functional.

*PR #1305 by @rogefm — 25 files, 13 commits, 1,219 lines. Includes accessibility improvements across terminal, resources, and shared components.*

---

## Access Request Rules

Complete redesign of the review/approval system. Replaced the old plugin-based architecture with a dedicated Access Request feature — new `AccessRequestRule` resource with full CRUD API, new data model, and new UI.

The rule form has separated sections for:
- Access type configuration
- Time range (access windows)
- Connection selection
- User group assignment
- Approval configuration (minimum approvals, force-approval groups)

Free tier: 1 rule. Enterprise: unlimited.

The old Reviews pages were removed entirely (PR #1298: -994 lines deleted). Review workflows now live in the Sessions view. Slack notifications link to `sessions/{session_id}` instead of the removed `reviews/{review_id}`.

*PR #1275 by @luanlorenzo — 35 files, 33 commits, 2,593 lines*

---

## JIT Review Gates for Native Clients

**The problem:** Native client credentials were handed out before any approval could intervene. Sessions were only created later, when the native client connected to the proxy — meaning credentials were issued without an audit trail or review gate.

**The fix:** Session creation now happens at the moment of the credential request. If a connection has reviewers or a JIT access request rule, `POST /credentials` returns HTTP 202 with a pending review (no credentials). A new `POST /credentials/:sessionID` resume endpoint releases credentials after approval.

All five proxy daemons (postgres, ssh, http-proxy, rdp, ssm) now read `session_id` from the credential and reuse the pre-existing session — so multiple connections using the same credential share a single session in the audit trail.

**Frontend changes:**
- JIT connections replace the duration picker with a callout showing the fixed access window + "Request Access" button
- On pending review, the native client modal swaps to the session details modal
- "Connect" button appears after approval

*PR #1317 by @rogefm — 25 files, 15 commits, 829 lines*

---

## Mandatory Metadata

Admins can now require specific metadata fields (Jira ticket, access justification, etc.) before running a terminal session or runbook.

**Backend** (PR #1299): `mandatory_metadata_fields` added to connection API — GET, POST, PATCH, PUT all support it. Validated server-side.

**Frontend** (PR #1302): Dynamic required fields UI, proactive callout when requirements exist, modal before execution that merges submitted metadata into the execution payload. Works in both terminal and runbook flows.

*PR #1299 by @p3rotto, PR #1302 by @luanlorenzo*

---

## Resources & Roles System

The biggest architectural change in this period: 4,600+ lines across 103 files, 88 commits.

Replaced the flat "connections" model with a hierarchical **Resources → Roles** structure. Before: `postgres-prod` was a connection. After: "PostgreSQL Production" is a Resource containing roles like `readonly`, `readwrite`, `admin`.

Includes a 4-step setup wizard, infinite scroll with advanced filtering (debounced search, tags, resource type), dynamic form inputs per connection type, and the Command Palette updated to use the new model. Routes: `:resources`, `:configure-resource`, `:configure-role`, `:add-resource-role`, `:resource-catalog`.

*PR #1120 by @rogefm*

---

## Claude Code Integration

Full support for Claude Code as a resource/role type. Users create Claude Code connections with Anthropic API credentials (API URL + API Key). The native client access tab shows instructions for configuring `~/.claude/settings.json` to route through Hoop.

AI coding assistants now get the same access controls, approval workflows, session recording, and audit logging as databases and servers.

*PR #1277 by @rogefm — 14 files, 385 lines*

---

## Runbooks v2

Major overhaul with multi-repository support, in-memory caching (5-minute TTL), custom branch selection with fallback, and connection-based access control via `getRunbookConnections()` filtering on runbook rules, connections, and user groups.

Subsequent releases added:
- **Migration system** auto-converting existing plugin configs to V2 format (PR #1119)
- **File upload support** (v1.48.0)
- **Field ordering** via `order` template function (v1.49.5, PR #1261)
- **Jira integration** — required-fields modal before execution (v1.51.2)
- **Parallel execution mode** (v1.48.0)
- **Default runbooks** for new organizations (v1.46.11)

*PR #1107 by @EmanuelJr — 15 files, 13 commits, 3,020 lines*

---

## Streaming Large Session Results

New `GET /sessions/{session_id}/result/stream` endpoint. Supports filtering by event types (input, output, error), optional newline separators, and RFC3339 timestamps. Frontend uses virtualized rendering — only visible rows are in the DOM. Sessions over 4MB get a download button.

The old `large-payload-warning` component is gone. Now it just works.

*PR #1292 by @rogefm — 12 files, 479 lines*

---

## Data Masking

- **AI data masking for free/OSS users** (v1.49.3)
- **HTTP proxy data masking** with guardrail errors saved to audit logs (v1.49.3)
- **PostgreSQL column-aware pre-anonymization** — experimental eager mode (v1.53.2)
- **Presidio performance** (PR #1332): Envoy proxy for load balancing (least-requests policy), Gunicorn tuning for higher concurrency, rolling update strategy for availability
- **MSPresidio for MySQL** (v1.43.0)
- **OSS rule check fixes** for data masking limits (v1.49.7)

---

## SSH Large File Transfer Fix

The SSH proxy had a critical bug for large SCP/rsync transfers. The gateway closed the gRPC stream before all data flushed, and the agent had 2-second timeouts that dropped data under SSH backpressure. On top of that, `go a.processPacket(pkt)` spawned unbounded goroutines (each holding 32KB) during large transfers, consuming hundreds of MBs of RAM.

Fix: removed timeouts so writes block correctly, made SSH data processing synchronous so gRPC flow control propagates end-to-end, fixed the goroutine leak in `handleClientWrite`. Tested with 6GB SCP transfers.

*PR #1338 by @matheusfrancisco — fixes #1327*

---

## Connection Filter with Infinite Scroll

Reusable component deployed across 7 features: access control, AI session analyzer, access request, runbooks setup, AI data masking, guardrails, and Jira templates. Uses Radix UI with 300ms debounced search, infinite scroll pagination, and visual badge when filter is active. Also migrated from Headless UI to Radix UI.

Fixed an infinite render loop using `r/with-let` for component initialization. Handles 1000+ connections efficiently using global pagination state.

*PR #1323 by @rogefm — 12 files*

---

## Terminal & Resources Accessibility

Comprehensive accessibility pass across the webapp:
- **Skip-to-content links** and keyboard navigation
- **ARIA tree navigation** for database schema (Arrow Up/Down/Left/Right, Home/End)
- **Tab pattern** for output tabs with roving tabIndex
- **Dynamic aria-labels** on log output announcing status and line count
- **Screen-reader hints** ("Press Escape to leave the editor")
- **Resources page** refactored to use Radix UI Tabs, semantic `<ul>`/`<li>` lists, ARIA labels on filter popovers

*PR #1295 by @rogefm — 19 files, 789 lines*
*PR #1296 by @luanlorenzo — 2 files, 358 lines*

---

## Other Notable Changes

### Force Approval (v1.48.0)
`force_approve_groups` field on connections. Authorized groups see "Force Approve" in the approval dropdown for emergency scenarios. Also added minimum review approvals threshold.

### Secrets Manager Integration (v1.47.0)
UI selector for secrets manager providers in role credentials. Supports manual input, secrets manager, and AWS IAM role. Environment variable inputs get a source selector adornment.

### Grafana & Kibana (v1.48.0)
New connection subtypes treated as HTTP proxies with custom icons.

### AWS RDS IAM Auth (v1.44.3)
Prefix convention (`_aws_iam_rds:<username>`) triggers IAM token generation. MySQL `--enable-cleartext-plugin` flag for IAM connections.

### HTTP Proxy Token Invalidation (v1.52.1)
Token/secret key invalidation now only triggers on actual credentials-expired errors, not all errors.

### Session Usage Analytics (v1.54.0)
Tracks `hoop-session-created`, `hoop-session-reviewed`, `hoop-session-finished`. Segment client lifecycle properly managed to prevent resource leaks.

### README Rewrite (v1.54.1)
Complete README overhaul: animated hero GIF showing live data masking, before/after examples (PII masking, guardrails blocking DROP TABLE from AI agents), architecture diagram, expanded protocol coverage table, simplified quick start.

---

## Infrastructure

- **Go 1.24** + wazero Rust runtime (v1.52.0)
- **Helm chart consolidation** — single config for multiple service types (v1.44.4)
- **Async agent packet processing** (v1.46.4)
- **Goroutine/memory leak fixes** (v1.42.3)
- **libhoop version tagging** aligned with main release (v1.51.2)
- **SAML login improvements** + Safari cookie fix (v1.47.2 - v1.51.1)
- **CMDK search reset** fix (v1.51.2)

---

## Upgrading

All features available in **v1.54.1**. If you're self-hosted, upgrade.

Key migrations:

| Version | What Changes |
|---------|-------------|
| v1.44.0 | Resources/roles system (103 files) |
| v1.46.0 | Runbooks v2 (auto-migrates plugin configs) |
| v1.48.0 | `force_approve_groups` column |
| v1.50.0 | Access request rules (new data model) |
| v1.51.0 | Mandatory metadata fields |
| v1.52.0 | Audit logs HTTP fields + indexes |
| v1.53.0 | ABAC attributes junction tables |

---

## By the Numbers

- **70+ releases** shipped (v1.42.3 → v1.54.1)
- **Major features:** 12
- **Contributors:** @rogefm, @EmanuelJr, @luanlorenzo, @matheusfrancisco, @racerxdl, @p3rotto, @sandromello, @andriosrobert, @mtmr0x

---

*Questions? Reach out — we'd love to hear from you.*
