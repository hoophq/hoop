# Hoop Quarterly Update — Q1 2026 (January - March)

*Versions: v1.47.0 → v1.54.1*

The biggest quarter in Hoop's history. We shipped RDP session recording, an AI session analyzer that can block dangerous commands, attribute-based access control, a full audit log system, redesigned access requests, and a lot more.

---

## RDP Session Recording & Browser-Based Access

Two major features that work together:

**Browser-based RDP** (v1.47.0): Connect to RDP servers directly from the Hoop UI — no local RDP client needed. Click "Open Web Client" and you're in a remote desktop session in your browser.

**Session Recording** (v1.52.0): Every RDP session can now be fully recorded and replayed. Finish a session, and the recording appears in your session list. Play it back with timeline scrubbing, speed controls, and fullscreen mode.

**What this means for you:** Remote desktop access is now fully auditable. Compliance teams get recordings as evidence. Users don't need to install RDP clients. The entire workflow — access, use, review — happens in the browser.

---

## AI Session Analyzer — Block Dangerous Commands Before They Execute

This isn't just another logging feature. The AI analyzer sits in the execution path and can **stop a command before it runs**.

Configure rules per role:
- "Block any DROP TABLE on production"
- "Flag queries accessing PII columns"
- "Alert on bulk DELETE operations"

When a developer runs a command, the AI evaluates it against the rules. Block rules prevent execution and explain why. Flag rules let it run but attach the analysis to the session for later review.

Works with OpenAI, Azure OpenAI, Anthropic, or any OpenAI-compatible provider — bring your own API key.

Any user can see the analysis results (not just admins). The analyzer works transparently — if a command is blocked, the user sees the reason in the terminal.

**What this means for you:** Your security posture goes from reactive to proactive. Instead of discovering a destructive command in yesterday's logs, you prevent it from executing today. This scales your security review to every session without adding reviewers.

---

## ABAC: Manage Policies at Scale

**The problem this solves:** If 30 production databases need the same guardrail policy, you were configuring it 30 times.

**How it works:** Create **Attributes** (like "production," "pci-scope," "customer-data") and assign them to connections. Then link your guardrail rules, access request rules, and data masking rules to the attribute. One policy change propagates to every connection with that attribute.

Add a new database? Tag it with the right attribute and it inherits all the rules automatically.

Nothing breaks if you don't use it — the system falls back to direct connection-based rules when no attributes are assigned.

**What this means for you:** Policy management at scale. Instead of maintaining rules per connection, maintain them per attribute. Fewer misconfigurations, faster onboarding of new infrastructure, and a single place to audit what policies apply where.

---

## Audit Logs — Complete Admin Activity Trail

Every administrative change in Hoop is now logged automatically: who made it, when, from what IP, and what changed. Covers users, connections, resources, guardrails, data masking, service accounts, agents, and server config.

A dedicated Audit Logs page lets admins browse, filter, and drill into any entry. Each row expands to show the actor, operation, outcome, IP address, and a redacted view of the change payload.

Logging happens asynchronously — zero impact on normal operations.

**What this means for you:** Audit and compliance requirements (SOC 2, ISO 27001, HIPAA) demand admin change trails. This gives you one out of the box. For incident response, "who changed that permission?" is answered in seconds, not hours.

---

## Redesigned Access Request System

The approval workflow was rebuilt from scratch. Each rule now defines:
- **What** resources it applies to
- **Who** can request access
- **When** access is allowed (time windows)
- **How** it's approved (minimum approvals, force-approve for emergencies)

Review workflows are integrated directly into the Sessions view — no separate Reviews page. Slack notifications link to the session context.

**What this means for you:** Differentiated access policies in minutes. Production databases require 2 approvals during business hours. Staging needs 1. Emergency access has a fast-track with full audit trail. All configurable without code.

---

## Approval Gates for Native Clients

Previously, using `psql`, `ssh`, or `kubectl` through Hoop bypassed the approval process — credentials were issued first, review happened later (or not at all).

