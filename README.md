<p align="center">
  <a href="https://hoop.dev">
    <img src="https://hoopartifacts.s3.amazonaws.com/hoop-logo.png" alt="hoop.dev" width="200" />
  </a>
</p>

<h3 align="center">The gateway between your team and your infrastructure.</h3>

<p align="center">
  Hoop parses wire protocols in real time. It masks sensitive data before it reaches the client, blocks dangerous commands before they execute, and records every session. One gateway covers databases, Kubernetes, SSH, AI agents, and MCP servers.
</p>

<p align="center">
  <a href="https://hoop.dev">Website</a> · <a href="https://hoop.dev/docs">Docs</a> · <a href="https://github.com/hoophq/hoop/discussions">Discussions</a> · <a href="https://hoop.dev/open-source">Open Source</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT License" />
  <img src="https://img.shields.io/badge/CNCF-Member-blue" alt="CNCF Member" />
  <img src="https://img.shields.io/badge/SOC_2-Type_II-green" alt="SOC 2 Type II" />
</p>

---

## What is Hoop?

Hoop is an open-source gateway that sits between users (engineers, AI agents, service accounts) and infrastructure (databases, Kubernetes clusters, servers, APIs). Every query and command passes through the gateway at the wire protocol level, where you can:

- **Mask sensitive data** in responses before it reaches the client (ML-powered, not regex)
- **Block dangerous commands** before they execute (`DROP TABLE`, `rm -rf`, `DELETE` without `WHERE`)
- **Require human approval** for risky operations via Slack or Teams
- **Record every session** with full replay for compliance and incident review
- **Govern AI agent access** to production infrastructure with the same controls

No agents on endpoints. No schema discovery. No code changes. Deploy the gateway, connect your identity provider, define your rules.

## Who is this for?

Teams where engineers or AI agents access production infrastructure that contains sensitive data. If your developers run queries against databases with customer PII, execute commands on production Kubernetes clusters, or use Claude Code / Cursor against real systems, Hoop gives you visibility and control over what happens inside those sessions.

Used by NYSE-listed companies in production. 5,000+ databases protected through a single deployment.

## The problem, concretely

### Without Hoop

Debugging a production issue...

```
SELECT * FROM users WHERE id = 42;

| id | name          | email              | ssn         | card_number      |
|----|---------------|--------------------|-------------|------------------|
| 42 | Jane Thompson | jane@example.com   | 123-45-6789 | 4532-XXXX-XXXX   |
```

You screenshot the result for Slack. SSNs, emails, and card numbers are now in your team chat.

### With Hoop

Same query through Hoop:

```
SELECT * FROM users WHERE id = 42;

| id | name | email          | ssn         | card_number      |
|----|------|----------------|-------------|------------------|
| 42 | J*** | j***@*****.com | ***-**-6789 | ****-****-****   |
```

Safe to share. No configuration required. The ML model detected the sensitive fields automatically.

### Without Hoop

AI agent fixing a bug at 3AM:

```
> claude-code: DROP TABLE orders;
> 
> Query OK, 47,291,834 rows affected 💀
```

### With Hoop

Same agent, same intent, through the gateway:

```
> claude-code: DROP TABLE orders;
> 
> ⛔ Blocked by guardrail: "Prevent destructive DDL in production"
> Event logged. Security team notified.
```

The command never reached the database.

## Quick Start

```bash
# create a jwt secret for auth
echo "JWT_SECRET_KEY=$(openssl rand -hex 32)" >> .env

# download and run
curl -sL https://hoop.dev/docker-compose.yml > docker-compose.yml && \
  docker compose up
```

Gateway running on `:8009`. OIDC connected. Masking and guardrails active.

