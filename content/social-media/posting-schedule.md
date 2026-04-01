# Hoop Social Media Posting Schedule

**Campaign:** 6-Month Product Update (April 2026)
**Goal:** Drive awareness of new features, encourage self-hosted upgrades, position Hoop as actively shipping

---

## Week 1: April 7-11 — Launch Week

### Monday, April 7 — Newsletter Drop + Overview
**Platform:** Twitter/X, LinkedIn
**Type:** Announcement thread

**Twitter/X Thread:**
> We've been shipping, not talking. That changes today.
>
> Here's 6 months of Hoop product updates in one thread. 🧵
>
> 1/ RDP Session Recording — You can now record and play back full RDP sessions. Timeline scrubbing, fullscreen, the works. Compliance teams, this one's for you.
>
> 2/ AI Session Analyzer — Configure rules, and Hoop automatically analyzes sessions to flag issues. Stop manually reviewing logs.
>
> 3/ ABAC (Attribute-Based Access Control) — Fine-grained access policies based on user attributes, resource properties, and conditions. Beyond RBAC.
>
> 4/ Audit Logs — A dedicated audit logs page with full HTTP middleware. Who did what, when, from where. Compliance just got easier.
>
> 5/ Streaming for large sessions — Giant SQL results no longer crash your browser. Virtualized scrolling + download for sessions over 4MB.
>
> 6/ And a LOT more: mandatory metadata fields, Claude Code integration, Runbooks v2, HTTP proxy as a first-class connection, data masking improvements...
>
> Full newsletter with every detail → [link]
>
> Self-hosted? Upgrade to v1.54.1 to get all of this.

**LinkedIn Post:**
> We shipped a lot over the past 6 months and said nothing about it. Here's the recap:
>
> - RDP session recording and playback
> - AI-powered session analysis with configurable rules
> - Attribute-Based Access Control (ABAC)
> - Dedicated audit logs with HTTP middleware
> - Streaming large session results (no more browser crashes)
> - Mandatory metadata fields for session creation
> - Claude Code as a supported resource type
> - Runbooks v2 with multi-repo support and Jira integration
> - HTTP proxy promoted to first-class connection type
> - Major data masking performance improvements
>
> If you're self-hosted, we strongly recommend upgrading to v1.54.1.
>
> Full details in our product update newsletter → [link]

---

### Wednesday, April 9 — RDP Recording Deep Dive
**Platform:** Twitter/X, LinkedIn
**Type:** Feature spotlight

**Twitter/X:**
> New in Hoop: Full RDP session recording and playback.
>
> Record remote desktop sessions. Play them back with timeline controls. Go fullscreen.
>
> If your compliance team needs to audit RDP access, this is it.
>
> Available since v1.52.0 → [link to release]

**LinkedIn:**
> We shipped RDP session recording in Hoop.
>
> Every RDP session can now be recorded and played back — complete with timeline scrubbing, playback controls, and fullscreen support.
>
> For teams with compliance requirements around remote desktop access, this fills a major gap. No third-party recording tools needed.
>
> Available in Hoop v1.52.0+

---

### Friday, April 11 — AI Session Analyzer
**Platform:** Twitter/X, LinkedIn
**Type:** Feature spotlight

**Twitter/X:**
> What if your sessions could review themselves?
>
> Hoop's new AI Session Analyzer lets you define rules, and it automatically flags issues across terminal sessions, database queries, and more.
>
> Stop scrolling through logs. Let AI catch what humans miss.

**LinkedIn:**
> Manually reviewing session logs doesn't scale. That's why we built the AI Session Analyzer in Hoop.
>
> Define analysis rules per role. Hoop automatically reviews sessions and surfaces issues — dangerous commands, policy violations, anomalies.
>
> It's like having a security reviewer that never sleeps and never misses a session.

---

## Week 2: April 14-18 — Security & Access Control

### Monday, April 14 — ABAC
**Platform:** Twitter/X, LinkedIn
**Type:** Feature spotlight

**Twitter/X:**
> RBAC not cutting it anymore?
>
> Hoop now supports ABAC — Attribute-Based Access Control.
>
> Define access policies based on user attributes, resource properties, and environmental conditions. Much more granular than roles alone.

**LinkedIn:**
> We added Attribute-Based Access Control (ABAC) to Hoop.
>
> RBAC is great until it isn't. When your access control needs involve conditions like "this user can access production databases only during business hours from a corporate IP," you need ABAC.
>
> Hoop v1.53.0 supports it natively.

---

