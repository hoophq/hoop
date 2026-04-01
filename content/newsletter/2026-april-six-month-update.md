# Hoop Product Update: October 2025 - March 2026

**Subject line options:**
- "6 months of Hoop: RDP recording, AI session analysis, ABAC, and a lot more"
- "What's new in Hoop: The biggest update recap we've ever done"
- "You asked, we shipped: 6 months of Hoop product updates"

---

Hey there,

We've been heads-down shipping and realized we haven't told you about... any of it. That changes now. Here's everything that landed in Hoop over the last 6 months — and why it matters for your team.

If you're self-hosted, **we strongly recommend upgrading to v1.54.1** to get all of these improvements.

---

## The Big Stuff

### RDP Session Recording & Playback (v1.52.0)

You can now **record and play back full RDP sessions**. We integrated a Rust runtime (wazero) inside Go and built a web player with timeline scrubbing, playback controls, and fullscreen support. Open an RDP session, do your work, close it — then replay the entire thing from the session list.

This was a 3,200+ line change across 37 files. We also upgraded to Go 1.24 as part of this work.

**Why upgrade:** If your compliance team needs to audit remote desktop access, this eliminates "trust me, I just checked the server." Now there's a recording.

*PR #1314 by @racerxdl — [ENG-30]*

### AI Session Analyzer (v1.52.0 - v1.54.1)

Hoop can now **automatically analyze sessions using AI** and flag risks based on rules you configure per role. Under the hood, we built a provider-agnostic AI client layer supporting OpenAI-compatible APIs and Anthropic, with tool-calling and structured-output support. The analyzer integrates into the session lifecycle — it can **block execution** if it detects a rule violation, not just report on it after the fact.

How it works:
1. Configure analysis rules for specific roles (e.g., "flag any DROP TABLE command," "alert on access to sensitive schemas")
2. Hoop runs every session through the AI analyzer
3. Results are persisted in the session record and surfaced in the UI
4. If a rule triggers a "block" action, the session is stopped before execution

Recent fixes (v1.54.1) removed a provider check that was blocking non-admin users from seeing analyzer results on the terminal page — the UI now loads the analyzer rule based on whether one exists for the role, without requiring admin-level provider configuration.

**Why upgrade:** Stop manually reviewing session logs. The AI catches dangerous commands, policy violations, and anomalies — and can block them in real-time.

*PR #1306 by @EmanuelJr, PR #1340 by @luanlorenzo, PR #1347 by @luanlorenzo — [ENG-269]*

### Attribute-Based Access Control / ABAC (v1.53.0)

We built an **Attributes** system that acts as a grouping layer between connections and policy rules. Instead of linking guardrail rules, access request rules, and data masking rules directly to individual connections, you create Attributes and associate them with both connections and rules. This gives you reusable, composable policy assignments.

The system includes:
- Full CRUD API for attributes (admin-only)
- Junction tables linking attributes to connections, access request rules, guardrail rules, and data masking rules
- Attribute-aware rule resolution that falls back to direct connection-based lookup when no attributes exist (backward compatible)
- Frontend: attribute management in Settings, inline attribute creation in role configuration, attribute filtering in access request rules

This was a 2,700+ line change across 51 files — fully additive and backward-compatible. If a connection has no attributes assigned, everything works exactly as before.

**Why upgrade:** If your access control needs have outgrown "assign rules per connection," ABAC gives you the composability you need without breaking existing config.

*PR #1319 by @EmanuelJr — [ENG-257]*

### Audit Logs (v1.50.1 - v1.52.0)

Two PRs built this feature end-to-end:

**Phase 1 (v1.50.1):** Added audit event logging to every admin mutation handler — users, connections, resources, service accounts, guardrails, data masking, agents, org keys, and server config. Each event records who changed what and the payload. New `security_audit_log` table and `GET /audit/log` endpoint.

**Phase 2 (v1.52.0):** Added an HTTP middleware (`AuditMiddleware`) that automatically captures ALL write operations (POST/PUT/PATCH/DELETE) on the gateway. The middleware wraps the response writer, captures request bodies, and writes audit entries asynchronously to avoid latency. This replaced the inline audit calls from Phase 1, removing duplicated code across handlers.

