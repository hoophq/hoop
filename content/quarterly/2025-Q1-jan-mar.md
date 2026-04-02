# Hoop Quarterly Update — Q1 2025 (January - March)

*Versions: v1.31.15 → v1.34.23*
*81 releases*

This quarter focused on expanding protocol coverage, building AWS infrastructure automation, deepening Jira integration, and improving the onboarding experience. MongoDB became fully supported, AWS Connect automated database provisioning end-to-end, Jira gained CMDB and workflow support, and we overhauled how new users get started.

---

## MongoDB: From Partial to Fully Supported

MongoDB went through a complete reliability overhaul this quarter:

- **Standard auth handshake** for environments that don't support speculative authentication
- **SASL authentication** with proper `$db` attribute handling
- **pymongo compatibility** — the most popular Python MongoDB driver now works cleanly
- **Increased gRPC packet sizes** for larger MongoDB payloads
- **Improved connection strings** displayed in the access flow
- Tested against MongoDB versions 5.0 through 8 with multiple client types

**What this means for you:** MongoDB connections through Hoop now work reliably regardless of your auth configuration, client library, or MongoDB version. If MongoDB connections were flaky before, this quarter fixed it.

---

## AWS Connect: Automated Database Provisioning

We built a complete workflow for automatically discovering and provisioning access to AWS databases:

- **RDS instance discovery** — Hoop scans your AWS account and lists available database instances, with region filtering
- **Automatic connection creation** — Discovered databases become Hoop connections with one click
- **Database role provisioning** — Automatically creates database roles for PostgreSQL, MySQL, and SQL Server with appropriate permissions (hoop_ro, hoop_rw, hoop_ddl naming convention), secure random passwords, and async job tracking
- **Aurora support** — Works with AWS Aurora clusters, not just standalone RDS
- **Vault integration** — Credentials can be stored in HashiCorp Vault as part of the provisioning flow
- **Cross-account support** — Works across multiple AWS accounts with proper role assumption
- **Security group management** — Automatically configures network access between the Hoop agent and the database
- **Webhooks via Svix** — Get notified when provisioning completes
- **Multi-step onboarding UI** — Account selection, instance filtering, agent assignment, and provisioning status all in one guided flow

The provisioning is async with persistent job tracking — kick it off and check back later. Passwords are redacted in logs.

**What this means for you:** Setting up Hoop for a new AWS database goes from "manually create connection, configure credentials, set up network access" to "point at your AWS account, select the databases, done." For teams managing dozens of RDS instances, this saves hours per environment.

---

## Jira Integration: CMDB, Workflows, and Templates

Jira integration went from basic to comprehensive this quarter:

- **CMDB support** — Select objects from Jira Assets (CMDB) directly in templates, including object type items with proper schema handling
- **Workflow transitions** — Trigger Jira workflow transitions when sessions or reviews change status
- **Date fields and select types** — Jira prompt fields now support dates and dropdown selections
- **Template improvements** — Better UX for template forms, connection tags mapping to templates
- **Review-Jira linking** — Jira integration icon appears on review and session details, making it easy to trace activity back to tickets

**What this means for you:** If your team uses Jira for change management, Hoop sessions and reviews now integrate directly with your Jira workflows. CMDB support means you can reference configuration items during access requests — useful for ITIL-based teams.

---

## SSH Proxy with Password Authentication

Native SSH proxy support with password auth, private key auth, and auth method selection. Hoop can now terminate SSH connections, authenticate with the target host, and proxy the session through with full audit.

Includes environment variable validation and configurable SSH port (default 2222).

**What this means for you:** SSH access through Hoop supports both key-based and password-based auth. Teams with legacy environments or cloud providers that use password auth can now route through Hoop.

---

## HTTP Proxy Type

HTTP proxy was introduced as a new connection type with validation (blocking exec for SSH/HTTP proxy/TCP), and support for configuring HTTP headers on connections.

**What this means for you:** Internal web tools, REST APIs, and HTTP-based services can now be managed through Hoop. This is the foundation for the full HTTP proxy features (guardrails, token management) that ship in later quarters.

---

## Onboarding Overhaul

We rebuilt the onboarding flow from scratch:
- Dynamic connection creation interface with form validation
- Agent assignment and polling
- Streamlined checks that eliminate unnecessary waiting
- Redirect to the editor (where you actually work) instead of a generic home page
- Improved state management across the Connect workflow

**What this means for you:** New team members get productive faster. The old onboarding had friction points that made people think setup was broken when it was just slow — that's fixed.

---

## Connection Tags

Connections can now be tagged with key-value attributes. Tags propagate to sessions and are filterable everywhere — the connection list, session history, and CLI all support tag selectors.

Default system tags are created automatically. The CLI supports `-q`/`--query` parameters and tag-based filtering.

**What this means for you:** Organize connections by team, environment, project, or any custom dimension. Filter everything using tags. Essential for large deployments.

---

## Kill Sessions

Admins can now terminate running sessions from the UI and via API. Works for sessions that are stuck, running too long, or shouldn't have been started.

**What this means for you:** No more waiting for sessions to time out. Admins have a clean kill switch.

---

## Session & Review Improvements

- **Review filtering** by user, status, date range, and connection — with pagination
- **Session tab view** — sessions are browsable in the CLI with the tabview flag
- **Session metadata** — URL links in metadata, mutatable session metadata attributes
- **Regular users can view all sessions** (output restricted based on access control)
- **Review flow integrated into session details** with approval system
- Fixed reviewed session logic to deny execution for non-owners

---

## Security

- **Security headers** — CSP and other headers protecting against common web vulnerabilities
- **Secrets removed from connection listing** — The connections API no longer returns sensitive fields in list responses. **Breaking change**, but necessary — secrets were being leaked on list endpoints.
- **Non-admin redirect middleware** — Regular users are properly redirected away from admin pages
- **IDP email normalization** — User emails from identity providers are normalized to lowercase

---

## CLI Improvements

- New `exec` subcommand for running commands directly
- Session tabview and `-q`/`--query` parameter for filtering
- Session ID propagation through protocol functions
- Exit code handling across agent and gateway layers
- Multi-connection exec with metadata support

---

## Other Improvements

- **Dark mode** for the editor with Elixir syntax highlighting
- **Keyboard shortcuts** component
- **Pre-computed event sizing** — faster session listing with default 100-item limit
- **Helm chart** published to GitHub Registry
- **BigQuery CLI** accepts project ID as environment variable
- **Base64 session decoding** — fixed invalid JSON characters in session details
- **Event stream base64 option** for binary-safe session data
- **Improved Slack notifications** — fixed duplicates, better approval workflow
- **Review by ID page** for direct links

---

**Upgrade to v1.34.23** to get everything in this quarter.