### Wednesday, April 16 — Audit Logs
**Platform:** Twitter/X, LinkedIn
**Type:** Feature spotlight

**Twitter/X:**
> "Who changed that permission last Tuesday?"
>
> Now you can actually answer that. Hoop's new Audit Logs page captures every admin action with full HTTP middleware logging.
>
> Compliance, incident response, and "who did that?" — covered.

---

### Friday, April 18 — Data Masking
**Platform:** Twitter/X
**Type:** Feature spotlight

**Twitter/X:**
> Data masking updates in Hoop:
>
> ✓ AI-powered masking now free for all users
> ✓ Column-aware pre-anonymization for PostgreSQL
> ✓ 3x faster with Envoy proxy + Gunicorn tuning
> ✓ Works on HTTP proxy, MySQL, and more
>
> Protect sensitive data without slowing down your team.

---

## Week 3: April 21-25 — DevEx & Integrations

### Monday, April 21 — Claude Code Integration
**Platform:** Twitter/X, LinkedIn
**Type:** Feature spotlight

**Twitter/X:**
> You can now connect Claude Code through Hoop.
>
> AI coding assistants get the same access control, audit logging, and session recording as everything else in your infrastructure.
>
> Hoop as the access layer for AI tools. This is where things are going.

**LinkedIn:**
> AI coding tools are accessing your infrastructure. Are you auditing that access?
>
> Hoop now supports Claude Code as a first-class resource type. That means the same access controls, approval workflows, session recording, and audit logs that protect your databases and servers now apply to AI assistants too.

---

### Wednesday, April 23 — Runbooks v2
**Platform:** Twitter/X, LinkedIn
**Type:** Feature spotlight

**Twitter/X:**
> Runbooks v2 in Hoop:
>
> - Multi-repository support
> - File uploads
> - Field ordering
> - Jira integration (required fields before execution)
> - Parallel execution mode
> - Default runbooks for new orgs
>
> Operational runbooks, but with access control baked in.

---

### Friday, April 25 — Streaming + Large Sessions
**Platform:** Twitter/X
**Type:** Feature spotlight

**Twitter/X:**
> "Why did my browser tab just die?"
>
> Because you ran SELECT * on a 500MB table through a web terminal.
>
> Hoop now streams large session results with virtualized scrolling. Sessions over 4MB get a download button.
>
> Your browser lives. Your data is still there.

---

## Week 4: April 28 - May 1 — Wrap-up & CTA

### Monday, April 28 — Infrastructure Roundup
**Platform:** Twitter/X, LinkedIn
**Type:** Roundup post

**Twitter/X:**
> Infrastructure improvements you might have missed in Hoop:
>
> → Go 1.24 + wazero Rust runtime
> → Helm chart consolidation
> → TLS termination for PostgreSQL and RDP
> → EKS integration
> → AWS RDS IAM auth for MySQL
> → Async agent packet processing
>
> Faster, more reliable, easier to deploy.

---

### Wednesday, April 30 — Self-Hosted CTA
**Platform:** Twitter/X, LinkedIn
**Type:** CTA

**Twitter/X:**
> If you're running Hoop self-hosted and haven't upgraded recently — now's the time.
>
> v1.54.1 includes 6 months of features: RDP recording, AI analysis, ABAC, audit logs, streaming sessions, and dozens of fixes.
>
> Upgrade guide → [link]

**LinkedIn:**
> If you're running a self-hosted Hoop deployment, here's what you're missing if you haven't upgraded recently:
>
> Over the last 6 months, we shipped RDP session recording, an AI session analyzer, ABAC, dedicated audit logs, streaming for large sessions, mandatory metadata fields, Claude Code integration, Runbooks v2, and much more.
>
> All of it is available in v1.54.1. We strongly recommend upgrading.
>
> Full changelog and upgrade notes → [link]

---

## Ongoing / Evergreen Content

### Monthly recycling (May-June)
Re-share top-performing posts with slight variations. Suggested cadence:
- **1x/week on Twitter/X**: Rotate between RDP recording, AI analyzer, ABAC, and Claude Code posts
- **2x/month on LinkedIn**: Feature spotlights tied to use cases (compliance, security, DevOps)

### Community Sharing
- **Hacker News**: Submit blog post "6 months of Hoop: What we shipped" as a Show HN
- **Reddit r/devops, r/sysadmin, r/netsec**: Share individual feature posts (RDP recording, ABAC, AI analysis)
- **Dev.to / Hashnode**: Cross-post blog content
- **Discord/Slack communities**: Share in relevant infrastructure/DevOps channels
