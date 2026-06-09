# Hoop — H1 2026 Engineering & Product Review
### Off-site presentation copy (speaker-ready)

> Scope: every PR merged to `hoophq/hoop` between Jan 1 and Jun 8, 2026 — **230 pull requests**.
> Note on labels: GitHub's API didn't expose the applied release labels, so major/minor/patch are inferred from each PR's stated change type. The narrative below doesn't depend on them.

---

## SLIDE 1 — Title

**Hoop H1 2026**
What we shipped, who it helped, and where it's taking us.

Speaker note: "230 merged PRs in five and a half months. That's roughly **1.6 changes shipped every working day** — and tonight we're going to walk through all of it."

---

## SLIDE 2 — The half-year in one number

**230 PRs merged. ~1.6 per working day.**

Six themes carried the half:
- **Access governance** got deeper, finer-grained, and auditable.
- **AI became a first-class citizen** — Hoop is now controllable *by* agents and a gateway *for* agents.
- **The protocol surface kept widening** — HTTP, RDP, SSH, Kubernetes/EKS, BigQuery, observability tools.
- **Audit & compliance** went from "we log sessions" to "we can prove who did what, on every protocol, with PII flagged."
- **Hoop Tunnel** turned native, zero-config access into a real product.
- **We rebuilt our foundation** — React frontend, release automation, integration tests, reliability.

Speaker note: "Almost everything we shipped this half rolls up into one of these six stories."

---

## SLIDE 3 — The big idea of H1

**Hoop stopped being just an access gateway for humans. This half it became the control plane for *every* identity — human, machine, and AI agent — across *every* protocol, with a full audit trail behind all of it.**

Three identity types, one governance model:
- **Humans** — smoother than ever (one-time auth, persistent credentials, native tunnel).
- **Machines** — first-class Machine Identities for CI/ETL/BI, with live audit.
- **AI agents** — MCP server + user tools, so an agent can request access, pass through approvals, and be recorded just like a person.

Speaker note: "This is the thread to pull on all night. Everything else is in service of this."

---

# PART 1 — THE GAME-CHANGERS

## SLIDE 4 — Game-changer #1: Hoop became an AI-native platform

**We shipped the MCP gateway. Hoop is now both controllable by AI agents and a secure broker for them.**

