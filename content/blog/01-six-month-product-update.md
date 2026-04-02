# 6 Months of Hoop: Everything We Shipped (October 2025 - March 2026)

*Published: April 2026*

We've been shipping features, fixing bugs, and improving Hoop for the past 6 months — and we haven't said a word about it. That changes today.

If you're running Hoop self-hosted, this is your guide to what you're missing and why you should upgrade to **v1.54.1**.

---

## Record and Replay RDP Sessions

**The problem:** When someone accesses a server via Remote Desktop, there's no record of what happened. If something breaks, gets misconfigured, or data is accessed inappropriately, the only evidence is "I just checked the server, it's fine."

**What's new:** Every RDP session through Hoop can now be fully recorded and replayed. Open an RDP session, do your work, close the window — the recording appears in your session list. Play it back with timeline scrubbing, speed controls, and fullscreen.

We also shipped a browser-based RDP client earlier in this cycle (v1.47.0), so your team can connect to RDP servers directly from the Hoop UI — no local RDP client needed.

**Impact:** Compliance and security teams get verifiable evidence of what happened during every remote desktop session. Incident response goes from "ask the person what they did" to "watch the recording." For regulated industries, this can be the difference between passing and failing an audit.

---

## AI Session Analyzer — Prevention, Not Just Reporting

**The problem:** Session logs pile up. Nobody has time to review every database query or terminal command. Dangerous actions — accidental `DROP TABLE`, queries on sensitive data, unauthorized operations — go unnoticed until something breaks.

**What's new:** Hoop now runs sessions through an AI analyzer that evaluates risk based on rules you configure per role. But the key differentiator: it doesn't just report after the fact — **it can block execution before it happens.**

How it works in practice:
1. An admin creates analysis rules for a role (e.g., "block any destructive DDL on production," "flag queries accessing PII columns")
2. A developer runs a command
3. The AI evaluates it against the rules
4. If it matches a "block" rule, the command is stopped and the developer sees why
5. If it matches a "flag" rule, it executes but the analysis is attached to the session for review

Works with OpenAI, Azure OpenAI, Anthropic, or any OpenAI-compatible API — you choose your provider.

**Impact:** Your security posture goes from reactive (finding problems in logs after they happen) to proactive (preventing problems before they execute). This scales your security review capacity without adding headcount — the AI reviews every session, not just the ones someone remembers to check.

---

## ABAC: Reusable, Composable Access Policies

**The problem:** As your infrastructure grows, managing access rules per connection doesn't scale. If 30 production databases need the same guardrail policy, you're configuring it 30 times. When the policy changes, you're updating it 30 times.

**What's new:** Hoop now has an **Attributes** system — a grouping layer that sits between your connections and your policy rules. Create an attribute like "production-databases" or "pci-scope," assign it to the relevant connections, then link your guardrail rules, access request rules, and data masking rules to the attribute instead of individual connections.

Change the policy once, it applies everywhere the attribute is used. Add a new database to the attribute group, it inherits all the rules automatically.

The system is fully backward-compatible. If you don't use attributes, everything works exactly as before — no migration required.

**Impact:** Policy management goes from O(n) per connection to O(1) per attribute. Onboarding new infrastructure into your security policies becomes "tag it with the right attribute" instead of "configure 5 different rule sets." For teams managing dozens or hundreds of connections, this is a significant reduction in admin overhead and misconfiguration risk.

---

## Audit Logs — Know Who Changed What, When, and Why

**The problem:** When a permission changes, a connection gets misconfigured, or a security setting is modified, there's no easy way to trace it back. "Who changed that?" shouldn't require digging through git blame or asking around.

**What's new:** Hoop now automatically logs every administrative change. Every time someone creates, updates, or deletes a user, connection, resource, guardrail, data masking rule, service account, agent, or server config — it's recorded with who did it, when, from what IP, and what the change was.

The Audit Logs page in the UI lets admins browse, filter, and drill into any entry. Each row expands to show the full detail: actor, operation, outcome, IP address, and a redacted view of the payload. Logging happens asynchronously so it doesn't slow down normal operations.

**Impact:** Compliance requirements (SOC 2, ISO 27001, HIPAA) typically demand an audit trail for administrative changes. This gives you one out of the box. For incident response, you can reconstruct exactly what changed and when — turning hours of investigation into minutes of filtering.

---

## Streaming Large Session Results

**The problem:** Run a query that returns a massive result set, and the browser tab crashes. This was a real limitation — users either had to download files manually or risk losing their session.

**What's new:** Large session results now stream to the browser instead of loading into memory all at once. The UI renders only the rows you can see (virtualized scrolling), so even sessions with hundreds of thousands of lines stay smooth. Sessions over 4MB show a download button if you want the raw data locally.

