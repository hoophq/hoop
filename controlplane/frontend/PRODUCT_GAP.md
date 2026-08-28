# PRODUCT_GAP.md: this frontend against the product

Where `controlplane/frontend` stands against [`../PRODUCT.md`](../PRODUCT.md). Read it
before you touch a feature page, and before you conclude from a screen here what the
product model is — several of these screens are the **gateway's**, kept because the
Control Plane had nowhere else to land them.

Nothing in this file is a task list. It is the record of what diverges and why, so that
a divergence is a decision someone makes on purpose instead of one an agent stumbles
into. The initiative has a naming session pending; that is why so many rows say
"pending" rather than naming a new label.

Every row was checked against the tree. Line numbers are from the commit this file
landed on and will drift; the file paths will not.

## The one-paragraph version

This app was extracted from `webapp_v2/`, the gateway's UI. It talks to the **gateway**
on `:8009` — every service file calls a gateway route, and there is no `/sidecars`
endpoint anywhere. So its three feature pages edit the gateway's rule tables, using the
gateway's two-type rule vocabulary, scoped to gateway connections. The Control Plane's
own two jobs — **one view of every Sidecar** and **rule sets** — have no implementation.
The information architecture already reserves a place for the first; the second has no
route at all.

---

## Route by route

| Route | Verdict | What it actually is |
|---|---|---|
| `/` | keep | `pages/Home/index.jsx`. Admin redirects to `/sidecars`; a non-admin gets a deliberate dead end, correctly reasoned in the file's own comment. |
| `/sidecars` | **missing** | `NotImplemented`. This is the admin landing page, so the app's front door is a placeholder. |
| `/reviews`, `/reviews/:sessionId` | question | `NotImplemented`. |
| `/reviews/rules` | question | Gateway JIT / command access requests. |
| `/reviews/slack` | question | Drives the gateway transport-plugin registry. |
| `/features/ai-session-analyzer` | rename or scope | The gateway analyzer, not the documented AI Analyzer. |
| `/guardrails` | rename or scope | The gateway guardrail engine, two rule types. |
| `/features/data-masking` | rename or scope | Gateway Live Data Masking (Presidio/DLP), not the Sidecar `mask` block. |
| `/organization/users` | keep | Administration. Matches "administration only, never end users". |
| `/login`, `/signup`, `/setup`, `/register`, `/auth/callback`, `/signup/callback` | keep | Local auth plus IDP, which is what `control-plane/install` describes: "authentication is local by default". |
| — | **missing** | Rule sets. |
| — | **missing** | Direct Access, and the listener concept it needs. |
| — | **missing** | License management. |
| — | **missing** | Sessions, events and stats. |

---

## The three feature pages are the gateway's

This is the single fact most likely to be got wrong, because the pages look right.

`docs/adr/0009-guardrails-and-masking-architecture.md` records two engines for two
deployments, and this app edits the left-hand one. The docs are explicit from the other
side too: `features/guardrails` ends with *"Looking for guardrails on the Hoop Gateway
instead? That is a different implementation"*, and `features/data-masking` says the same
about Live Data Masking.

### `/guardrails`

