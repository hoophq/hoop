<h4 align="center">
<sub><b>NEW</b>  ·  Admin MCP server — AI agents can now manage hoop, audited like humans.  <a href="#whats-new"><b>Read more →</b></a></sub>
</h4>
<h1 align="center">
One Gateway Between Your Team and Your Infrastructure.
</h1>
<h3 align="center">
hoop.dev is a layer 7 gateway that masks sensitive data, blocks dangerous commands, approves risky writes, and records every session inline, before anything reaches your infrastructure.
</h3>
<p align="center">
Engineers · AI Agents · MCP Clients · Services · Support/QA
</p>
<p align="center">
Open-source. Used by NYSE-listed companies. 5,000+ databases protected.
</p>
<p align="center">
<a href="https://github.com/hoophq/hoop/releases"><img src="https://img.shields.io/github/v/release/hoophq/hoop?style=flat-square" alt="release"></a>
<a href="https://github.com/hoophq/hoop/blob/main/LICENSE"><img src="https://img.shields.io/github/license/hoophq/hoop?style=flat-square" alt="license"></a>
<a href="https://hub.docker.com/r/hoophq/hoop"><img src="https://img.shields.io/docker/pulls/hoophq/hoop?style=flat-square" alt="docker pulls"></a>
<a href="https://github.com/hoophq/hoop/stargazers"><img src="https://img.shields.io/github/stars/hoophq/hoop?style=flat-square" alt="stars"></a>
</p>
<p align="center">
<a href="#quick-start">Quick start</a> ·
<a href="#how-it-works">How it works</a> ·
<a href="https://hoop.dev/docs/quickstart/overview">Connectors</a> ·
<a href="#vs-alternatives">vs alternatives</a> ·
<a href="#whats-new">What's new</a> ·
<a href="https://hoop.dev/docs">Docs</a>
</p>

---
 
## What is hoop?
 
hoop is an open-source layer 7 gateway that sits between users (engineers, AI agents, service accounts)
and infrastructure (databases, Kubernetes clusters, servers, APIs). Every query and command
passes through at the wire protocol level, where the gateway can:
 
- **Mask sensitive data in responses** — ML-powered classification, not regex pattern matching, applied before bytes leave the gateway.
- **Block dangerous commands before they execute** — `DROP TABLE`, `rm -rf`, `DELETE` without `WHERE`, configurable per role and per backend.
- **Require human approval for risky operations** — Slack or Teams workflow, time-bound, fully logged.
- **Record every session** — full replay of SQL, shell, kubectl, and HTTP traffic, indexed by user, table, and query.
No agents on endpoints. No schema discovery. No code changes. Deploy the gateway, connect your identity provider, define your rules.
 
---
 
## Who is hoop for?
 
Teams where engineers or AI agents access production infrastructure. If your developers run queries against databases with customer PII, execute commands on production Kubernetes clusters, or use AI coding assistants against real systems, hoop gives you visibility and control over what happens inside those sessions and what data is allowed to leave them.
 
---
 
## The problem, concretely
 
An engineer pulls recent payments to investigate a customer report:
 
**❌ Without hoop**
 
```sql
SELECT * FROM payments LIMIT 10;
```
 
```
 id    | customer_email          | card_number          | amount | status
-------+-------------------------+----------------------+--------+----------
 84021 | jane.thompson@gmail.com | 4532-1024-5678-9012  |  49.99 | settled
 84022 | mreyes@acmecorp.io      | 5412-7510-3344-1182  | 120.00 | settled
 84023 | k.patel@protonmail.com  | 4716-9923-1144-5577  |  24.99 | refunded
 84024 | dlin@northwind.co       | 5577-3344-9911-2266  |  89.50 | settled
 84025 | tyler.s@gmail.com       | 4111-2222-3333-4444  |  15.00 | failed
 ...
```
 
10 rows of real card numbers and emails. Now in `psql` history, in the screenshot the engineer pasted into Slack, and in the CSV they exported to debug locally.
 
**✅ With hoop**
 
```sql
SELECT * FROM payments LIMIT 10;
```
 