The frontend is a full audit logs page under `/audit-logs` with expandable rows showing timestamp, actor, operation, HTTP path, outcome badge, detail panel with IP address, redacted payload, and error messages. Skips GET/HEAD/OPTIONS and health checks.

**Why upgrade:** "Who changed that permission last Tuesday?" — now you can actually answer that.

*PR #1279 by @matheusfrancisco — [ENG-246], PR #1305 by @rogefm — [ENG-247]*

### Streaming Large Session Results (v1.50.0)

New `GET /sessions/{session_id}/result/stream` endpoint that streams session results to the browser instead of loading everything into memory. Supports filtering by event type (input, output, error), optional newline separators, and RFC3339 timestamps. The frontend uses virtualized rendering — only visible rows are in the DOM. Sessions over 4MB get a download button in the header.

**Why upgrade:** If you've ever had a browser tab die from a large SQL result set, this fixes that. The old large-payload-warning component is gone; now it just works.

*PR #1292 by @rogefm*

---

## Access Control & Security

### Access Request Rules (v1.50.0)

Completely redesigned rule management system — a dedicated "Access Request" feature replacing the old plugin-based review architecture. New API, data model, and UI with separated sections for access type, time range, connections, user groups, and approval configuration. Free tier gets 1 rule; enterprise gets unlimited.