- `src/pages/Guardrails/helpers.js` offers exactly two rule types, `deny_words_list` and
  `pattern_match`, against the [eight in the Sidecar](../PRODUCT.md#the-eight-rule-types).
- Rules carry input/output sections and attach to gateway connections. A Sidecar rule
  carries `name`, `message`, `action` and attaches to a listener.
- `src/services/guardrails.js` → `GET|POST|PUT|DELETE /guardrails`, the gateway's
  `guardrail_rules` table.
- Nothing here expresses `policy.enforce`, first-match-wins ordering, or the
  concatenate-on-inherit rule.

**Do not infer the Sidecar rule model from this page.**

### `/features/data-masking`

- The gateway DLP model: `supported_entity_types`, `custom_entity_types`, a score
  threshold, and per-connection assignment (`src/pages/Features/DataMasking/helpers.js`).
- The four Sidecar strategies — `redact`, `mask`, `partial`, `hash` — appear nowhere.
  Neither do `entity` XOR `columns`, `keep_last`, `mask_char`, or the rule that a
  listener's `mask` block **replaces** the inherited list.
- Its docs link pointed at the **Sidecar** Data Masking page while the screen is the
  gateway's. Retargeted to `/docs/learn/features/live-data-masking` in `src/utils/docsUrl.js`.

### `/features/ai-session-analyzer`

The name is the first divergence: the docs' AI Analyzer classifies a **statement**; this
one classifies a **session**.

| | Documented AI Analyzer | This page |
|---|---|---|
| Providers | `vertex`, `anthropic`, `openai` | `azure-openai`, `anthropic`, `openai`, `custom` — no `vertex` (`ConfigureTab.jsx`) |
| Credential | `credentials_file`, a path, `0600` or stricter, never inline | inline `api_key` in a `PasswordInput`, posted to `POST /ai/session-analyzer/providers` |
| Actions | `allow`, `warn`, `block`, `defer`, `review` | `allow_execution`, `block_execution`, `require_access_request` (`helpers.js`) |
| Cost controls | `trigger`, `cache`, `max_calls`, `max_input_bytes`, `timeout_sec` | none |
| Failure | `fail_open`, defaulting true | not expressed |
| Redaction | `send: raw \| redacted \| refuse` | not expressed |

Two of those are load-bearing:

- **`require_access_request` corresponds to `review`, which the docs mark Coming soon and
  which a Sidecar refuses at startup.** Do not treat human approval as a live path
  through a Sidecar.
- **Any Control Plane → Sidecar analyzer delivery has to turn a stored key into a path or
  a mounted secret.** A Sidecar config with an inline `api_key` is rejected as an unknown
  field. Never render a key input for the Sidecar path.

---

## What the Control Plane is for, and does not do yet

### One view of every Sidecar

`core-concepts/control-plane` names three fields: which Sidecars are running, what each
one resolved, and when each last checked in. None appears in the code.

`/sidecars` is `NotImplemented` and names the project that owes it. Two notes for
whoever fills it:

- **The four states already exist**, as `inventory.State` in
  `controlplane/backend/internal/api/inventory/` on the EVL-230 branch. Do not invent a
  fifth string:

  | State | Means |
  |---|---|
  | `connected` | socket open, heartbeat current, acked generation equals issued |
  | `stale` | socket open, acked generation behind the issued one past the heartbeat window |
  | `rejected` | the sidecar sent `config.nack`; carry the reason |
  | `disconnected` | socket closed, record retained with a last-seen timestamp |

  The NACK reason is the highest-value field in that view. Inventory storage is
  in-memory, so after a Control Plane restart the list is empty for about one backoff
  window — say that in the UI rather than rendering an empty list that reads as an
  outage.
- **`HOOP_KEY` in the placeholder was wrong.** That is the *agent's* DSN variable
  (`agent/config/config.go`). It is now a name-agnostic string in `src/Router.jsx`,
  because the documented names — `control_plane.token_file` and
  `HOOP_SIDECAR_TOKEN_FILE` — are themselves marked Draft. See
  [PRODUCT.md](../PRODUCT.md#what-the-docs-say-does-not-exist-yet).

### Rule sets

The Control Plane's differentiator: Guardrail plus Analyzer plus Data Masking applied to
a resource as one posture, in one action. No page, no route, no service.

The nearest existing primitive is the gateway's `/rulepacks`, which already bundles
`GuardRailRules` + `DataMaskingRules` + `ConnectionNames` — behind
`experimental.rulepacks`, returning 404 when the flag is off
(`gateway/api/rulepacks/rulepacks.go`). There is no `services/rulepacks.js` here. Read
that handler before designing anything; do not reinvent the shape.

The three feature pages are the pre-rule-set state, not the target architecture.

### Direct Access, and the listener

Direct Access is one of the four documented Features and has no UI, no route and no nav
entry. The frontend has no concept of a listener at all — which is why nothing here can
express the things that hang off one:

- `enforcing` versus `observe-only` per listener, defaulting to **observe-only**. The
  documented rollout is: ship observe-only, read `/api/events?kind=violation`, then turn
  enforcement on. Nothing in this app supports that loop.
- The resolved-config view. `GET /config` returns `lanes[]` with the rules each lane
  actually ended up with after inheritance, and the docs call it "the first thing to
  check when a rule you wrote never fires".

### License

`control-plane/install` makes applying the license step 2 of the install. There is no
license page. `src/layout/LicenseBanner/index.jsx` is read-only and its only action opens
support — its own comment says the gateway had a self-serve page and the control plane
does not. `docsUrl.setup.licenseManagement` is defined and unconsumed, so the link target
already exists.

The free-tier caps are also scoped wrong for this product. Four pages carry the same
test: `Guardrails/index.jsx:70`, `Features/DataMasking/index.jsx:68` and
`Features/AiSessionAnalyzer/index.jsx:84` read `isFreeLicense && list.length >= 1`, and
`Features/AccessRequest/index.jsx:81` reads it as `isFreeLicense && rules.length >= 1`.
`isFreeLicense` is organization-wide. The
documented free tier is **one rule per feature per Sidecar**, and the Control Plane
itself is Enterprise, so inside it the cap should not be reachable at all. Do not copy
the pattern into a new page without a decision.

### Sessions, events, stats

Fully specified on the Sidecar's admin API and entirely absent here: no service, no
store, no route.

Two constraints that must survive into whatever gets built:

- **The Sidecar admin API has no authentication and no CORS, and belongs on loopback.** A
  browser must never call `localhost:19000` directly. It goes through the Control Plane.
- **`audit.memory_buffer` and `audit.query_sessions` both default to `0`,** so an empty
  result can mean the sink is disabled. Rendering that the same as "no traffic" is a
  wrong answer that looks like a correct one.

---

## Naming, all in one place

Nothing below is decided. It is listed so that a divergence is visible at the moment
someone writes a label.

| Screen says | Docs say | Wire says |
|---|---|---|
| Resource Role | `resource` (the upstream), `connection` (the per-listener name) | `connection_id` / `connection_name`, `GET /connections` |
| Session Analyzer | AI Analyzer | `/ai/session-analyzer/*` |
| Live Data Masking | Data Masking | `/datamasking-rules` |
| Reviews → Rules | *review is Coming soon* | `/access-requests/rules`, license feature `access-requests` |
| Guardrails | Guardrails (a different engine) | `/guardrails` |
| — | rule set | `/rulepacks` (`experimental.rulepacks`) |

Two traps in that table:

- **"Resource Role" is not a docs term for this product.** Four features scope rules by
  it, and two of them key by **name** while two key by **id** — that is why both
  `ConnectionsMultiSelect` (id) and `ConnectionNamesMultiSelect` (name) exist. Pick by
  what the endpoint takes, not by which one you saw first.
- **"Agentic" means two things.** In the docs it is the request *path* through the
  analyzer, paired with Direct Access. In the gateway it is a boolean field on an
  analyzer rule. They are unrelated.

---

## The API this app will move to

`controlplane/frontend` calls the gateway on `:8009` today, and the backend
`controlplane/CLAUDE.md` names that explicitly as the reason its own route paths are
still cheap to rename. When that changes, the target is `0.0.0.0:8020` — deliberately not
8009, because the two run side by side for the whole transition.

From `controlplane/backend/internal/api/routes.go` on the EVL-230 branch:

```
GET  /healthz                              GET    /api/userinfo
GET  /readyz                               GET    /api/sidecar-configs
                                           POST   /api/sidecar-configs
POST /api/login                            GET    /api/sidecar-configs/:name
POST /api/register                         PUT    /api/sidecar-configs/:name
POST /api/logout                           DELETE /api/sidecar-configs/:name
                                           GET    /api/fleet
POST /api/sidecar-auth/enroll              GET    /api/fleet/:name
POST /api/sidecar-auth/rotate              DELETE /api/sidecar-auth/credentials/:name
```

What a frontend needs to know about it:

- **Every `/api` route answers `501` today**, naming its ticket. That is deliberate: a
  stub here never returns an empty list, because an empty fleet view reads as "no
  sidecars are connected" and somebody acts on it. The two probes are real —
  `GET /healthz` answers 200 with the version, `GET /readyz` answers 200 or 503 from a
  database ping.
- **A route is closed unless it is in the unauthenticated set.** `RequireAdmin` is
  mounted and returns 501, so protected routes are closed rather than open.
- **CORS is a strict allow list, empty by default.** A dev machine opts in with
  `CONTROLPLANE_CORS_ALLOWED_ORIGINS`. There is a test that fails on a wildcard.
- **The config document is `sidecar/daemon.Config`, shipped as JSON.** Not a second
  schema. Generation travels in the wire envelope, never inside the document, because
  the sidecar's loader calls `DisallowUnknownFields` and one extra key fails the whole
  document.
- **Delivery is a WebSocket push**, not the HTTP pull the public docs describe. See
  [PRODUCT.md](../PRODUCT.md#where-the-docs-and-the-repository-disagree). Do not design
  an instant "Apply" toast on top of the docs' pull wording, and do not design a poller
  on top of the design's push either — the sidecar acks with `config.ack` or refuses with
  `config.nack`, and the fleet view reports what it was told, never what was sent.
