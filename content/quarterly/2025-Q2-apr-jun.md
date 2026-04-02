# Hoop Quarterly Update — Q2 2025 (April - June)

*Versions: v1.34.12 → v1.37.9*

This quarter was about data protection, architecture cleanup, and developer experience. PostgreSQL got native data redaction, we removed a major dependency (PostgREST), the terminal editor got significantly faster, and HTTP proxy became a real connection type.

---

## PostgreSQL Native Redaction with MSPresidio

Hoop can now redact sensitive data directly in PostgreSQL query results using MSPresidio. This is native, wire-protocol-level redaction — it parses the PostgreSQL wire protocol and masks PII before results reach the user.

We also introduced redaction modes: **best-effort** (mask what you can, return the rest) and **strict** (block the entire result if redaction can't be applied cleanly). Choose based on your risk tolerance.

**What this means for you:** Data masking for PostgreSQL went from "pattern matching on output text" to "structured, column-aware redaction at the protocol level." Strict mode gives compliance teams confidence that no PII leaks through, even in edge cases.

---

## PostgREST Removal — Direct Database Driver

We removed PostgREST, a major external dependency that handled database access for plugins and connections. In its place, we built a direct database driver using GORM.

This was a multi-week refactoring effort that touched user auth, organization logic, agent models, reviews, connections, access control, and more. Every area that previously called PostgREST now goes directly to the database.

**What this means for you:** One fewer component to deploy and maintain. Faster API responses (no more routing through an intermediary). Simpler troubleshooting when something goes wrong. If you're self-hosted, your deployment just got simpler.

---

## HTTP Proxy: From Workaround to Connection Type

HTTP proxy was promoted from an internal feature to a first-class connection type with proper validation, header configuration, and environment variable support. You can now create HTTP proxy connections through the same UI as databases and SSH.

**What this means for you:** Internal web tools, REST APIs, and HTTP-based services can be accessed through Hoop with the same audit trail and access controls as your databases. No more workarounds.

---

## AG Grid: Better Data Display

We replaced DataGridXL2 with AG Grid for displaying SQL query results. AG Grid handles large result sets better, supports proper pagination, and deals correctly with edge cases (like tab characters in cell values that were breaking the old grid).

**What this means for you:** Query results are displayed more reliably, especially for large datasets. Pagination works properly. No more broken rendering from unexpected characters in data.

---

## Database Schema Visualization

Hoop now parses the native database wire protocol and displays a visual schema browser alongside the terminal. You can browse tables, columns, and data types without running `\dt` or `SHOW TABLES`.

**What this means for you:** Context while writing queries. See your schema without switching windows or running info commands. Especially useful for developers who don't have the schema memorized.

---

## Terminal Editor Performance

The CodeMirror editor got significant performance work:
- Optimized extensions and onChange handlers
- Debounced autocomplete with caching
- Improved auto-save based on typing intensity (saves when you pause, not on every keystroke)
- Schema processing moved from Web Workers to synchronous (turned out to be faster for our use case)
- Lazy loading for large schemas

**What this means for you:** The terminal editor is noticeably more responsive, especially when connected to databases with large schemas. Autocomplete suggestions appear faster. Auto-save doesn't interfere with typing.

---

## Runbooks Improvements

- **Jira integration** — Jira support in runbooks, linking executions to tickets
- **Runbook hooks** — Execute actions when sessions open or close
- **Connection and configuration tabs** — Separate views for runbook config vs. connection assignment
- **Default values** — Propagate API defaults to runbook form fields

**What this means for you:** Runbooks are more tightly integrated with your ticketing workflow. Hooks enable automation on session lifecycle events (e.g., notify a channel when a runbook starts, clean up when it finishes).

---

## User Groups Management

Full user groups management — create, update, delete groups and assign users to them. Groups are the building block for access control policies, review requirements, and feature restrictions.

**What this means for you:** Admin teams can manage group membership directly in the Hoop UI instead of relying solely on IDP groups or manual configuration.

---

## MySQL Database Schema API

MySQL connections now have schema browsing — same database schema panel as PostgreSQL, with table and column listing.

**What this means for you:** MySQL users get the same schema context while writing queries as PostgreSQL users.

---

## MongoDB Wrapper CLI

Added `mongosh` as an optional wrapper for the MongoDB CLI, plus the `mongosh` command for exec CLI and Web Console. SSH key handling for MongoDB connections was also fixed.

**What this means for you:** MongoDB connections work with the modern `mongosh` shell, not just the legacy `mongo` CLI.

---

## Billing: Profitwell → Paddle

Migrated billing integration from Profitwell to Paddle.

**What this means for you:** If you're on a paid plan, billing infrastructure is more reliable. No action needed on your part.

---

## Other Improvements

- GCP integration — Helm chart customization for GCP ingress, gRPC healthcheck
- Jira template enhancements with connection tags mapping
- AWS Connect: increased timeouts, improved error handling
- Go updated to 1.23.8, npm audit fixes
- Removed unused auth0-lock package
- Authentication flow improvements with better redirect handling
- Improved logout experience

---

**Upgrade to v1.37.9** to get everything in this quarter.
