# Hoop Quarterly Update — Q1 2025 (January - March)

*Versions: v1.31.32 → v1.34.11*

This quarter focused on expanding protocol coverage, building AWS infrastructure automation, and improving the onboarding experience. MongoDB became fully supported, AWS Connect automated database provisioning, and we overhauled how new users get started.

---

## MongoDB Support

MongoDB went from partial to fully supported. We added standard authentication handshake (for environments that don't support speculative auth), SASL authentication, increased gRPC packet sizes for larger MongoDB payloads, and improved connection string handling.

**What this means for you:** MongoDB connections through Hoop now work reliably across different authentication configurations, including cloud-hosted and self-managed deployments. If MongoDB connections were flaky before, this quarter fixed it.

---

## AWS Connect: Automated Database Provisioning

We built a complete workflow for automatically discovering and provisioning access to AWS databases:

- **RDS instance discovery** — Hoop scans your AWS account and lists available database instances
- **Automatic connection creation** — Discovered databases can be turned into Hoop connections with one click
- **Database role provisioning** — Automatically creates database roles (PostgreSQL, MySQL, SQL Server) with appropriate permissions
- **Aurora support** — Works with AWS Aurora clusters, not just standalone RDS
- **Vault integration** — Credentials can be stored in HashiCorp Vault as part of the provisioning flow
- **Cross-account support** — Works across multiple AWS accounts with proper role assumption
- **Security group management** — Automatically configures network access between the Hoop agent and the database

The provisioning is async — it runs as a job with persistent tracking, so you can kick it off and check back later.

**What this means for you:** Setting up Hoop for a new AWS database goes from "manually create connection, configure credentials, set up network access" to "point at your AWS account, select the databases, done." For teams managing dozens of RDS instances, this saves hours of setup per environment.

---

## SSH Proxy with Password Authentication

Native SSH proxy support with password authentication. Hoop can now terminate SSH connections, authenticate with password, and proxy the session through to the target host.

**What this means for you:** SSH access through Hoop is no longer limited to key-based auth. Teams using password-based SSH (common in legacy environments and some cloud providers) can now route through Hoop with full audit and access control.

---

## Onboarding Overhaul

We rebuilt the onboarding flow from scratch. New organizations get a guided setup experience with:
- Dynamic connection creation interface with form validation
- Agent assignment UI
- Streamlined checks that eliminate unnecessary polling
- Redirect to the editor (where you actually work) instead of a generic home page

**What this means for you:** New team members get productive faster. The old onboarding had friction points that made people think setup was broken when it was just slow — that's fixed.

---

## Connection Tags

Connections can now be tagged with key-value attributes. Tags propagate to sessions, so you can filter sessions by tag later. The CLI supports filtering connections by tag selector.

**What this means for you:** Organize connections by team, environment, project, or any dimension that makes sense for your organization. Filter the connection list and session history using tags. Useful for large deployments where the flat connection list gets unwieldy.

---

## Kill Sessions

Admins can now terminate running sessions from the UI. A new endpoint lets you kill sessions that are stuck, running too long, or shouldn't have been started.

**What this means for you:** No more waiting for sessions to time out or asking someone to close their terminal. Admins have a clean kill switch.

---

## Review & Approval Improvements

- Session review filtering by user, status, and date range
- Jira integration icon on review and session details (linking reviews to tickets)
- Improved Slack notifications — fixed duplicate messages, better approval workflow when approving via web
- Review by ID page for direct links to specific reviews

**What this means for you:** Approval workflows are easier to navigate, especially for teams using Jira for change management and Slack for notifications.

---

## Security

- **Security headers** — Added CSP and other headers protecting against common web vulnerabilities
- **Secrets removed from connection listing** — The connections API no longer returns sensitive fields in list responses (breaking change, but necessary)
- **Non-admin redirect** — Regular users are properly redirected away from admin-only pages instead of seeing confusing errors

---

## CLI

- New `exec` subcommand for running commands directly from the CLI
- Session ID propagation through to protocol functions for better traceability
- Exit code handling across agent and gateway layers

---

## Other Improvements

- Dark mode for the editor (v1.33.0)
- Elixir syntax highlighting
- Keyboard shortcuts component
- Improved Slack review workflow
- Pre-computed event sizing for faster session listing
- Helm chart published to GitHub Registry
- BigQuery CLI accepts project ID as environment variable

---

**Upgrade to v1.34.11** to get everything in this quarter.