```
 id    | customer_email        | card_number         | amount | status
-------+-----------------------+---------------------+--------+----------
 84021 | j****@*****.com       | **-**-****-9012     |  49.99 | settled
 84022 | m****@*******.io      | **-**-****-1182     | 120.00 | settled
 84023 | k****@*********.com   | **-**-****-5577     |  24.99 | refunded
 84024 | d****@*********.co    | **-**-****-2266     |  89.50 | settled
 84025 | t****@*****.com       | **-**-****-4444     |  15.00 | failed
 ...
```
 
Engineers can still debug using amounts, statuses, and timestamps. PII never leaves the gateway.
 
---
 
An AI agent fixing a bug at 3AM:
 
**❌ Without hoop**
 
<pre>
> claude-code: DROP TABLE orders;
Query OK
47,291,834 rows affected 💀
</pre>
 
**✅ With hoop**
 
<pre>
> claude-code: DROP TABLE orders;
⛔ Blocked by guardrail: "Prevent destructive DDL in production"
Event logged. Security team notified.
</pre>
 
The command never reached the database.
 
---
 
## Quick Start
 
```bash
# create a jwt secret for auth
echo "JWT_SECRET_KEY=$(openssl rand -hex 32)" >> .env
 
# download and run
curl -sL https://hoop.dev/docker-compose.yml > docker-compose.yml && \
  docker compose up
```
 
Gateway running on `:8009`. OIDC connected. Masking and guardrails active.
 