**Impact:** No more crashed browser tabs. No more "I lost my query results." Users working with large datasets — data engineers, DBAs, analysts — can view results directly in the UI regardless of size.

---

## Redesigned Access Request System

**The problem:** The old review/approval system was tightly coupled to a plugin architecture, making it rigid to configure and hard to extend. Setting up approval workflows for different teams and scenarios was more complicated than it needed to be.

**What's new:** A completely redesigned Access Request system with flexible, rule-based configuration. For each rule, you define:
- **What** resources it applies to
- **Who** can request access (user groups)
- **When** access is allowed (time windows)
- **How** it's approved (minimum number of approvals, which groups can force-approve in emergencies)

Review workflows are now integrated directly into the Sessions view — no separate Reviews page to navigate. Slack notifications link straight to the session context.

**Impact:** Admins can set up differentiated access policies in minutes — production databases might require 2 approvals during business hours, while staging environments need just 1. Emergency access gets a "force approve" path for designated groups that still maintains a full audit trail.

---

## Native Client Access: Now With Proper Approval Gates

**The problem:** When users accessed infrastructure through native clients (psql, ssh, kubectl, etc.), credentials were issued immediately — before any approval could happen. The review gate only kicked in when the native client connected to the proxy, meaning credentials were already out the door.

**What's new:** The approval gate now happens at the moment you request credentials, not after. If a connection requires review, you get a pending review status instead of credentials. Once approved, a resume flow releases the credentials. The UI adapts automatically — showing you the review status and a "Connect" button once approved.

This works consistently across all five proxy types: PostgreSQL, SSH, HTTP proxy, RDP, and SSM.

**Impact:** Your security policies are now enforced consistently regardless of how users access infrastructure — web UI or native client. There's no way to bypass the review process by going through the native path. Every credential issuance has a session and an audit trail.

---

## Mandatory Metadata — Context Before Execution

**The problem:** When something goes wrong, the first question is "why were they doing that?" But sessions often lack context — there's no Jira ticket, no justification, no business reason recorded.

**What's new:** Admins can require specific metadata fields before a terminal session or runbook can execute. Fields are configurable per connection — you might require a Jira ticket number for production databases and an incident ID for emergency access. The UI shows a clear callout when requirements exist and prompts for the information before execution. Validation is server-side, so it can't be bypassed.

**Impact:** Every session has business context attached from the start. This makes post-incident analysis faster ("here's the Jira ticket for why this was done"), simplifies compliance reporting, and creates a natural friction point that encourages thoughtful access.

---

## Resources & Roles — A New Way to Organize Infrastructure

**The problem:** The flat connections model (one connection = one configuration) made it hard to represent real-world access patterns. A production PostgreSQL server typically has multiple access levels — read-only for analysts, read-write for developers, admin for DBAs — but each required a separate connection configuration.

**What's new:** Connections are now organized as **Resources** containing **Roles**. A Resource is the infrastructure itself (e.g., "PostgreSQL Production"), and Roles are the access levels available (e.g., `readonly`, `readwrite`, `admin`). A 4-step setup wizard guides you through resource creation, role configuration, and access assignment.

The UI includes a resource catalog, advanced filtering with search and tags, and the Command Palette works with the new model.

**Impact:** Your connection list goes from a flat, hard-to-navigate list to a structured hierarchy that mirrors how your team actually thinks about infrastructure access. Setting up a new database with 3 access levels is one resource configuration instead of three separate connections. Users find what they need faster because resources are grouped logically.

---

## Claude Code Integration — AI Coding Assistants Under Access Control

**The problem:** AI coding assistants access your infrastructure — APIs, databases, internal tools — but they typically bypass the access control and audit logging that applies to human users.

**What's new:** Claude Code is now a first-class resource type in Hoop. Create a Claude Code connection, configure it with your Anthropic API credentials, and AI coding sessions go through the same access controls, approval workflows, session recording, and audit logging as everything else.

The native client tab shows instructions for configuring Claude Code to route through Hoop.

**Impact:** AI tool usage becomes observable and governable. Your security team can see what AI assistants are doing, apply the same guardrails and data masking rules, and maintain the same audit trail. As AI coding tools become more prevalent, this ensures they don't create a blind spot in your security posture.

---

## Runbooks v2 — Multi-Repo, Jira, Parallel Execution

**The problem:** Runbooks v1 was limited to a single repository and lacked integration with ticketing systems. Teams with runbooks spread across multiple repos had to consolidate, and there was no connection between runbook execution and the tickets driving them.

