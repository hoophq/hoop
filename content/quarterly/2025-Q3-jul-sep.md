# Hoop Quarterly Update — Q3 2025 (July - September)

*Versions: v1.37.10 → v1.42.2*

This quarter was about polish and new protocols. We shipped native client access for PostgreSQL, the Resource Catalog, a new Terminal UI, secure authentication, AI Data Masking, RDP proxy (the foundation for session recording coming in Q1 2026), and lots of reliability work across the board.

---

## Native Client Access for PostgreSQL

You can now connect to PostgreSQL databases through Hoop using your local `psql` client. Hoop proxies the connection with full audit logging and access controls — same security guarantees as the web terminal, but using the tools your developers already know.

Includes connection testing during setup, so you find configuration problems before users hit them.

**What this means for you:** Developers can use `psql`, pgAdmin, DBeaver, or any PostgreSQL client of their choice. They don't have to use the Hoop web terminal for database access. The audit trail and access controls still apply.

---

## Resource Catalog and New Terminal UI

The terminal UI got a complete redesign with a new connection dialog, Database Schema Panel integration, and a compact layout that makes better use of screen space. The Resource Catalog provides a browsable view of all available connections, organized by type.

**What this means for you:** Finding and connecting to resources is faster. The schema panel gives you context while writing queries. The UI takes up less space, which matters when you're working with multiple terminals.

---

## RDP Proxy (Foundation)

We built the initial RDP proxy infrastructure — a Rust-based agent that handles RDP connections with WebSocket support, plus the gateway-side proxy. This is the foundation for RDP browser access and session recording that shipped in Q1 2026.

**What this means for you:** RDP connections through Hoop are now possible. If you enabled the feature, remote desktop access gets the same audit and access control treatment as database and SSH connections.

---

## Secure Authentication

Authentication was hardened this quarter:
- **Secure cookies** replaced URL-based tokens for auth. Cookies are more secure (HttpOnly, SameSite flags prevent XSS and CSRF attacks) and don't leak tokens in browser history or server logs
- **Auto API URL detection** — eliminates manual configuration of the gateway URL
- **OIDC validation improvements** — proper `azp` claim verification through id_token validation per OpenID spec
- **SSO group sync** — Admin user groups are now properly synced during SSO login

**What this means for you:** Authentication is more secure out of the box. Token leakage via URL parameters is eliminated. SSO setups work more reliably, especially for admin users whose group memberships weren't syncing correctly.

---

## AI Data Masking Rules

Data masking configuration got a dedicated UI for creating and managing masking rules. Admins can define which entity types to detect and mask, set confidence thresholds, and assign rules to specific connections.

**What this means for you:** Configuring data masking is now self-service for admins instead of requiring manual setup. You can tune the sensitivity (what gets masked and at what confidence level) per connection.

---

## Self-Hosted Auth Management

Added authentication management capabilities for self-hosted deployments, giving admins control over auth configuration directly in the Hoop UI.

**What this means for you:** Self-hosted customers can configure authentication providers (OIDC, SAML) without editing config files or environment variables. Reduces the setup complexity for new deployments.

---

## Presidio Deployment for CloudFormation

Presidio (the data masking engine) can now be deployed via CloudFormation template with Docker support and 20GB disk allocation. This makes it easier to run the full data masking stack alongside Hoop.

**What this means for you:** If you're on AWS with CloudFormation, deploying Presidio for data masking is a single stack deployment instead of manual Docker setup.

---

## OracleDB

OracleDB connections got significant improvements — fixed connection issues, improved the connection setup flow, and resolved query reliability problems.

**What this means for you:** OracleDB connections through Hoop are more reliable. If you had connection drops or query failures with Oracle databases, this quarter addressed them.

---

## Dependency Updates and Security

- Bumped dependencies to address Dependabot security alerts
- Go updated across modules
- Removed unused packages
- Helm chart improvements for various deployment scenarios

---

## Other Improvements

- Multiple simultaneous native client sessions with localStorage management
- Improved session stream memory handling for large outputs
- Tab view for CLI sessions
- Query parameter support for session filtering
- Enhanced review detail layout
- Dashboard reviews improvements
- Helm chart: added Postgres deployment option with volume persistence
- Plugin dispatch refactoring

---

**Upgrade to v1.42.2** to get everything in this quarter.