[Full installation options →](https://hoop.dev/docs/introduction/getting-started)
 
---
 
## How It Works
 
```
Engineers / AI Agents / Service Accounts
              │
              ▼
     ┌────────────────┐
     │   hoop Gateway │  ← Parses wire protocols in real time
     │                │
     │  • Masks PII   │  (ML-powered, <5ms latency)
     │  • Blocks cmds │  (DROP, DELETE, rm -rf)
     │  • Approvals   │  (Slack / Teams)
     │  • Records all │  (full session replay)
     │  • AI controls │  (per-action governance)
     └────────────────┘
              │
              ▼
    Your Infrastructure
    (Databases, K8s, SSH, APIs, MCP servers)
```
 
The gateway parses wire protocols natively: PostgreSQL, MySQL, MSSQL, MongoDB, Kubernetes, SSH, HTTP/gRPC, RDP, and more. Your tools connect through the gateway without knowing it's there. No SDKs, no plugins, no browser extensions.
 
---
 
## Key Capabilities
 
### Inline controls
 
What hoop does in real-time on every connection — for engineers, AI agents, and service accounts equally.
 
**Data masking**
 
- ML-powered detection of PII, PHI, PCI data, and credentials inside database responses, API payloads, and terminal output. Not regex. The model understands context: `555-1234` in a `phone` column is a phone number, `BUILD-555-1234` in a CI log is a build ID. One rule covers thousands of resources. No schema mapping required.
**Guardrails**
 
- Define dangerous operations and block them at the protocol layer before they reach the target system. `DROP TABLE`, `DELETE` without `WHERE`, `kubectl delete namespace`, `rm -rf`, and any custom pattern. Prevention, not detection.
**Command approval**
 
- Route risky operations (production writes, schema changes, config mutations) for human approval via Slack or Teams. One command, one decision. The operation waits until approved, denied, or scheduled for a maintenance window.
**SSO**
 
- Connect Okta, JumpCloud, Azure AD, Google Workspace, or any OIDC/SAML provider. Included in the open-source license with no separate tier or seat charge. Identity is a security primitive, not a revenue lever.
### Built for AI agents
 
Same policy engine, agent-aware semantics. No parallel stack, no sandbox.
 
**AI agent governance**
 
- Claude Code, Cursor, and autonomous agents connect to your infrastructure through the gateway. Agents read freely (with masked responses). Agents write with approval. Destructive operations are blocked outright. Every agent action is logged, risk-scored, and replayable.
**MCP gateway**
 
- Not just a proxy. hoop inspects MCP payloads, masks PII in JSON responses before they reach the agent, blocks dangerous operations, and federates identity so developers never touch real credentials. Auto-generates a sensitive data catalog from MCP traffic.
### Audit & operations
 
What you stop building yourself once hoop is in place.
 
**Session recording**
 
- Full session capture with replay. Every command, every response, every approval and denial. Generates compliance evidence for SOC 2, GDPR, PCI DSS, and HIPAA automatically.
**Runbooks**
 
- Parameterized templates stored in Git. Your team executes common operations with validated inputs. Guardrails, masking, and approval workflows apply automatically to every run.
---
 
## vs Alternatives
 
hoop gets compared to three different categories of tools. Here's where it overlaps and where it doesn't.
 
### vs PAM (Privileged Access Management)
 
PAM tools route the connection, broker credentials, and log the session. hoop does that too — and then parses the wire protocol on top. Once a user is connected, PAM is done; hoop is just starting. We mask sensitive fields in database responses, block destructive commands by content (`DROP TABLE`, `rm -rf`), and require approval on risky writes — all inline, before the action reaches the target system.
 
If your concern is *who connected*, PAM is enough. If your concern is *what data left the session and what commands ran*, you need both — or you need hoop.
 
### vs DLP (Data Loss Prevention)
 
DLP inspects data in motion at the network or endpoint layer — usually after a developer has already pulled it onto their laptop, into a Slack message, or into an email. hoop inspects data in motion at the wire-protocol layer — before it reaches the developer at all. Sensitive fields never leave the gateway in the first place.
 
DLP catches leaks. hoop prevents them.
 
### vs AI Security (LLM guardrails, prompt firewalls)
 
AI security tools sit in front of the LLM. They inspect prompts going in and outputs coming out, looking for jailbreaks, prompt injection, and policy violations at the application layer. hoop sits in front of the infrastructure. We inspect what data the agent is allowed to read, what commands it's allowed to run, and what gets returned — at the database, Kubernetes, and MCP layers.
 
Different problem. Different layer. Most regulated AI deployments end up with both — application-layer controls on the prompt, infrastructure-layer controls on the data.
 
---
 
## Installation
 
### Docker (Recommended)
 
```bash
touch .env && \
curl -sL https://hoop.dev/docker-compose.yml > docker-compose.yml && \
docker compose up
```
 
[See Docker Compose documentation →](https://hoop.dev/docs/setup/deployment/docker-compose)
 
[See Kubernetes deployment documentation →](https://hoop.dev/docs/setup/deployment/kubernetes)
 
[See AWS deploy & host documentation →](https://hoop.dev/docs/setup/deployment/AWS)
 
---
 
## Supported Protocols
 
| Category | Protocols |
| --- | --- |
| Databases | PostgreSQL, MySQL, MSSQL, MongoDB |
| Infrastructure | Kubernetes (exec, port-forward), SSH, RDP |
| APIs | HTTP, gRPC |
| AI | Claude Code, Cursor, MCP servers |
| Runtimes | Rails, Django, Elixir IEx, PHP |
| Cloud | AWS SSM, custom CLIs |
 
---
 
## What's New
 
### May 14, 2026 — Admin MCP server
 
AI agents can now manage hoop itself. Connect Claude Code, Cursor, or any MCP-compatible client and the agent can configure connections, guardrails, data masking, and reviews — the same surface a human admin uses.
 
Every agent action flows through the same auth, audit log, and policy enforcement as a human admin. Approvals route to Slack or Teams. Destructive operations are blocked at the protocol layer. Sessions are recorded and replayable.
 
hoop now governs the agents that govern hoop.
 
[Read the full breakdown →](https://hoop.dev/blog/ai-agents-dont-get-a-governance-exemption)
 
---
 
## Contributing
 
We welcome contributions. Protocol parsers, masking patterns, guardrail rules, runbook templates, integrations, and documentation improvements. Check out our [Development Documentation](https://hoop.dev/docs) to get started.
 
---
 
## Community
 
Join our [Discussions](https://github.com/hoophq/hoop/discussions) to ask questions, share ideas, and connect with other users.

---

## Star the Repository

If hoop solves a problem for you, give us a star. It helps other teams find the project and tells us what to invest in next.

<p >
<a href="https://github.com/hoophq/hoop"><img src="https://img.shields.io/github/stars/hoophq/hoop?style=social" alt="Star hoop on GitHub"></a>
</p>
 
---
 
## License
 
MIT. The code that touches your data is code you can read.
 
---
 
<p align="center">
<a href="https://hoop.dev">hoop.dev</a> · Data security in transit. One gateway, every protocol.
</p>