**What's new:**
- **Multi-repository support** — pull runbooks from multiple Git repos with branch selection
- **Jira integration** — require Jira fields to be filled before execution, linking every runbook run to a ticket
- **File uploads** — attach files to runbook executions
- **Parallel execution** — run multiple runbooks simultaneously with a dedicated UI
- **Field ordering** — control the order of input fields in runbook forms
- **Default runbooks** — new organizations start with pre-configured runbooks

Existing runbook configurations are automatically migrated to the v2 format.

**Impact:** Operations teams can organize runbooks by team or service (across repos), enforce ticket linkage for change management compliance, and run multiple remediation actions in parallel during incidents. The Jira integration alone closes a common audit gap between "we ran the runbook" and "here's the ticket that authorized it."

---

## Data Masking — Faster, Broader, and Free for OSS

**The problem:** Data masking was slow under high concurrency, limited to certain database types, and unavailable to open-source users.

**What's new:**
- **AI-powered data masking is now free** for all users, including OSS deployments
- **Significantly faster** — we added load balancing and tuned concurrent request handling, resulting in a major throughput improvement
- **More databases covered** — PostgreSQL gets column-aware pre-anonymization (experimental), MySQL support via MSPresidio, HTTP proxy data masking
- **Better observability** — guardrail violations are captured in audit logs

**Impact:** Sensitive data protection is no longer a paid-only feature. Open-source users get AI data masking out of the box. Performance improvements mean data masking doesn't become a bottleneck during peak usage. Column-aware anonymization for PostgreSQL means more precise masking — replacing specific columns rather than pattern-matching across entire results.

---

## SSH: Large File Transfers Fixed

**The problem:** SCP and rsync transfers of large files through Hoop were failing silently — data corruption, incomplete transfers, and in some cases gateway process crashes.

**What's new:** We found and fixed a cascade of issues in the SSH proxy: the gateway was closing connections before data finished flushing, the agent was dropping data under backpressure due to aggressive timeouts, and a resource leak was accumulating memory over time.

After the fix, we successfully tested 6GB SCP transfers through Hoop with no data loss.

**Impact:** If your team uses SCP or rsync through Hoop for file transfers, backups, or deployments, large files now work reliably. This was a blocking issue for teams moving significant amounts of data through SSH.

---

## Better Navigation for Large Environments

**The problem:** As organizations add more connections, finding and filtering across features (access control, guardrails, data masking, runbooks, etc.) became slow and clunky.

**What's new:** A unified connection filter with infinite scroll, deployed across 7 features simultaneously. Search is instant (debounced), handles 1000+ connections smoothly, and shows a visual indicator when a filter is active so you always know when you're looking at a subset.

**Impact:** Navigating Hoop in large environments is noticeably faster. Admins managing hundreds of connections can find what they need without waiting for pages to load or scrolling through endless lists.

---

## Accessibility

We did a comprehensive accessibility pass across the entire web application:
- **Keyboard navigation** works throughout — skip-to-content links, arrow key navigation in database schema trees, tab patterns for output sections
- **Screen reader support** — dynamic labels announce session status and output size, editor hints explain how to exit
- **Semantic HTML** — proper list structures, ARIA attributes on interactive elements, labeled filter controls

**Impact:** Hoop is now usable by team members who rely on keyboard navigation or screen readers. This isn't just compliance — it's making the product work for everyone on your team.

---

## Other Improvements

- **Force Approval** — Designated groups can emergency-approve reviews when needed, with minimum approval thresholds for critical changes
- **Secrets Manager Integration** — Configure connection credentials from your secrets manager or AWS IAM role instead of pasting values
- **Grafana & Kibana** — Native connection types so you can access your observability tools through Hoop's access control
- **AWS RDS IAM Auth** — Authenticate to MySQL and PostgreSQL using IAM roles instead of static passwords
- **HTTP Proxy as a first-class connection type** — Full configuration UI, guardrails validation, and token lifecycle management
- **SAML improvements** — Better Safari compatibility and auth protocol selection
- **Session analytics** — Usage tracking for sessions (created, reviewed, finished) to understand how your team uses Hoop

---

## Upgrading

All features are available in **v1.54.1**. If you're self-hosted, we recommend upgrading.

Key versions with significant changes:

| Version | What's New |
|---------|-----------|
| v1.44.0 | Resources & Roles system |
| v1.46.0 | Runbooks v2 (auto-migrates existing config) |
| v1.48.0 | Force approval, parallel runbooks, file uploads |
| v1.50.0 | Access request rules, streaming sessions |
| v1.51.0 | Mandatory metadata |
| v1.52.0 | RDP recording, AI analyzer, audit logs |
| v1.53.0 | ABAC, JIT review gates for native clients |

---

*Questions? Reach out — we'd love to hear from you.*