- **Admin MCP server embedded in the gateway** (#1384) — agents like Claude Code and Cursor can manage connections, guardrails, masking, groups, access rules, runbooks, reviews and sessions, with full admin parity — all behind existing auth and audit.
- **User-facing MCP tools** (#1408) — an agent acting *as a regular user* can discover the resources it's allowed to touch, run queries, pass through approval gates, and read its own session history.
- **MCP secured to enterprise standard** (#1404) — OAuth 2.1 Resource Server support, strict JWT validation, RFC 9728 protected-resource metadata, per-org config.
- **AI session analyzer** (#1306, #1474) — AI-powered risk analysis wired into the session lifecycle, with custom prompts and the ability to auto-open a review on risky sessions. OpenAI-compatible and Anthropic providers.
- **Claude Code as a governed resource** (#1277) + **`hoop claude configure`** (#1425) — point Claude Code at Hoop in one command; credentials and headers written straight to `~/.claude/settings.json`.
- **CLI built for agents** (#1376) — `exec --output json` with NDJSON state events and a non-blocking review flow (returns a session_id + review URL), so an agent can submit → poll → execute through approval gates.

Speaker note: "Six months ago we governed people using databases. Now we govern AI agents using everything — and the agent experience is genuinely good, not bolted on."

---

## SLIDE 5 — Game-changer #2: Machine Identities

**Non-human access — CI, ETL, BI tools, scripts, agents — is now a managed, auditable identity, not a shared secret in a config file.**

- **Issue, rotate, and revoke** credentials for machines (#1442, #1387).
- **Instant kill switch** — revoking a machine credential immediately tears down *every* live proxy session it has open, across Postgres, SSH, HTTP, RDP and the broker — not "whenever the token expires" (#1387).
- **Live audit streaming** — machine sessions stream their audit events in real time over SSE (#1442).
- Surfaced in the product with its own sidebar area (#1463).

Speaker note: "The honest truth about every infra team is that the scariest credentials are the non-human ones nobody rotates. We made those first-class and killable in one click."

---

## SLIDE 6 — Game-changer #3: The human connection, reimagined

**We made connecting as a human feel as direct as a machine — without giving up the audit trail.**

- **Unified human + machine connection flow** (#1478): one-time auth mints a persistent credential, the "Configure Session" ceremony is skipped for non-review connections, and the CLI gains `hoop connect --persistent-credential`.
- Human sessions now **stream audit events live and persist incrementally** — so a session is captured even if the client crashes mid-flight.
- **Stable per-user credentials** (#1398) — the token no longer changes on every re-issue, so people stop re-editing their local config every time they connect.

Speaker note: "The number one complaint about secure access tools is friction. We took friction out and added *more* audit fidelity at the same time."

---

## SLIDE 7 — Game-changer #4: Hoop Tunnel

**Native, zero-config access to anything Hoop brokers — `psql -h mydb.hoop` just works.**

- **End-to-end tunnel v1** (#1486): single-archive install, a systemd daemon, OAuth/local login, and auto-registered DNS so native clients resolve `*.hoop` hostnames with no manual setup.
- **macOS support** (#1489): real platform implementation (utun, LaunchDaemon, `/etc/resolver`) with dual-stack addressing.
- **Pause/resume without re-auth** (#1507): tunnel lifecycle (up/down) separated from authentication.
- Shipped **on by default** for new deployments at the end of the half (#1503).

Speaker note: "This is the feature that makes Hoop disappear into the developer's normal workflow. No copy-pasting connection strings. Just use your tools."

---

## SLIDE 8 — Game-changer #5: Audit & compliance you can hand to an auditor

**We went from "we record sessions" to "we can show you who changed what, on every surface, with PII flagged."**

- **Full internal audit-log system** (#1279, #1305): middleware that automatically captures every admin write operation — users, connections, guardrails, masking, agents, org keys, auth config — recording who, what, when, and the payload, with a browsable/filterable admin UI.
- **RDP session recording + web player** (#1314): record and replay RDP sessions in the browser (Rust RDP parser running inside Go via WASM).
- **PII detection on RDP replays** (#1391, #1424): an async pipeline OCRs recorded frames, runs Presidio PII detection, and overlays pixel-accurate highlights on the replay — with progress UI and on-demand re-analysis.
- **Structured guardrails in the audit trail** (#1334, #1346): every guardrail hit now records the rule name, direction, and matched terms — visible in session details — instead of an opaque error string.
- **Workflow timeline** (#1429): related sessions grouped by correlation ID into an expandable, step-by-step view.

Speaker note: "If a customer's security team asks 'prove it,' this half is the half we got an answer for every protocol — including the screen pixels in an RDP session."

---

# PART 2 — THE MAJOR FEATURE STORIES

## SLIDE 9 — Access governance got serious

**The biggest sustained investment of the half. Approvals went from blunt to surgical.**

- **Force-approval groups** (#1228, #1248) — designated groups can bypass normal review for emergencies, still fully audited.
- **Minimum approvals** (#1240) — require N approvals instead of every group.
- **Per-connection JIT max-duration** (#1235) — replaces the hard-coded 48-hour cap; admins set the window per connection.
- **Access time-range UI** (#1251) and **pre-approved session API** (#1210).
- **"Access Request" redesign** (#1275) — renamed from "Reviews," rebuilt as a dedicated rule system with its own API, data model and UI.
- **JIT review gate for native-client credentials** (#1317) — TCP/native connections now go through review just like everything else, sharing one audit session per credential.
- **True "kill session"** (#1351) — a revoke endpoint that actively disconnects live sessions across all proxy types.

Speaker note: "Every one of these came from a real customer asking 'can I make approvals work the way *my* org works?' Now they can."

---

## SLIDE 10 — ABAC & Rulepacks: policy that scales

**Stop assigning rules connection-by-connection. Assign them by attribute, in bundles.**

- **ABAC — Attribute-Based Access Control** (#1319, #1369): an intermediary layer that groups connections and attaches guardrail, access-request and data-masking rules — extended across guardrails, AI data masking and access control.
- **Rulepacks** (#1450): bundle guardrail + masking rules under one named unit and apply to many connections at once. Ships with nine managed packs (Postgres, MySQL, MongoDB, and more).
- **Guardrail suggestions at setup** (#1437, #1432): curated, service-specific guardrail recommendations on the resource-creation success screen — one click to secure a new resource.

Speaker note: "This is what lets a customer with 500 connections actually maintain a coherent policy. Bundles and attributes, not toil."

---

## SLIDE 11 — Enterprise identity & zero-trust

**The features that get us through enterprise security review.**

- **SPIFFE/SPIRE agent auth** (#1393, #1403): agents can authenticate with short-lived, rotatable JWT-SVIDs instead of a static `HOOP_KEY` — static keys still work. Inline JWKS bundle delivery and agent HA support.
- **API Key management** (#1385): `hpk_`-prefixed keys, stored SHA-256-hashed, raw shown once, scoped to connections via groups; CLI `--api-key` login for automation.
- **IAM GCP Federation** (#1480, #1495): mint short-lived cloud credentials per session by impersonating the calling user's IAM principal — so the cloud's own audit logs attribute actions to the *human*, not a shared service-account key. Includes a "Test as user" dialog for BigQuery.
- **SAML 2.0 hardening** (#1312): ForceAuthn, correct cookie Secure flag behind TLS proxies, broader IdP attribute mapping (Azure AD, UPN, mail aliases), config UI.
- **OIDC refresh tokens** (#1364, #1415): sessions refresh transparently instead of forcing re-login.
- **Auditor role** (#1359): true read-only access across all routes for compliance users.

Speaker note: "Remember our 'No SSO Tax' positioning — this is us backing it up. SAML, OIDC, SPIFFE: all in, all in the open-source product."

---

## SLIDE 12 — The protocol surface kept widening

**More of the customer's stack, brokered through one gateway.**

- **HTTP Proxy matured into a first-class protocol** — promoted to a real connection type (#1264), WebSocket upgrades (#1216), SSE streaming for AI/event feeds (#1372), guardrails enforcement (#1226) with audit (#1250), data masking (#1255), subdomain access (#1390), and large-response chunking so big payloads stop hitting gRPC limits (#1498).
- **Browser-based RDP** via the IronRDP web proxy (#1188).
- **Kubernetes & EKS** connection types with time-based tokens (#1241).
- **Grafana & Kibana** as brokered observability targets (#1232).
- **Git over SSH** via robust SSH multiplexing (#1246); **async SSH dispatch** so parallel sessions and big transfers stop blocking each other (#1441).
- **Resource Provisioning Hub** (#1449): provision least-privilege Postgres roles *from* Hoop — diff and apply CREATE ROLE / GRANT / REVOKE.

Speaker note: "Every new protocol is a new reason a team can standardize on Hoop instead of five different access tools."

---

## SLIDE 13 — Runbooks & automation

**Hoop as a programmable, GitOps-friendly platform.**

- **Runbook file uploads** (#1230), **field ordering** (#1261), and **min/max length validation** (#1360).
- **Mandatory metadata** (#1299, #1302): require fields like a ticket ID before a terminal or runbook can run.
- **Event routing for runbooks** (#1462): subscribe a runbook to platform events; the event payload maps into parameters and runs as a fresh, audited session.
- **Terraform provider support** (#1213, #1402, #1368): runbook config endpoints, user management by email-or-ID, license propagation for API-key automation.
- **Batch connection creation** from a YAML file (#1362).
- **Secrets-manager integration** (#1193): source role credentials from external secret stores.

Speaker note: "The customers growing fastest with us are the ones managing Hoop as code. We invested heavily in making that first-class this half."

---

## SLIDE 14 — The frontend rebuild begins

**We started the migration off ClojureScript onto React — without a big-bang rewrite.**

- **React shell introduced** (#1427): `webapp_v2` wraps the existing ClojureScript app; React now owns the Sidebar, Command Palette and auth, and we migrate pages one at a time.
- A wave of **parity-restoration** work followed: first-run setup flow (#1481), analytics privacy UI (#1463), session downloads (#1445), terminal video player (#1430), and a bundle of UX fixes (#1457).
- **Accessibility push** (#1289, #1295, #1296): ARIA, semantic markup, keyboard navigation of the DB schema tree, "skip to content," screen-reader support.
- **Parallel Mode** (#1233, #1215): run scripts across many connections with a selection modal and real-time execution summary.

Speaker note: "We're paying down a decade of frontend debt incrementally, while still shipping features on top. No frozen quarter, no rewrite death-march."

---

# PART 3 — MAKING CUSTOMERS HAPPY

## SLIDE 15 — Reliability wins customers actually felt

**The fixes that turned "Hoop is flaky with my tool" into "it just works."**

- **DBeaver / SSMS over Postgres & SQL Server** (#1411): reverted a concurrency change that caused out-of-order packets — restored correct behavior for everyday DB clients.
- **Large SSH/SCP transfers** (#1338): fixed data corruption, a gateway crash, and a goroutine-leak-driven OOM on big transfers.
- **SSH hangs under parallel use** (#1441): parallel sessions and large transfers no longer block each other; real upstream errors now surface instead of hanging.
- **Large HTTP/DB result sets** (#1498): ClickHouse-via-JDBC in DataGrip/DBeaver and other big payloads no longer fail on gRPC message limits.
- **MSSQL databases with dots/spaces in the name** (#1281).
- **Mongo connection params with spaces** (#1320).
- **AWS SSM native client** redirect/connection fixes (#1263).
- **Startup race condition** that could double-create connections (#1222).

Speaker note: "None of these have a marketing slide of their own. All of them are why a customer renewed."

---

## SLIDE 16 — Login & SSO: the silent churn-killers

**Auth bugs are invisible until they lock someone out. We closed a batch.**

- **Safari login loop** (#1217): fixed the Safari ITP-driven infinite OIDC/SAML loop.
- **SAML cross-site cookies** (#1247) and **URL token exchange fallback** (#1249) for restrictive cookie environments.
- **API-key gRPC sessions** no longer killed after 5 minutes by the IDP token poller (#1482).
- **Self-service org migration** (#1392) for multi-tenant SSO users auto-placed in the wrong org.
- **G Suite group sync** fix for MCP auth (#1464).

Speaker note: "Every one of these is someone who, on a bad day, would have filed a 'Hoop is broken' ticket. They never had to."

---

## SLIDE 17 — Windows, downloads, and the long tail of polish

**The small stuff that signals craft.**

- **`hoop versions` on Windows** (#1509): install/sync/upgrade now work with `hoop.exe`, copy-based activation, and PATH guidance.
- **`hoop versions` / `hoop upgrade`** (#1465): an nvm-style in-CLI version manager that keeps client and gateway in sync.
- **CLI/agent version mismatch warning** (#1358): no more silent protocol-incompatibility surprises.
- **Unified session downloads** (#1445): one component, TXT/CSV/JSON, everywhere.
- **Long-running session feedback** (#1420): a clear "still running" message instead of a misleading success toast when a query exceeds the exec timeout.
- **Streaming large session results** (#1292): big outputs render virtualized in the browser instead of forcing a file download.
- **Reviewer attribution back in session details** (#1511): who approved/rejected, when, on hover.
- **Slack approval clarity** (#1265): partial-approval messages now show a "1 of 3" progress counter.
- **No more silently dropped connection config** (#1493): typed-but-not-"Added" config files and env vars are now saved.

Speaker note: "These are the details people screenshot and post in their team Slack. Cheap to build, disproportionate goodwill."

---

# PART 4 — THE FOUNDATION

## SLIDE 18 — We rebuilt how we ship

**Faster, safer releases — and we can prove the build is correct.**

- **Auto-release pipeline** (#1421): merge a labeled PR → tag + GitHub Release created automatically; exactly-one-label enforcement.
- **Nightly releases + sandbox auto-deploy** (#1374), with **AI-assisted migration-safety checks** that analyze DB migrations and comment on the PR.
- **Integration tests in CI** (#1373, #1439, #1440, #1501): Postgres and MySQL agent integration suites driving the real agent against testcontainers.
- **Race detector re-enabled** (#1500) after fixing a real data race in all four DB proxies.
- **API changelog automation** (#1309, #1452): breaking-change detection posted as a PR comment.
- **Go 1.26 + dependency/vuln bumps** (#1380, #1383, #1487).
- **libhoop version traceability** (#1318, #1336, #1343).

Speaker note: "We can now ship nightly, with an AI checking our migrations and the race detector watching our backs. A year ago this was a manual `make publish`."

---

## SLIDE 19 — We learned to see ourselves

**Product analytics matured from on/off to a real, privacy-respecting telemetry story.**

- **Per-org analytics modes** (#1438): `identified | anonymous | disabled`, replacing the global kill-switch — customers control their own privacy.
- **Session usage analytics** (#1315, #1348, #1508): track verb (connect/exec) and origin (cli, webapp, api, mcp, runbooks, proxymanager, agent).
- **Org-level enrichment** (#1260): org-id, API hostname, license type on every event; PostHog group tracking.
- **Email-addressable analytics for OSS** (#1395): so support and growth can actually talk to OSS users — while enterprise installs stay pseudonymous.
- **MTU cost fix** (#1473): collapsed double-counted users (~14k → ~6k).

Speaker note: "We can finally answer 'what do people actually use?' with data instead of vibes — and we did it without becoming creepy about it."

---

## SLIDE 20 — First impressions matter

**We made the very first five minutes with Hoop feel good.**

- **Phased, human-readable boot logs** (#1389, #1388): `docker compose up` shows a clean, colored startup trace and a "Welcome to Hoop" card pointing you at the UI — JSON logs preserved for CI/Helm/AWS.
- **First-run setup page** (#1396, #1481): any URL redirects to a guided `/setup` when no org exists.
- **README rebuilt** (#1341): concrete before/after examples (PII masking, a blocked `DROP TABLE` from an AI agent), clear positioning, animated hero.
- **Smarter onboarding** (#1267, #1223): full cross-OS install docs, "Native" connection option preferred over CLI.

Speaker note: "An evaluator's first `docker compose up` now tells a story instead of vomiting 20 lines of JSON. That's a conversion lever."

---

## SLIDE 21 — Monetization, done gracefully

**Free-tier limits that let people explore before they hit a wall.**

- **Usage-based gating** (#1475): Dashboard, Jira Templates and Resource Discovery are explorable on the free tier up to a limit, instead of hard-locked — better product-led conversion.
- **Feature-gated data masking & guardrails** (#1252, #1256, #1417): OSS/Free get a meaningful taste (one rule), with a clear sales callout at the limit.
- **Brazilian CPF PII type** (#1469) for our Brazil DLP customers.

Speaker note: "We learned to gate by *showing* value first, not hiding it. Let people feel the product, then talk to sales."

---

# PART 5 — CLOSING

## SLIDE 22 — H1 2026 by the theme

- **Security & Access Control** — force approvals, JIT, ABAC, Rulepacks, API keys, SPIFFE, IAM federation, kill-session. *The spine of the half.*
- **AI / MCP** — admin + user MCP server, OAuth 2.1, AI session analyzer, Claude Code integration. *The new frontier.*
- **Protocols & Connectivity** — HTTP proxy, RDP, SSH, K8s/EKS, BigQuery, Tunnel, provisioning. *The widening moat.*
- **Sessions, Audit & Compliance** — audit-log system, RDP record+PII, structured guardrails, workflow timeline. *The proof.*
- **Machine & Human Identity** — Machine Identities, unified human flow, persistent credentials. *The unifying idea.*
- **Foundation** — React migration, release automation, integration tests, analytics. *The runway for H2.*

---

## SLIDE 23 — What it adds up to

**In H1 2026, Hoop became the single, audited control plane for human, machine, and AI access — across every protocol our customers run.**

- We didn't just add features. We **unified an identity model**, **widened the protocol surface**, and **made the whole thing provable** to an auditor.
- We made it **lower-friction for the people using it** and **safer for the people responsible for it** — at the same time.
- And we **rebuilt the foundation** (frontend, releases, tests, telemetry) so H2 ships faster than H1.

Speaker note: "230 PRs. One story: every identity, every protocol, fully governed, fully audited — and nicer to use than it was in January."

---

## SLIDE 24 — Thank you

To the team: **230 merged PRs.** Every fix in the long tail, every game-changer headline — it's all here because of you.

Speaker note: close on the team. Name names if you can.

---

### Appendix — full PR index by month
*(Available on request — the underlying digest covers all 230 PRs with one-line summaries, organized Jan→Jun. Pull specific numbers from there for backup slides or Q&A.)*
