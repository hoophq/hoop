# Hoop H1 2026
	What we shipped, who it helped, and where it's taking us.

	Every PR merged to hoophq/hoop, Jan 1 – Jun 8, 2026.

---

## 230 PRs merged
	~1.6 changes shipped every working day.

	Six themes carried the half:

	- Access governance got deeper and auditable
	- AI became a first-class identity
	- The protocol surface kept widening
	- Audit & compliance became provable
	- Hoop Tunnel turned native access into a product
	- We rebuilt the foundation

---

## The big idea of H1
	Hoop stopped being an access gateway for humans. It became the control plane for *every* identity — human, machine, and AI agent — across *every* protocol, fully audited.

	- **Humans** — one-time auth, persistent credentials, native tunnel
	- **Machines** — first-class identities for CI/ETL/BI, with live audit
	- **AI agents** — request access, pass approvals, recorded like a person

---

# Part 1
	The Game-Changers

---

## Hoop became an AI-native platform
	We shipped the MCP gateway — Hoop is now controllable *by* agents and a secure broker *for* them.

	- Admin MCP server in the gateway, full admin parity, behind existing auth & audit (#1384)
	- User-facing MCP tools — an agent acts as a user, through approval gates (#1408)
	- MCP secured with OAuth 2.1 Resource Server support (#1404)
	- AI session analyzer — risk analysis in the session lifecycle (#1306, #1474)
	- Claude Code as a governed resource + `hoop claude configure` (#1277, #1425)
	- Agent-ready CLI: `exec --output json`, submit → poll → execute (#1376)

---

## Machine Identities
	Non-human access — CI, ETL, BI, scripts, agents — is now a managed, auditable identity, not a shared secret in a config file.

	- Issue, rotate, and revoke machine credentials (#1442, #1387)
	- Instant kill switch — revoking tears down every live session across PG, SSH, HTTP, RDP, broker (#1387)
	- Live audit streaming over SSE (#1442)
	- Its own area in the product sidebar (#1463)

---

## The human connection, reimagined
	Connecting as a human now feels as direct as a machine — without losing the audit trail.

	- Unified human + machine flow: one-time auth, skip "Configure Session", `--persistent-credential` (#1478)
	- Sessions stream audit live and persist incrementally — captured even if the client crashes (#1478)
	- Stable per-user credentials — no more re-editing local config every connect (#1398)

---

## Hoop Tunnel
	Native, zero-config access — `psql -h mydb.hoop` just works.

	- End-to-end tunnel v1: installer, daemon, OAuth login, auto-registered DNS (#1486)
	- macOS support — utun, LaunchDaemon, /etc/resolver, dual-stack (#1489)
	- Pause/resume without re-auth (#1507)
	- Shipped on by default for new deployments (#1503)

---

## Audit you can hand to an auditor
	From "we record sessions" to "we can prove who did what, on every surface, with PII flagged."

	- Full audit-log system: every admin write captured, with a browsable UI (#1279, #1305)
	- RDP session recording + in-browser replay (#1314)
	- PII detection overlaid on RDP replays via OCR + Presidio (#1391, #1424)
	- Structured guardrails in the trail — rule, direction, matched terms (#1334, #1346)
	- Workflow timeline — related sessions grouped by correlation ID (#1429)

---

# Part 2
	The Major Feature Stories

---

## Access governance got serious
	The biggest sustained investment of the half. Approvals went from blunt to surgical.

	- Force-approval groups for emergencies, still audited (#1228, #1248)
	- Minimum approvals — require N, not every group (#1240)
	- Per-connection JIT max-duration, replacing the 48h cap (#1235)
	- Access time-range UI + pre-approved session API (#1251, #1210)
	- "Access Request" redesign — dedicated rule system (#1275)
	- JIT review gate for native-client credentials (#1317)
	- True "kill session" — disconnect live sessions on revoke (#1351)

---

## ABAC & Rulepacks
	Stop assigning rules connection-by-connection. Assign them by attribute, in bundles.

	- ABAC — attach guardrail, access, and masking rules by attribute (#1319, #1369)
	- Rulepacks — bundle rules, apply to many connections; nine managed packs (#1450)
	- One-click guardrail suggestions at resource setup (#1437, #1432)

---

## Enterprise identity & zero-trust
	The features that get us through enterprise security review.

	- SPIFFE/SPIRE agent auth — short-lived rotatable SVIDs, agent HA (#1393, #1403)
	- API Key management — hashed `hpk_` keys, scoped, `--api-key` login (#1385)
	- IAM GCP Federation — per-session credentials attributed to the human (#1480, #1495)
	- SAML 2.0 hardening — ForceAuthn, cookie/TLS fixes, broader IdP mapping (#1312)
	- OIDC refresh tokens — no forced re-login (#1364, #1415)
	- Auditor role — true read-only across all routes (#1359)

---

## The protocol surface kept widening
	More of the customer's stack, brokered through one gateway.

	- HTTP Proxy as a first-class protocol — WebSockets, SSE, guardrails, masking, big-payload chunking (#1264, #1216, #1372, #1226, #1255, #1498)
	- Browser-based RDP via the IronRDP web proxy (#1188)
	- Kubernetes & EKS connection types (#1241)
	- Grafana & Kibana as brokered targets (#1232)
	- Git over SSH + async SSH dispatch (#1246, #1441)
	- Resource Provisioning Hub — provision Postgres roles from Hoop (#1449)

---

## Runbooks & automation
	Hoop as a programmable, GitOps-friendly platform.

	- File uploads, field ordering, length validation (#1230, #1261, #1360)
	- Mandatory metadata — require a ticket ID before running (#1299, #1302)
	- Event routing — subscribe a runbook to platform events (#1462)
	- Terraform provider support (#1213, #1402, #1368)
	- Batch connection creation from YAML (#1362)
	- Secrets-manager integration (#1193)

---

## The frontend rebuild begins
	We started the migration off ClojureScript onto React — without a big-bang rewrite.

	- React shell wraps the CLJS app; pages migrate one at a time (#1427)
	- Parity restored: setup flow, analytics UI, downloads, video player (#1481, #1463, #1445, #1430)
	- Accessibility push — ARIA, keyboard nav, screen-reader support (#1289, #1295, #1296)
	- Parallel Mode — run scripts across many connections (#1233, #1215)

---

# Part 3
	Making Customers Happy

---

## Reliability wins customers felt
	The fixes that turned "Hoop is flaky with my tool" into "it just works."

	- DBeaver / SSMS over Postgres & SQL Server — out-of-order packets fixed (#1411)
	- Large SSH/SCP transfers — data corruption, crash, and OOM fixed (#1338)
	- SSH hangs under parallel use resolved (#1441)
	- Large HTTP/DB result sets no longer hit gRPC limits (#1498)
	- MSSQL names with dots/spaces; Mongo params with spaces (#1281, #1320)
	- AWS SSM native client; startup race condition (#1263, #1222)

---

## Login & SSO: the silent churn-killers
	Auth bugs are invisible until they lock someone out. We closed a batch.

	- Safari ITP infinite login loop fixed (#1217)
	- SAML cross-site cookies + URL token-exchange fallback (#1247, #1249)
	- API-key gRPC sessions no longer killed after 5 minutes (#1482)
	- Self-service org migration for misplaced SSO users (#1392)
	- G Suite group sync fix for MCP auth (#1464)

---

## The long tail of polish
	The small stuff that signals craft.

	- `hoop versions` on Windows + nvm-style version manager (#1509, #1465)
	- CLI/agent version mismatch warning (#1358)
	- Unified session downloads — TXT/CSV/JSON everywhere (#1445)
	- Clear feedback on long-running sessions (#1420)
	- Streamed large session results in the browser (#1292)
	- Reviewer attribution back in session details (#1511)
	- Slack "1 of 3" partial-approval progress (#1265)
	- No more silently dropped connection config (#1493)

---

# Part 4
	The Foundation

---

## We rebuilt how we ship
	Faster, safer releases — and we can prove the build is correct.

	- Auto-release on merge + exactly-one-label enforcement (#1421)
	- Nightly releases + AI-assisted migration-safety checks (#1374)
	- Postgres & MySQL integration tests in CI (#1373, #1439, #1501)
	- Race detector re-enabled after fixing a real DB-proxy race (#1500)
	- API changelog / breaking-change automation (#1309, #1452)
	- Go 1.26 + dependency and vuln bumps (#1380, #1383, #1487)

---

## We learned to see ourselves
	Product analytics matured into a real, privacy-respecting telemetry story.

	- Per-org analytics modes: identified / anonymous / disabled (#1438)
	- Session usage analytics — verb and origin tracked (#1315, #1348, #1508)
	- Org-level enrichment + PostHog group tracking (#1260)
	- Email-addressable analytics for OSS; enterprise stays pseudonymous (#1395)
	- MTU cost fix — double-counted users collapsed (~14k → ~6k) (#1473)

---

## First impressions matter
	We made the very first five minutes with Hoop feel good.

	- Phased, human-readable boot logs on `docker compose up` (#1389, #1388)
	- Guided first-run setup page (#1396, #1481)
	- README rebuilt — concrete before/after examples (#1341)
	- Smarter onboarding — cross-OS docs, Native preferred over CLI (#1267, #1223)

---

## Monetization, done gracefully
	Free-tier limits that let people explore before they hit a wall.

	- Usage-based gating — explore before the sales wall (#1475)
	- Tasteful feature gates on masking & guardrails (#1252, #1256, #1417)
	- Brazilian CPF PII type for Brazil DLP customers (#1469)

---

# Part 5
	Closing

---

## H1 2026 by the theme
	- **Security & Access Control** — the spine of the half
	- **AI / MCP** — the new frontier
	- **Protocols & Connectivity** — the widening moat
	- **Sessions, Audit & Compliance** — the proof
	- **Machine & Human Identity** — the unifying idea
	- **Foundation** — the runway for H2

---

## What it adds up to
	In H1 2026, Hoop became the single, audited control plane for human, machine, and AI access — across every protocol our customers run.

	- We unified an identity model
	- We widened the protocol surface
	- We made the whole thing provable
	- We made it lower-friction to use and safer to operate — at the same time
	- We rebuilt the foundation so H2 ships faster than H1

---

## Thank you
	230 merged PRs.

	Every fix in the long tail, every game-changer headline — it's all here because of you.