The old "Reviews" pages have been removed from the webapp (PR #1298, -994 lines). Review workflows now live in the Sessions view. Slack notifications link to sessions instead of the removed reviews page.

*PR #1275 by @luanlorenzo, PR #1298 by @rogefm*

### Force Approval (v1.48.0)

Designated groups can now **force-approve reviews** for emergency scenarios. The backend adds `force_approve_groups` to connections and a `forced_review` flag to review_groups. The frontend shows a "Force Approve" option in the approval dropdown (only visible to authorized groups) with a distinct `CheckCheck` icon.

Also added: **minimum review approvals** threshold (v1.48.2) so critical access changes require sign-off from multiple reviewers.

*PR #1228 by @EmanuelJr, PR #1248 by @rogefm*

### JIT Review Gates for Native Clients (v1.53.0)

Previously, native client credentials were handed out before any approval had a chance to intervene — sessions were only created later when the native client connected to the proxy. This PR moves session creation to the moment of the credential request and wires the review/JIT system into the flow.

Now: if a connection has reviewers or a JIT access request rule, `POST /credentials` returns HTTP 202 with a pending review instead of credentials. A new `POST /credentials/:sessionID` resume endpoint releases credentials after approval. All five proxy daemons (postgres, ssh, http-proxy, rdp, ssm) read `session_id` from the credential and reuse the pre-existing session.

The frontend replaces the duration picker with a JIT callout showing the fixed access window and a "Request Access" button. On pending review, the native client modal swaps to the session details modal.

*PR #1317 by @rogefm — [ENG-266]*

### Mandatory Metadata (v1.51.0)

Admins can configure roles/connections to require specific metadata fields before running a terminal session or runbook. The backend validates `mandatory_metadata_fields` on the API; the frontend shows a proactive callout when requirements exist and opens a metadata form modal before execution. Works in both terminal and runbook flows.

*PR #1299 by @p3rotto — [ENG-258], PR #1302 by @luanlorenzo — [ENG-259]*

### Data Masking Improvements

- **AI data masking for free users** (v1.49.3)
- **Data masking for HTTP proxy** with guardrail errors saved in audit logs (v1.49.3)
- **Column-aware pre-anonymization for PostgreSQL** — experimental eager mode (v1.53.2)
- **Presidio performance:** Added Envoy proxy for load balancing (least-requests policy), tuned Gunicorn for higher concurrency, added rolling update strategy (PR #1332 by @sandromello)
- **MSPresidio redactor for MySQL** (v1.43.0)
- **Guard rails validation via MSPresidio** (v1.42.3)

---

## Infrastructure & Connections

### Resources & Roles System (v1.44.0)

Complete architectural refactoring — 4,600+ lines across 103 files. Replaced the flat "connections" model with a hierarchical **Resources → Roles** system. A Resource groups related roles with different access levels (e.g., Resource "PostgreSQL Production" with roles `readonly`, `readwrite`, `admin`).

Includes: 4-step setup wizard, infinite scroll, advanced filtering with debounced search, dynamic form inputs per connection type, and the Command Palette updated to use the new model.

*PR #1120 by @rogefm*

### Claude Code Integration (v1.49.9)

Full support for Claude Code as a new resource/role type. Users can create and configure Claude Code connections with Anthropic API credentials (API URL + API Key). The native client access tab shows instructions for configuring `~/.claude/settings.json`. AI coding assistants now get the same access controls, approval workflows, and audit logging as everything else in Hoop.

*PR #1277 by @rogefm*

### IronRDP Web Proxy (v1.47.0)

Implemented the IronRDP web client gateway — a WebSocket endpoint (`/iron`) that lets users connect to RDP sessions directly from the browser. Added "Open Web Client" button to the credentials dialog. This was a 5,000+ line change across 29 files.

*PR #1188 by @racerxdl — [ENG-29]*

### SSH: SCP/rsync Large File Fix (v1.52.4)

The SSH proxy was closing the gRPC stream before all data was flushed, causing SCP to fail on large transfers. The agent side had 2-second timeouts that silently dropped data under backpressure, and spawned unbounded goroutines (each holding 32KB) that consumed hundreds of MBs. Fix: removed timeouts so writes block correctly, made SSH data processing synchronous for proper gRPC flow control, and added guards against late packets. Successfully tested with 6GB SCP transfers.

*PR #1338 by @matheusfrancisco — Fixes #1327*

### Secrets Manager Integration (v1.47.0)

New UI selector for secrets manager providers in role credentials configuration. Supports manual input, secrets manager, and AWS IAM role as connection methods. Environment variable inputs now have a source selector adornment when secrets manager is selected.

*PR #1193 by @luanlorenzo*

### Grafana & Kibana (v1.48.0)

Added `grafana` and `kibana` as connection subtypes, treated as HTTP proxies. Includes custom icons and reuses the HTTP proxy form components.

*PR #1232 by @luanlorenzo*

### AWS RDS IAM Authentication (v1.44.3)

Prefix convention in user/password fields (`_aws_iam_rds:<username>` / `_aws_iam_rds:autogen`) triggers IAM RDS token generation. Also added MySQL `--enable-cleartext-plugin` flag for IAM-authenticated connections (required by AWS).

*PR #1131, #1137 by @matheusfrancisco*

### Other Database & Protocol Improvements
- **MSSQL:** Web terminal fixes and database listing (v1.46.7 - v1.49.8)
- **OracleDB:** Connection testing and query fixes (v1.42.3 - v1.44.2)
- **PostgreSQL:** TLS termination and proxy fixes (v1.44.4 - v1.44.6)
- **MongoDB:** Positional argument handling fix preventing parameter mishandling (v1.51.2)
- **Kubernetes:** Bearer token support, EKS integration, normalized token handling (v1.46.2 - v1.52.0)
- **AWS SSM:** Native client support, WebSocket URL fix for `ws://` vs `wss://` behind reverse proxies (v1.46.2 - v1.49.6)
- **HTTP Proxy:** Promoted to first-class connection type with modal interface, guardrails validation, and header/env var validation (v1.47.0 - v1.49.7)

### Gateway & Infrastructure
- Go upgraded to 1.24 with wazero Rust runtime (v1.52.0)
- Helm chart consolidation — multiple service types into a single config (v1.44.4)
- Asynchronous agent controller packet processing (v1.46.4)
- Goroutine and memory leak fixes (v1.42.3)
- libhoop version tagging aligned with main release (v1.51.2)

---

## Runbooks v2 (v1.43.0 - v1.46.0)

Major overhaul with multi-repository support, caching, and connection-based access control. The new system uses `NewConfigV2()` with SSH/basic auth, supports custom branch selection with fallback, and caches with 5-minute TTL.

Also shipped:
- **Runbooks migration** — auto-converts existing plugin configs to V2 format (v1.46.0)
- **File upload support** (v1.48.0)
- **Field ordering** via `order` template function (v1.49.5)
- **Jira integration** — required-fields modal before execution (v1.51.2)
- **Parallel execution mode** with enhanced UI (v1.48.0)
- **Default runbooks** for new organizations (v1.46.11)

*PR #1107 by @EmanuelJr, PR #1119 by @EmanuelJr*

---

## UX & Accessibility

### Connection Filter with Infinite Scroll (v1.52.1)

Reusable filter component deployed across **7 features** — access control, AI session analyzer, access request, runbooks setup, AI data masking, guardrails, and Jira templates. Uses Radix UI with 300ms debounced search and infinite scroll to handle 1000+ connections. Also migrated from Headless UI to Radix UI as part of ongoing UI library migration.

*PR #1323 by @rogefm — [ENG-255]*

### Terminal Accessibility (v1.50.1)

Comprehensive accessibility pass: skip-to-content links, ARIA tree keyboard navigation for database schema (Arrow keys, Home/End), proper `tablist`/`tab` ARIA patterns for output tabs, keyboard-focusable output with dynamic aria-labels announcing status and line count, and screen-reader-only hints in the editor. 789 lines changed across 19 files.

*PR #1295 by @rogefm — [ENG-244]*

### Reviews → Sessions Consolidation (v1.51.0)

Removed the dedicated Reviews pages entirely (-994 lines). Review workflows now live in the Sessions view. Slack notifications updated to point to sessions. Added "Access Request" status filter (PENDING/APPROVED/REJECTED) in audit filters.

*PR #1298 by @rogefm*

### Other UX Improvements
- **Session search by IDs** (v1.46.3)
- **Kill session** functionality in session header (v1.49.11)
- **Clipboard copy/cut blocking** with conditional UI hiding (v1.49.7)
- **CMDK search reset** fix (v1.51.2)
- **Inline tabular results** restored in runbooks (v1.51.1)
- **Anonymized information** in session detail modal (v1.49.0)
- **SAML login improvements** and Safari cookie compatibility (v1.47.2 - v1.51.1)
- **README rewrite** — new hero section, concrete before/after examples showing data masking and guardrails, architecture diagram, expanded protocol coverage table (v1.54.1, PR #1341 by @andriosrobert)

---

## Analytics & Observability

Session usage analytics now tracks `hoop-session-created`, `hoop-session-reviewed`, and `hoop-session-finished` events. Client version is included in tracking events. Analytics send by default for new orgs (opted-in at the infrastructure level, replacing the old `ANALYTICS_TRACKING` env var).

*PR #1315 by @p3rotto — [ENG-274]*

---

## Upgrading

If you're on a self-hosted deployment, upgrading to **v1.54.1** gets you everything above. Check the [releases page](https://github.com/hoophq/hoop/releases) for migration notes on specific versions.

Key migrations to be aware of:

| Version | Migration |
|---------|-----------|
| v1.42.3 | Guard rails validation via MSPresidio |
| v1.44.0 | New resources/roles system (103 files changed) |
| v1.46.0 | Runbooks v2 migration (auto-converts plugin configs) |
| v1.48.0 | Force approval groups + `force_approve_groups` column |
| v1.50.0 | Access request rules (new data model) |
| v1.51.0 | Mandatory metadata fields |
| v1.52.0 | Audit logs HTTP fields migration, RDP recording |
| v1.53.0 | ABAC attributes system (junction tables) |

---

## By the Numbers

Over the last 6 months:
- **70+ releases** (v1.42.3 → v1.54.1)
- **Major features:** RDP recording, AI session analyzer, ABAC, audit logs, streaming sessions, access request rules, JIT review gates, mandatory metadata, Claude Code integration, Runbooks v2, secrets manager, IronRDP web proxy
- **Contributors:** @rogefm, @EmanuelJr, @luanlorenzo, @matheusfrancisco, @racerxdl, @p3rotto, @sandromello, @andriosrobert, @mtmr0x

---

## What's Next

We're continuing to invest in AI-powered session analysis, deeper audit capabilities, and making Hoop the simplest way to secure and observe access to your infrastructure.

Questions? Hit reply — we actually read these.

— The Hoop Team
