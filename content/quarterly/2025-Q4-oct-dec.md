# Hoop Quarterly Update — Q4 2025 (October - December)

*Versions: v1.42.3 → v1.46.13*

This quarter was about foundations. We rebuilt how connections are organized, overhauled runbooks, expanded database and protocol support, and laid the groundwork for everything that came in Q1 2026.

---

## Resources & Roles: A New Way to Organize Your Infrastructure

Connections used to be flat — one connection, one configuration. If a production PostgreSQL server needed three access levels (read-only for analysts, read-write for devs, admin for DBAs), you created three separate connections and managed them independently.

Now, connections are organized as **Resources** containing **Roles**. "PostgreSQL Production" is one Resource with roles like `readonly`, `readwrite`, `admin`. Each role has its own access controls, but they share the resource configuration.

A setup wizard walks admins through resource creation, role configuration, and access assignment. The UI includes a resource catalog, advanced filtering with search and tags, and the Command Palette works with the new model.

**What this means for you:** Fewer connections to manage, clearer access structure that mirrors how your team thinks about infrastructure, and faster onboarding for new databases and services.

---

## Runbooks v2: Multi-Repository and Connection-Based Access

Runbooks were rebuilt from the ground up with multi-repository support. Pull runbooks from multiple Git repos, select specific branches, and control which runbooks are available for which connections through rules.

Access control is built in — runbooks are filtered based on rules, connections, and user groups, so users only see what they're authorized to run.

Existing runbook configurations are automatically migrated to the v2 format — no manual work required.

**What this means for you:** Operations teams can organize runbooks by team or service across multiple repos. Access control ensures people only see runbooks relevant to their role. The migration is seamless.

---

## AWS RDS IAM Authentication for MySQL

You can now authenticate to MySQL databases using IAM roles instead of static passwords. Configure the connection with IAM credentials, and Hoop generates authentication tokens automatically.

**What this means for you:** No more rotating database passwords manually. IAM authentication integrates with your existing AWS identity management and meets security requirements for credential rotation.

---

## Data Masking with Guard Rails

Added guard rails validation through MSPresidio — if a query or command would expose sensitive data, it can be blocked or masked before the results reach the user. Guard rail violations are logged for auditing.

Also fixed goroutine and memory leaks that were accumulating over time in long-running deployments, and added MSPresidio support for MySQL.

**What this means for you:** Sensitive data protection gets enforcement (not just masking) and now covers MySQL. Stability improvements mean fewer restarts needed for long-running gateways.

---

## SSH & RDP Improvements

- **SSH native client access** — use your local SSH tools through Hoop with full audit and access control
- **RDP custom types and credential enhancements** — more flexibility in how RDP connections are configured
- **TLS termination for PostgreSQL and RDP** — Hoop can terminate TLS connections, simplifying network configuration

**What this means for you:** More protocols work natively through Hoop without requiring users to change their workflows or tools.

---

## Kubernetes & AWS SSM

- **Kubernetes bearer token support** — connect to clusters using bearer tokens in addition to existing auth methods
- **AWS SSM native client support** — access EC2 instances through SSM with full Hoop audit and access control
- **EKS integration** — streamlined setup for Amazon EKS clusters

**What this means for you:** Cloud-native infrastructure (EKS, SSM) works natively with Hoop, reducing the number of separate access tools your team needs.

---

## Database Improvements

- **MSSQL:** Web terminal now works correctly, database listing for navigating schemas
- **OracleDB:** Connection testing during setup (find config problems before users hit them), query reliability fixes
- **PostgreSQL:** TLS and proxy stability improvements
- **MongoDB:** Data loss prevention enhancements

---

## Quality of Life

- **Session search by IDs** — find specific sessions quickly
- **Groups claim configuration** in authentication forms — easier SAML/OIDC setup
- **Multiple simultaneous native client sessions** — users can have multiple native connections open at once with draggable session cards
- **Default runbooks for new organizations** — onboarding starts with useful starter runbooks instead of an empty page
- **Helm chart improvements** — consolidated configuration, added RDP and SSH port support

---

## Stability

- Fixed goroutine and memory leaks in long-running deployments
- Asynchronous agent packet processing for better throughput under load
- Migration fixes for databases with legacy views
- Improved base64 decoding for session output

---

**Upgrade to v1.46.13** to get everything in this quarter.
