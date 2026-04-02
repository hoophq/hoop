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

### Record and Replay RDP Sessions (v1.52.0)

Every RDP session through Hoop can now be fully recorded and replayed. Open a session, do your work, close the window — the recording appears in your session list with timeline scrubbing, speed controls, and fullscreen.

We also shipped a browser-based RDP client (v1.47.0), so your team can connect to RDP servers directly from the Hoop UI — no local RDP client needed.

**Why upgrade:** Compliance teams get verifiable evidence of every remote desktop session. Incident response goes from "ask the person what they did" to "watch the recording." For regulated industries, this can be the difference between passing and failing an audit.

### AI Session Analyzer (v1.52.0 - v1.54.1)

Hoop now runs sessions through an AI analyzer that evaluates risk based on rules you configure per role. The key: **it can block execution before it happens**, not just report after the fact.

1. Admin creates rules for a role (e.g., "block destructive DDL on production," "flag queries accessing PII columns")
2. Developer runs a command
3. AI evaluates it against the rules
4. Block rules stop the command. Flag rules let it execute but attach the analysis to the session for review.

Works with OpenAI, Azure OpenAI, Anthropic, or any OpenAI-compatible API.

**Why upgrade:** Your security posture goes from reactive (finding problems in logs) to proactive (preventing problems before execution). This scales security review without adding headcount.

### Reusable Access Policies with ABAC (v1.53.0)

If 30 production databases need the same guardrail policy, you were configuring it 30 times. Now you can create **Attributes** (like "production-databases" or "pci-scope"), assign them to connections, and link your guardrail, access request, and data masking rules to the attribute. Change the policy once, it applies everywhere.

Fully backward-compatible — if you don't use attributes, nothing changes.

**Why upgrade:** Policy management goes from per-connection to per-attribute. Onboarding new infrastructure becomes "tag it" instead of "configure 5 rule sets." Massive reduction in admin overhead and misconfiguration risk.

### Audit Logs (v1.50.1 - v1.52.0)

Every administrative change is now automatically logged — who did it, when, from what IP, and what changed. Covers users, connections, resources, guardrails, data masking, service accounts, agents, and server config.

A new Audit Logs page lets admins browse, filter, and drill into any entry. Logging is asynchronous so it doesn't slow anything down.

**Why upgrade:** SOC 2, ISO 27001, HIPAA all require admin audit trails. This gives you one out of the box. Incident investigation goes from hours of asking around to minutes of filtering.

---

## Access Control & Security

### Redesigned Access Requests (v1.50.0)

Completely rebuilt approval system. For each rule, define what resources it covers, who can request access, when access is allowed (time windows), and how it's approved (minimum approvals, force-approve groups for emergencies). Review workflows now live directly in the Sessions view.

### Approval Gates for Native Clients (v1.53.0)

Previously, native client credentials (psql, ssh, kubectl) were issued *before* any approval could happen. Now the approval gate happens at the moment you request credentials. If review is required, you get a pending status — not credentials. Once approved, the credentials are released.

Enforced consistently across all five proxy types: PostgreSQL, SSH, HTTP proxy, RDP, and SSM.

### Mandatory Metadata (v1.51.0)

Require Jira ticket numbers, access justification, or any custom fields before a session or runbook can execute. Validated server-side — can't be skipped. Every session gets business context from the start.

### Force Approval (v1.48.0)

Designated groups can emergency-approve reviews when the normal process is too slow. Minimum approval thresholds ensure critical changes still require multiple sign-offs.

### Data Masking — Faster and Free (v1.49.3 - v1.53.2)

- **AI data masking is now free** for all users, including OSS
- **Major performance improvement** — load balancing and concurrency tuning so masking doesn't bottleneck under peak usage
- **Broader coverage** — PostgreSQL column-aware anonymization (experimental), MySQL, HTTP proxy
- **Guardrail violations** now captured in audit logs

---

## Infrastructure & Connections

### Resources & Roles (v1.44.0)

Connections are now organized as **Resources** containing **Roles**. Instead of 3 separate connections for a production database (read-only, read-write, admin), you have one Resource with 3 Roles. A setup wizard, resource catalog, and advanced filtering make configuration straightforward.

### Claude Code Integration (v1.49.9)

AI coding assistants now go through the same access controls, approval workflows, session recording, and audit logging as your databases and servers. Configure with your Anthropic API credentials and route Claude Code through Hoop.

### Database Improvements
- **AWS RDS IAM auth** for MySQL and PostgreSQL — use IAM roles instead of static passwords
- **MSSQL** — web terminal and database listing
- **MongoDB** — fixed parameter handling that could cause incorrect query behavior
- **OracleDB** — connection testing and query reliability
- **PostgreSQL** — TLS termination and proxy stability

### SSH: Large File Transfers Fixed (v1.52.4)

SCP and rsync of large files were failing silently — data corruption, incomplete transfers, and memory issues. We found and fixed a cascade of issues in the SSH proxy. Successfully tested with 6GB transfers after the fix.

### HTTP Proxy (v1.47.0 - v1.52.1)

Promoted to a first-class connection type with dedicated configuration UI, guardrails validation, and automatic token invalidation for expired sessions.

### Secrets Manager (v1.47.0)

Configure connection credentials from your secrets manager or AWS IAM role instead of pasting static values.

### Grafana & Kibana (v1.48.0)

Native connection types so you can access observability tools through Hoop's access control.

---

## Runbooks v2 (v1.43.0 - v1.48.0)

Major overhaul:
- **Multi-repository support** — pull runbooks from multiple Git repos
- **Jira integration** — require ticket fields before execution
- **Parallel execution** — run multiple runbooks simultaneously during incidents
- **File uploads** and **field ordering** for better input forms
- **Default runbooks** for new organizations

Existing config is auto-migrated.

---

## Usability

- **Streaming large results** — sessions with massive outputs (big SQL results, verbose logs) now stream to the browser with virtualized scrolling instead of crashing your tab. Sessions over 4MB get a download button. (v1.50.0)
- **Connection filter with infinite scroll** — unified search across 7 features, handles 1000+ connections smoothly (v1.52.1)
- **Accessibility** — full keyboard navigation, screen reader support, skip-to-content links, ARIA tree navigation for database schemas (v1.50.1)
- **Session search by IDs** and kill session functionality
- **SAML improvements** — better Safari compatibility
- **Improved navigation** and search across the entire UI

---

## Upgrading

If you're on a self-hosted deployment, upgrading to **v1.54.1** gets you everything above. Check the [releases page](https://github.com/hoophq/hoop/releases) for version-specific notes.

Key versions:

| Version | What's New |
|---------|-----------|
| v1.44.0 | Resources & Roles system |
| v1.46.0 | Runbooks v2 (auto-migrates existing config) |
| v1.48.0 | Force approval, parallel runbooks |
| v1.50.0 | Access request rules, streaming sessions |
| v1.51.0 | Mandatory metadata |
| v1.52.0 | RDP recording, AI analyzer, audit logs |
| v1.53.0 | ABAC, approval gates for native clients |

---

## What's Next

We're continuing to invest in AI-powered session analysis, deeper audit capabilities, and making Hoop the simplest way to secure and observe access to your infrastructure.

Questions? Hit reply — we actually read these.

— The Hoop Team