[Full installation options →](#installation)

## How It Works

```
Engineers / AI Agents / Service Accounts
              │
              ▼
     ┌────────────────┐
     │   Hoop Gateway │  ← Parses wire protocols in real time
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

## Key Capabilities

### Data Masking

ML-powered detection of PII, PHI, PCI data, and credentials inside database responses, API payloads, and terminal output. Not regex. The model understands context: `555-1234` in a `phone` column is a phone number, `BUILD-555-1234` in a CI log is a build ID. One rule covers thousands of resources. No schema mapping required.

### Guardrails

Define dangerous operations and block them at the protocol layer before they reach the target system. `DROP TABLE`, `DELETE` without `WHERE`, `kubectl delete namespace`, `rm -rf`, and any custom pattern. Prevention, not detection.

### Command Approval

Route risky operations (production writes, schema changes, config mutations) for human approval via Slack or Teams. One command, one decision. The operation waits until approved, denied, or scheduled for a maintenance window.

### AI Agent Governance

Claude Code, Cursor, and autonomous agents connect to your infrastructure through the gateway. Agents read freely (with masked responses). Agents write with approval. Destructive operations are blocked outright. Every agent action is logged, risk-scored, and replayable.

### MCP Gateway

Not just a proxy. Hoop inspects MCP payloads, masks PII in JSON responses before they reach the agent, blocks dangerous operations, and federates identity so developers never touch real credentials. Auto-generates a sensitive data catalog from MCP traffic.

### Session Recording

Full session capture with replay. Every command, every response, every approval and denial. Generates compliance evidence for SOC 2, GDPR, PCI DSS, and HIPAA automatically.

### Runbooks

Parameterized templates stored in Git. Your team executes common operations with validated inputs. Guardrails, masking, and approval workflows apply automatically to every run.

## No SSO Tax

SSO is included in the open-source license. Connect Okta, JumpCloud, Azure AD, Google Workspace, or any OIDC/SAML provider. For free. Identity is a security primitive, not a revenue lever.

## Installation

### Docker (Recommended)

```bash
echo "JWT_SECRET_KEY=$(openssl rand -hex 32)" >> .env
curl -sL https://hoop.dev/docker-compose.yml > docker-compose.yml && \
  docker compose up
```

[See Docker Compose documentation →](https://hoop.dev/docs)

### Kubernetes

[See Kubernetes deployment documentation →](https://hoop.dev/docs)

### AWS

[See AWS deploy & host documentation →](https://hoop.dev/docs)

| Region | Launch Stack |
|--------|-------------|
| N. Virginia (us-east-1) | [Launch](https://hoop.dev/docs) |
| Ohio (us-east-2) | [Launch](https://hoop.dev/docs) |
| N. California (us-west-1) | [Launch](https://hoop.dev/docs) |
| Oregon (us-west-2) | [Launch](https://hoop.dev/docs) |
| Ireland (eu-west-1) | [Launch](https://hoop.dev/docs) |
| London (eu-west-2) | [Launch](https://hoop.dev/docs) |
| Frankfurt (eu-central-1) | [Launch](https://hoop.dev/docs) |
| Sydney (ap-southeast-2) | [Launch](https://hoop.dev/docs) |

[View all regions →](https://hoop.dev/docs)

## Architecture: Open Source vs. Commercial

The gateway (everything on the data path) is open source under MIT. The commercial layer adds AI capabilities, the web UI, and enterprise integrations.

**Open source (MIT)**
- Wire protocol parsing (PostgreSQL, MySQL, MongoDB, SSH, Kubernetes, HTTP)
- Connection routing, session management, TLS termination
- SSO/IdP integration (OIDC, SAML)
- Plugin system (masking, guardrails, runbooks, webhooks)
- CLI (`hoop connect`, `hoop exec`, `hoop admin`)
- Session recording and audit logging

**Commercial (built on the open source core)**
- AI-powered masking (ML models for context-aware PII detection)
- AI session analysis (LLM-based risk scoring, anomaly detection)
- Web UI (developer portal, admin console, session browser)
- IdP group sync (OAuth 2.0)
- Managed hosting
- Enterprise support and SLA

## Supported Protocols

| Category | Protocols |
|----------|-----------|
| Databases | PostgreSQL, MySQL, MSSQL, MongoDB |
| Infrastructure | Kubernetes (exec, port-forward), SSH, RDP |
| APIs | HTTP, gRPC |
| AI | Claude Code, Cursor, MCP servers |
| Runtimes | Rails, Django, Elixir IEx, PHP |
| Cloud | AWS SSM, custom CLIs |

## Guides

**Databases:** [PostgreSQL](https://hoop.dev/docs) · [MySQL](https://hoop.dev/docs) · [MongoDB](https://hoop.dev/docs) · [MSSQL](https://hoop.dev/docs)

**Infrastructure:** [Kubernetes](https://hoop.dev/docs) · [SSH](https://hoop.dev/docs) · [AWS](https://hoop.dev/docs)

**AI Agents:** [Claude Code](https://hoop.dev/docs) · [Cursor](https://hoop.dev/docs) · [MCP Gateway](https://hoop.dev/docs)

[View all guides →](https://hoop.dev/docs)

## Contributing

We welcome contributions. Protocol parsers, masking patterns, guardrail rules, runbook templates, integrations, and documentation improvements. Check out our [Development Documentation](https://hoop.dev/docs) to get started.

## Community

Join our [Discussions](https://github.com/hoophq/hoop/discussions) to ask questions, share ideas, and connect with other users.

## License

MIT. The code that touches your data is code you can read.

---

<p align="center">
  <a href="https://hoop.dev">hoop.dev</a> · Data security in transit. One gateway, every protocol.
</p>
