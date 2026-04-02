# Hoop Quarterly Update — Q3 2025 (July - September)

*Versions: v1.37.10 → v1.42.2*
*42 releases*

This quarter was about protocol expansion, authentication hardening, and infrastructure modernization. We shipped SAML 2.0, native client access for PostgreSQL, SSH and Postgres server proxies on the gateway, upgraded to Ubuntu 24.04, replaced URL tokens with secure cookies, and laid the foundation for the RDP and Resource Catalog features that shipped in Q4.

---

## SAML 2.0 Integration

Hoop now supports SAML 2.0 for single sign-on. This is a complete implementation — identity provider configuration, refactored auth methods, CLI login support, and Helm chart updates.

The system uses a persistent signing key with Curve25519 elliptic curve cryptography, replacing the old JWT_SECRET_KEY approach (now deprecated). Identity provider loading was refactored to a singleton pattern with deferred loading, so auth configuration changes take effect without restarts.

We also added an authentication management UI for self-hosted deployments — admins can configure OIDC or SAML providers directly from the Settings page, with a provider selection grid showing icons for Auth0, AWS Cognito, Azure, Google, JumpCloud, and Okta.

**What this means for you:** If your organization uses SAML (common in enterprises with Azure AD, Okta, or JumpCloud), you can now use it with Hoop. Self-hosted admins no longer need to edit config files to set up SSO — it's all in the UI.

---

## Native Client Access for PostgreSQL

You can now connect to PostgreSQL databases through Hoop using your local `psql` client (or pgAdmin, DBeaver, etc.). Hoop proxies the connection with full audit logging and access controls — same security as the web terminal, but using the tools your team already knows.

The UI includes a session duration selector, connection details panel, active session tracking, and draggable session cards for managing multiple connections.

We also added a **Test Connection** feature — validate that a database connection works during setup, before any user hits a "connection refused" error. Works for PostgreSQL, MySQL, MSSQL, OracleDB, and custom connections.

**What this means for you:** Developers use whatever PostgreSQL client they prefer. DBAs can use their specialized tools. The audit trail and access controls still apply. And you catch connection problems at setup time, not when someone is trying to get work done.

---

## Postgres and SSH Server Proxies on the Gateway

Two new server proxies run directly on the gateway:

- **Postgres Server Proxy** — The gateway itself now speaks the PostgreSQL wire protocol. This enables direct database access without requiring an agent for simple proxy scenarios. Can be enabled/disabled per connection via API.

- **SSH Server Proxy** — Same concept for SSH. The gateway accepts SSH connections, manages credentials, and proxies to the target host. This enables native SSH workflows (scp, rsync, port forwarding) through Hoop.

Both features include credential management with proper lifecycle (creation, expiration, cleanup) and API endpoints for starting/stopping the proxy services.

**What this means for you:** Simpler deployment for common use cases — PostgreSQL and SSH connections can work without a separate agent. Fewer moving parts means fewer things to troubleshoot.

---

## Secure Cookie Authentication (Replacing URL Tokens)

Authentication tokens are now sent as secure cookies instead of URL parameters. This is a significant security improvement:

- **HttpOnly cookies** can't be read by JavaScript, preventing XSS token theft
- **SameSite flags** prevent CSRF attacks
- Tokens no longer appear in browser history, server logs, or referrer headers
- Auto API URL detection eliminates manual gateway URL configuration

**What this means for you:** Authentication is more secure by default. If you had concerns about token leakage (a common finding in security audits), this addresses it without any changes on your part.

---

## Ubuntu 24.04 LTS Base Images

Both Gateway and Agent Docker images were upgraded to Ubuntu 24.04 LTS (Noble Numbat) with multi-architecture support (amd64 + arm64).

Updated tool versions in the Agent image:
- Python 3.9 → 3.12
- Node 22.18 LTS
- AWS CLI 2.28.6
- kubectl 1.29.15
- PostgreSQL client 16.9
- Oracle clients 23.9
- Added `mongosh` 2.5.6 alongside legacy mongo client
- Added `redis-cli`

Removed: Clojure and JRE (no longer needed in the agent).

**What this means for you:** Security patches from the latest Ubuntu LTS. Modern tool versions. Arm64 support means better performance on ARM-based infrastructure (AWS Graviton, Apple Silicon dev environments). Smaller image without unused dependencies.

---

## AI Data Masking Configuration

Data masking rules got a dedicated management interface. Admins can create rules, assign them to connections, and configure which entity types to detect. A new connections panel within masking rules shows loading states and provides clear feedback.

Also added "ORGANIZATION" to the supported DLP info types, and fixed an issue where plain exec commands were incorrectly getting info type redaction applied.

**What this means for you:** Data masking is easier to configure and more precise about when it applies. No more redaction on commands that don't return data.

---

## Resource Catalog and New Terminal UI

The terminal got a complete redesign with a compact layout, connection dialog, and integrated Database Schema Panel. The Resource Catalog provides a browsable, searchable view of all available connections organized by type.

Also added **Cmd+K search** across resources — type to find any connection, runbook, or resource instantly.

**What this means for you:** Finding and connecting to resources is faster, especially in large environments. The schema panel gives context while writing queries. Cmd+K means you never have to scroll through a list to find what you need.

---

## RDP Proxy Foundation

The initial RDP proxy infrastructure landed this quarter — a Rust-based agent handling RDP connections with WebSocket support, plus the gateway-side proxy. This is the foundation for the browser-based RDP client (v1.47.0) and session recording (v1.52.0) that shipped in Q1 2026.

**What this means for you:** RDP connections through Hoop are now possible. The groundwork is laid for full visual session recording in the next quarter.

---

## SSO and Auth Improvements

- **SSO group sync for admins** — Admin user groups now properly sync during SSO login. Previously, admin users could have stale group memberships.
- **OIDC validation** — Proper `azp` claim verification through id_token validation per OpenID spec.
- **Self-hosted auth management UI** — Configure identity providers directly in the Hoop Settings page.
- **Provider selection grid** — Visual icons for Auth0, Cognito, Azure, Google, JumpCloud, Okta.

---

## CLI Improvements

- `hoop list` and `hoop describe` commands for connection management (list available connections with output format options)
- SSH automatic connect mode and listen port mode
- Service daemon commands (`start`, `stop`, `remove`, `logs`) for systemd (Linux) and launchctl (macOS)
- Enhanced error handling with response body details

**What this means for you:** The CLI is more capable for daily use. Daemon management is built in — no more manual systemd unit files.

---

## Runbooks Improvements

- Environment variable support in runbook execution
- Reviewers information propagated to runbook hooks
- Normalized runbook paths
- Multiple connections management with primary connection requirements

---

## Other Improvements

- Video/Logs tabs with ANSI color interpretation for terminal sessions
- OracleDB library updated to 23.9 with improved connection testing
- MSPresidio API timeout increased for heavier NLP models
- Helm chart: MSPresidio deployment option, Postgres with volume persistence
- Presidio included in docker-compose for demo environments
- CloudFormation: Presidio deployment option with 20GB disk
- Connection similarity search endpoint (fuzzy matching for connections and runbooks)
- Clipboard handling refactored for better resource management
- Brew tap fix for macOS installation

---

## Breaking Change

`POST /api/connections/{nameOrID}/exec` was removed (deprecated). Use `POST /api/sessions` instead. If you have automation calling the old endpoint, update it before upgrading past v1.39.6.

---

**Upgrade to v1.42.2** to get everything in this quarter.