Now the approval gate happens at the moment you request credentials. If a connection requires review, you see a pending status. Once approved, credentials are released. This works for all proxy types: PostgreSQL, SSH, HTTP proxy, RDP, and SSM.

**What this means for you:** Your security policies are enforced consistently regardless of how users access infrastructure. No more "native client loophole." Every credential issuance has a session and an audit trail.

---

## Mandatory Metadata — Business Context on Every Session

Require Jira ticket numbers, access justification, incident IDs, or any custom field before a session or runbook can execute. Validated server-side — users can't skip it. The UI shows a clear prompt when requirements exist.

**What this means for you:** Every session has business context from the start. Post-incident analysis is faster ("here's the Jira ticket"). Compliance reporting is simpler. It creates a natural friction point that encourages thoughtful access.

---

## Claude Code Integration

AI coding assistants now go through the same access controls, approval workflows, session recording, and audit logging as everything else in Hoop. Claude Code is a first-class resource type — configure it with your Anthropic API credentials and route through Hoop.

**What this means for you:** As AI coding tools access your infrastructure, they don't create a blind spot in your security posture. Same rules, same audit trail, same governance.

---

## Streaming Large Session Results

Run a query that returns a massive result set and... it just works. Results stream to the browser instead of loading all at once. The UI renders only visible rows. Sessions over 4MB get a download button.

**What this means for you:** No more crashed browser tabs. Data engineers, DBAs, and analysts can view large results directly in the UI regardless of size.

---

## SSH Large File Transfer Fix

SCP and rsync of large files were failing — data corruption, incomplete transfers, and memory issues. We found and fixed the root cause: the proxy was dropping data under pressure and leaking resources. After the fix, 6GB transfers work reliably.

**What this means for you:** Teams using SCP/rsync for file transfers, backups, or deployments through Hoop can now handle large files without issues.

---

## Data Masking — Faster, Broader, Free

- **AI data masking is now free** for all users, including open-source deployments
- **Major performance improvement** — load balancing and concurrency tuning mean masking doesn't bottleneck under peak usage
- **PostgreSQL column-aware anonymization** (experimental) — masks specific columns instead of pattern-matching entire results
- **HTTP proxy data masking** — masking extends beyond databases to any HTTP connection
- **Guardrail violations in audit logs** — see when and why data was blocked

---

## More Improvements

- **Force approval** — designated groups can emergency-approve when the normal process is too slow, with minimum approval thresholds
- **Secrets manager integration** — configure credentials from your secrets manager or AWS IAM role instead of static values
- **Grafana & Kibana** — native connection types for observability tools through Hoop
- **HTTP proxy** promoted to first-class connection type with full configuration UI and guardrails
- **Connection filter with infinite scroll** — unified search across 7 features, handles 1000+ connections
- **Full accessibility pass** — keyboard navigation, screen reader support, ARIA patterns throughout the app
- **Runbooks improvements** — file uploads, field ordering, Jira integration, parallel execution
- **SAML improvements** — better Safari compatibility and auth protocol selection
- **Session analytics** — usage tracking for session lifecycle events

---

## Upgrading

All of this is available in **v1.54.1**.

Key versions:

| Version | What's New |
|---------|-----------|
| v1.47.0 | Browser-based RDP, secrets manager, HTTP proxy config |
| v1.48.0 | Force approval, parallel runbooks, Grafana/Kibana |
| v1.50.0 | Access request rules, streaming sessions |
| v1.51.0 | Mandatory metadata, reviews → sessions consolidation |
| v1.52.0 | RDP recording, AI analyzer, audit logs |
| v1.53.0 | ABAC, approval gates for native clients |
| v1.54.0 | Session analytics, AI analyzer improvements |

---

**This was our biggest quarter ever.** If you're self-hosted and haven't upgraded recently, now is the time.

Questions? Hit reply — we'd love to hear from you.
