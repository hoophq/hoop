# Hoop Quarterly Update — Q2 2025 (April - June)

*Versions: v1.34.24 → v1.37.14*
*81 releases*

This quarter was about data protection, architecture cleanup, new data sources, and developer experience. PostgreSQL got native wire-protocol redaction, we removed PostgREST entirely, DynamoDB and AWS CloudWatch became connection types, the Data Masking feature got a full management UI, and the terminal editor got significantly faster.

---

## PostgreSQL Native Redaction with MSPresidio

Hoop can now redact sensitive data directly in PostgreSQL query results at the wire protocol level. Instead of pattern-matching output text, this parses the PostgreSQL extended query protocol and masks PII before results reach the user.

We also introduced **redaction modes**:
- **Best-effort** — mask what you can, return the rest
- **Strict** — block the entire result if redaction can't be applied cleanly

Includes statistics collection (how many fields were redacted, which entity types were found), structured content redaction via YAML encoder, and timeout control for large result sets.

**What this means for you:** Data masking for PostgreSQL went from "pattern matching on text output" to "structured, protocol-aware redaction." Strict mode gives compliance teams confidence that no PII leaks through, even in edge cases. Stats let you understand your data exposure.

---

## PostgREST Removal — Simpler Architecture

We removed PostgREST, a major external dependency that handled all database access for plugins, connections, users, auth, and more. Over several weeks, we migrated every subsystem to use a direct database driver (GORM):

- User authentication and organization logic
- Agent models and connection status
- Reviews and access control
- Session storage and reporting
- Plugins and service accounts
- Login, logout, and Ask AI routes
- Proxy manager

This culminated in v1.36.0 where PostgREST was fully removed from the codebase.

**What this means for you:** One fewer component to deploy and maintain. Faster API responses (no intermediary). Simpler troubleshooting. If you're self-hosted, your deployment just got meaningfully simpler — one less container to manage, monitor, and update.

---

## Data Masking: Full Feature with Management UI

Data masking graduated from a configuration flag to a full-featured system:

- **Complete management UI** — create, edit, delete masking rules from the Settings page
- **Per-connection control** — enable or disable masking on individual connections
- **Ad-hoc recognizers** — define custom entity detection using regex patterns or deny word lists
- **Entity type configuration** — choose which PII types to detect (names, emails, SSN, credit cards, plus custom "ORGANIZATION" type)
- **Score threshold** — tune the confidence level for AI-powered detection
- **Default enabled** — new connections have redaction enabled by default

The backend uses MSPresidio with support for loading recognizers from different data sources.

**What this means for you:** Data masking is now fully self-service for admins. Create rules, assign them to connections, tune the sensitivity. No more editing config files or asking engineering to set up masking. Custom recognizers mean you can mask domain-specific data (internal IDs, project codes) not just standard PII.

---

## DynamoDB Support

DynamoDB is now a first-class connection type with schema browsing and query execution. The Schema Explorer shows tables and columns, and queries can be run directly from the terminal.

**What this means for you:** Teams using DynamoDB can now access it through Hoop with the same audit trail, access controls, and data masking as relational databases.

---

## AWS CloudWatch Connection

CloudWatch is now a connection type with log group schema browsing. Browse log groups directly from the Hoop UI and query logs.

**What this means for you:** Centralized access to CloudWatch logs through Hoop. Useful for teams that want to audit who's looking at logs and ensure consistent access controls across observability tools.

---

## AG Grid: Better Data Display

Replaced DataGridXL2 with AG Grid for SQL query results. AG Grid handles large result sets better, supports pagination, and handles edge cases (like tab characters in cell values that broke the old grid).

Migration involved a couple of reverts and re-implementations (the first attempt caused rendering issues), but the end result is significantly more robust.

**What this means for you:** Query results display more reliably, especially with large datasets or data containing special characters. Pagination works properly.

---

## Database Protocol Visualization

Hoop now parses the native PostgreSQL wire protocol and displays a visual schema browser alongside the terminal. Browse tables, columns, and data types without running `\dt` or schema queries. Also added an `event_stream` option for raw query auditing at the wire protocol level.

MySQL also got schema API support this quarter.

**What this means for you:** Context while writing queries — see your schema without switching windows. Works for both PostgreSQL and MySQL. The wire protocol auditing gives security teams visibility into exactly what queries were sent.

---

## Terminal Editor Performance

The CodeMirror editor got significant performance work:
- Optimized extensions and onChange handlers
- Debounced autocomplete with caching
- Auto-save based on typing intensity (saves when you pause, not on every keystroke)
- Schema processing optimized with memoization
- Lazy loading for large schemas
- Removed Web Worker for schema processing (synchronous turned out faster for this use case)

**What this means for you:** The terminal editor is noticeably more responsive, especially with databases that have large schemas. Autocomplete is faster. Auto-save doesn't interfere with typing.

---

## Runbooks Improvements

- **Jira support in runbooks** — link runbook executions to Jira tickets
- **Runbook hooks** — execute actions when sessions open or close (e.g., notify a channel, run cleanup)
- **Connection and configuration tabs** — separate views for runbook config vs. connection assignment
- **Default values** — API defaults propagate to form fields
- **Lint CLI subcommand** — validate runbook templates from the CLI

**What this means for you:** Runbooks are tightly integrated with Jira. Hooks enable automation on session lifecycle. The lint command catches template errors before deployment.

---

## User Management

A complete Users feature with sidebar navigation, user profile views, and group management:
- Create, update, delete user groups
- Assign users to groups
- Case-insensitive sorting
- Connection filter and search in group list
- GSuite Groups sync via Cloud Identity API

**What this means for you:** Admin teams can manage users and groups directly in the Hoop UI. Group membership is the building block for access control — easier group management means easier policy management.

---

## Review System Improvements

- **Rejection after approval** — Resource owners and admins can now reject a review even after it was approved (useful for revoking access when circumstances change)
- **Connection filtering** in review lists
- Enhanced review detail layouts
- Jira icon integration on review details

---

## Other Improvements

- **SSH setup improvements** — Auth method selection (password vs. private key), new connection icons, credential management
- **Billing: Profitwell → Paddle** — More reliable billing infrastructure
- **GCP integration** — Helm chart customization for GCP ingress, gRPC healthcheck endpoint
- **AWS Connect enhancements** — Webhooks for provisioning, Vault Secrets Provider in setup, resource tagging
- **Expandable toast notifications** for better error visibility
- **mongosh compatibility** — Modern MongoDB shell works alongside legacy client
- **MongoDB wrapper CLI** as optional configuration
- **Connection permission checks** for web terminal and native client access
- **Dependency updates** — Go 1.23.8, npm audit fixes, removed unused auth0-lock
- **Helm improvements** — Deployment annotations, gRPC port defaults, service account annotations

---

## Breaking Changes

- **Jira CMDB pagination** (v1.36.20): Removed `GET objecttype-values` route, replaced with paginated Jira Assets API endpoint. Update any integrations using the old route.
- **Reviews API** (v1.35.17): Removed `/api/reviews` list endpoint and metadata fields from `GET /api/reviews/{id}`. Reviews are now accessed through sessions.

---

**Upgrade to v1.37.14** to get everything in this quarter.
