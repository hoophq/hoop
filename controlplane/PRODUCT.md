# PRODUCT.md: what the Control Plane is

The product model, taken from the five sections of `https://hoop.dev/docs` the team
treats as the source of truth. Read this before you name anything, before you design a
form, and before you decide that a screen the gateway had belongs here too.

`controlplane/CLAUDE.md` says how to write the backend. `controlplane/frontend/CLAUDE.md`
says how to write the frontend. Neither says what the product is. This does.

**Where this file and the docs disagree with the repository, the disagreement is the
point.** It is written down under [Where the docs and the repository disagree](#where-the-docs-and-the-repository-disagree)
rather than smoothed over, because both sides are live and an agent reading only one of
them builds the wrong thing.

## The five sections

Raw markdown is served at `https://hoop.dev/docs/<path>.md`.

| Section | Pages |
|---|---|
| Getting Started | `introduction/getting-started`, `introduction/quickstart` |
| Core Concepts | `core-concepts/sidecar`, `core-concepts/control-plane` |
| Features | `features/direct-access`, `features/agentic-access`, `features/data-masking`, `features/guardrails` |
| Control Plane Setup | `control-plane/install`, `control-plane/deployment/docker-compose`, `control-plane/deployment/kubernetes`, `control-plane/deployment/aws`, `control-plane/connect-sidecar` |
| Sidecar Reference | `setup/configuration/hoop-inspect/get-started`, `.../config-file`, `.../policy-rules`, `.../risk-analysis`, `.../components`, `.../kerberos` |

Everything else on that site — Hoop Gateway, API Reference, Blog, Community — describes
a **different product**. Do not source a fact about the Control Plane from it.

<a id="soft-404"></a>
**The site has no 404.** A path that does not exist silently serves the docs home page,
whose subtitle is `Runtime control for agents`. A link check that reads the HTTP status
passes on a dead link. Read the body:

```bash
curl -sS -L "https://hoop.dev/docs/<path>.md" | head -8 | grep -q "Runtime control for agents" \
  && echo DEAD
```

One false positive, always: `introduction/getting-started` **is** the home page, so it
reports DEAD and is fine.

---

## The model

### Sidecar — the engine

A proxy that runs next to the resource it protects. It decodes the wire protocol between
a client and that resource, and decides what reaches the resource and what comes back.

One binary, one config file. No external dependency, no database, no gateway, no agent.
It ships inside the `hoop` CLI and starts with `hoop start sidecar --config config.yaml`.

It routes nothing, and normally terminates no client TLS. Something in front of it —
usually Envoy — owns the network path and identity, and forwards plaintext to it. The
Sidecar needs the connection in the clear, because it reads the wire protocol.

The one exception is `downstream_tls`, valid only on a `postgres` listener: pgwire
negotiates TLS in-band with an 8-byte `SSLRequest` that a plain TLS listener in front
cannot answer, so the Sidecar terminates that leg itself. Set it on any other protocol
and config load fails.

### Listener — one upstream

A listener is one bind address in front of one upstream, speaking one protocol, with its
own rules. `listeners` is a list, so a second resource is a second entry, not a second
process.

`protocol` is `postgres`, `mssql` or `http`, and it picks the codec. Two other listener
fields are gated by it: `http` is only valid on an `http` listener, `downstream_tls` only
on a `postgres` one. Everything else in the listener is the same shape either way.

**A listener is called a *lane* in the code and in `GET /config`.** The config file says
`listeners`; the resolved state says `lanes`. Both refer to the same object.

| | Reads on the request | Reads on the response |
|---|---|---|
| `postgres` | full statement text, its worst effect, every effect, each relation as a read or a write | result columns, row count, masking by re-framing |
| `mssql` | SQLBatch and RPC, `sp_executesql` unwrapped | result columns, masking by re-framing the TDS token stream |
| `http` | method, path, normalized resource; body and headers **only** with `http.capture_body` | status; body and headers under the same setting |

The HTTP codec captures nothing by default. Everything captured reaches the policy
engine, the audit trail and — where an analyzer runs — a third party, so each listener
opts in explicitly.

### Control Plane — the brain

A single Sidecar reads one file and needs nothing else. That stops working at twenty:
twenty files, twenty deploys to change one rule, and no single place that says which
rules are live.

The Control Plane is that place. Sidecars register with it and ask it for their
configuration; rules are written once and delivered everywhere.

Four properties, from `core-concepts/control-plane` and `control-plane/connect-sidecar`:

- **No session to maintain.** The Sidecar loads its config into memory and keeps
  running. A Control Plane restart is not an outage for the resources behind it.
- **The token is the authentication.** No second credential, no certificate exchange.
- **The Sidecar always dials out.** The Control Plane never dials in. No inbound
  firewall rule, no NAT traversal.
- **Listeners still come from the local file.** What the Control Plane manages is rule
  sets, masking policies and analyzer settings.

It solves three things, and the third is the one with no implementation anywhere yet:

1. Rule combinations decided once, in one place, per resource.
2. **Pre-configured rule sets.** Protecting a resource without one means writing a
   Guardrail, then an Analyzer, then Data Masking — three edits that have to agree. The
   Control Plane ships them as sets: one action applies a whole posture.
3. **One view of every Sidecar:** status, resolved config, and last check-in time.

### Administration only

The end user never authenticates with the Control Plane. They reach their resource the
way they already do, and the Sidecar is transparent on the path. A requirement that
starts with "when the end user logs in" belongs to a different product.

---

## Two paths, two features

```
                   ┌─ Direct Access ──> Guardrails, Data Masking ──┐
request ──────────>┤                                               ├──> resource
                   └─ Agentic Access ─> AI Analyzer ──> a tool ────┘
                                        (block | allow | warn | defer | review)
```

**Direct Access** is the default. The Sidecar decodes the request, evaluates it against
the rules you wrote, and forwards it. Nothing about it is probabilistic: the same
statement produces the same verdict every time, no third-party API is in the chain, and
the only latency added is a parse and a scan. It **fails closed**.

**Agentic Access** routes the request through the AI Analyzer first. The analyzer
classifies the statement and the risk it reports selects a tool. It **fails open** by
default — see [Failure directions](#failure-directions).

Both run in the same Sidecar and a listener picks per rule, so this is not an either/or
for a deployment, only for a class of statement. The usual shape is direct rules for
everything nameable, and one `ai_analysis` rule as the backstop for the rest.

**Guardrails** and **Data Masking** are reachable from both paths.

| | Direct Access | Agentic Access |
|---|---|---|
| Decides with | rules you wrote | a language model classifying the statement |
| Determinism | same input, same verdict | a classification, with a cache |
| Latency added | parse and scan | a model call, roughly 100 ms to 2 s uncached |
| External dependency | none | your model provider |
| Failure mode | fails closed | fails open by default |
| Best for | effects you can name | intent you cannot enumerate in advance |

---

## Canonical vocabulary

The left column is what the product is called. The middle column is what the code says
today. A mismatch is not a bug to fix in passing — it is recorded in
`frontend/PRODUCT_GAP.md`, and the initiative has a naming session pending.

| Docs term | What the repository calls it | Where |
|---|---|---|
| Sidecar | `sidecar/` module; the binary and `HOOP_INSPECT_CONFIG` keep the `hoop-inspect` spelling | `sidecar/CLAUDE.md` |
| listener | `lane` in the resolved config and in the policy engine | `GET /config` → `.lanes[]` |
| resource | the upstream a listener points at | `upstream: host:port` |
| connection | the operator-facing name a listener records in audit and exposes to policy | listener field `connection` |
| **Resource Role** | *not a docs term.* Gateway vocabulary for a gateway `connection`. The frontend's primary user-facing noun | `frontend/src/components/ConnectionsMultiSelect` and 10 other files |
| Guardrail | gateway `guardrail_rules`, 2 rule types; sidecar `policy.rules`, 8 rule types | `docs/adr/0009` |
| Data Masking | gateway "Live Data Masking" (Presidio/DLP); sidecar `mask` block | `docs/adr/0009` |
| AI Analyzer | gateway calls its own "AI Session Analyzer" | `gateway/api/ai/` |
| statement | the unit the AI Analyzer classifies | — |
| session | the unit the **gateway's** analyzer classifies. Not the same thing | — |
| rule set | closest existing primitive is `rulepack`, behind `experimental.rulepacks` | `gateway/api/rulepacks/` |
| Agentic Access | the request **path** through the analyzer | — |
| `agentic` (boolean) | a gateway analyzer rule field. Collides with the term above and is not a path selector | `gateway` migration `000112_ai_session_analyzer_agentic` |

---

## Rule semantics a form has to respect

### A rule set is an ordered deny list

Three sentences cover the whole thing:

1. A rule matches, and by default it **denies**.
2. **First match wins** among the rules that deny.
3. No match means allowed.

Every rule takes `name`, `message` and `action` on top of its own fields. `message` is
what the user reads on denial, delivered in the protocol's own error frame — a real
pgwire `ErrorResponse`, or `403` with an `X-Hoop-Denied` header on HTTP. Leave it empty
and the rule falls back to a generated string. Write one.

### `policy.enforce` defaults to false

Without it a listener inspects and audits but denies nothing. That is the right way to
roll out and the wrong way to leave it. `GET /config` reports `enforcing` per listener,
and `--validate` prints `observe-only`.

### Inheritance is asymmetric, and this is the top silent failure

| Field | Merge | Why |
|---|---|---|
| `policy.rules` | **concatenate**, listener first | Every rule denies and first match wins, so concatenating cannot change allow/deny — only which message the user reads. |
| `policy.opa` | replace | One lane, one decision endpoint. |
| `policy.enforce` | replace | A lane rolling out behind an enforcing default has to be able to say observe-only. |
| `mask` | **replace** | A rule owns an entity type. Two concatenated lists leave two rules competing for one entity. |

Adding one column rule to a listener therefore **drops every inherited entity rule**.
List them again alongside it. Reading the file will not tell you what a lane resolved;
inheritance happens at startup. Ask `GET /config`.

### `action` is `defer` or nothing

| `action` | Effect |
|---|---|
| *(empty)* | Deny. First match wins. |
| `defer` | Record a finding, keep evaluating, hand the decision to an external policy endpoint. |

`action: warn` and `require_review` are **refused at startup**. `warn` exists only as a
per-tier action on an `ai_analysis` rule. `defer` with no `policy.opa.url` is refused
too — a finding nobody reads forwards every statement while looking like enforcement.

### The eight rule types

| `type` | Matches on | Fields | Protocols |
|---|---|---|---|
| `operation` | the statement's most consequential effect | `operations` | SQL, HTTP |
| `table` | a relation the statement touches | `tables`, `access`, `require_table_match` | SQL |
| `deny_words_list` | a case-insensitive substring of the raw text | `words` | any |
| `pattern_match` | an RE2 regex over the statement text | `pattern_regex` | any |
| `pii` | entity classes a detector finds **in the request** | `entities` | any |
| `http_resource` | the normalized request path | `resources`, `methods` | HTTP |
| `http_status` | the response status | `statuses`, `methods` | HTTP |
| `ai_analysis` | risk a language model reports | `trigger`, `high`, `medium`, `low`, `prompt` | any |

Details that change a form:

- `operation` reads the **worst effect**, not the leading verb. A `DELETE` inside a CTE
  reports `delete`. `EXPLAIN DELETE` reports `explain`; `EXPLAIN ANALYZE DELETE` reports
  `delete`, because it runs. Vocabulary: `select insert update delete merge create drop
  alter truncate grant revoke call copy explain show set begin commit rollback`, plus
  `other` (parsed, unclassified) and `unknown` (the scanner could not finish). HTTP verbs
  are their own values: `get post put patch head options connect trace`.
- **`CALL` and `EXECUTE` report `unknown`, not `call`.** Their bodies live in the
  catalog. A rule written `operations: [call]` matches neither. Write
  `operations: [call, unknown]`.
- `access` on a `table` rule is `read` or `write`; unset matches either. Anything else is
  refused at startup. `require_table_match: true` also denies when the relations could
  not be determined — the fail-closed choice.
- `pattern_regex` is RE2: no lookaround, no backreferences. A bad regex is refused at
  startup, naming the listener and the rule.
- `pii` requires a top-level `pii.entities`. A rule naming an entity absent from that
  list is **refused at startup**, because otherwise the guardrail loads, evaluates, and
  matches nothing.
- `http_resource` matches a normalized path: `/users/12345/orders/98765` becomes
  `/users/*/orders/*`. A trailing `/**` matches any deeper path.
- `http_status` is response-side, which is why an authorization filter running before the
  upstream can never ask it. Exact codes (`"404"`) and classes (`"5xx"`) both work.
- `ai_analysis` takes per-risk-level actions instead of `action`; setting `action` on it
  is refused at startup.

### Data Masking

Masking runs on **responses only**. Requests are never rewritten: changing the statement
the upstream executes is a correctness change wearing a privacy label.

| Field | Meaning |
|---|---|
| `entity` | Entity type to rewrite wherever it appears. Required unless `columns` is set. |
| `columns` | Result-set column names to mask outright, compared case-insensitively. |
| `strategy` | `redact`, `mask`, `partial` or `hash`. Empty means `redact`. |
| `keep_last` | Tail length for `partial`. Default `4`. |
| `mask_char` | Replacement character for `mask` and `partial`. Default `*`. |

| Strategy | `4111111111111111` becomes |
|---|---|
| `redact` | `[REDACTED:CREDIT_CARD]` |
| `mask` | `****************` |
| `partial` | `************1111` |
| `hash` | `sha256:<first 16 hex>` |

`hash` is the one worth knowing: equal inputs give equal outputs, so a masked column
still works as a join key.

**Entity rules mask by detection** and work anywhere a value appears, including inside an
opaque HTTP body — and they miss whatever the detector does not recognize. **Column rules
mask by position**: they cannot miss, and they only work where the protocol names its
values. Use column rules for the columns you know and entity rules for the rest.

`pii.entities` is required and there is no all-entities default. Turning on every
recognizer rewrites ordinary numeric columns: nine digits in a legal range is a valid
`US_SSN` as far as any detector can tell. Card, CPF and IBAN carry real checksums, so
those three leave lookalike ids alone.

A `mask` block on a protocol whose codec can carry neither masking mechanism is refused
at startup.

### The AI Analyzer

```yaml
analyzer:
  provider: vertex                 # vertex | anthropic | openai
  model: claude-sonnet-4-5@20250929
  extra: {project: my-gcp-project, region: global}
  timeout_sec: 10
  fail_open: true                  # the default, deliberately
  send: redacted                   # raw | redacted | refuse
  max_input_bytes: 8192
  cache: {size: 4096, ttl_sec: 900}
  max_calls: 500
```

Tools, mapped per risk level. A level you do not name defaults to `allow`.

| Tool | Effect |
|---|---|
| `allow` | Forward. The verdict is still recorded. |
| `warn` | Forward and record the risk. Observe-only, one tier at a time. |
| `block` | Deny, with the model's own title in the protocol's error frame. |
| `defer` | Forward, and hand the decision to an external policy endpoint. |
| `review` | Hold the statement for a human to approve. **Coming soon — see below.** |

**The credential is always a path** (`credentials_file`), never inline, and the file must
be `0600` or stricter.

Three cost controls, because this is the only evaluator that leaves the process and costs
money per statement:

1. **`trigger` narrows what is classified.** An empty trigger is a startup error — the
   failure mode of the opposite default is an invoice.
2. **The cache keys on the statement shape.** `WHERE id = 1` and `WHERE id = 2` are one
   verdict; literals are stripped from SQL.
3. **`max_calls` is a process-lifetime backstop.** Past it, statements fall through to
   the local rules.

Local rules run **before** the analyzer, so a `DELETE` that a `type: operation` rule
already refuses never costs a model call. That ordering is fixed.

An `ai_analysis` rule on an HTTP listener without `http.capture_body` is refused at
startup. `authorization`, `cookie` and `proxy-authorization` can never be allowlisted as
captured headers, and no HTTP header ever reaches the model.

<a id="failure-directions"></a>
### Failure directions

The analyzer defaults to `fail_open: true`. Everything else fails closed. The difference
is what each depends on: OPA is a service you run, a language model is a third-party API
over the public internet, and a vendor outage that refuses every `UPDATE` on the lane is
a bigger incident than the one it prevents. `fail_open: false` makes your model provider
a hard dependency of your database.

`audit.fail_closed` defaults to `false`, which is the uncomfortable one: a dropped audit
write lets the statement proceed.

---

## The admin API on the Sidecar

The `admin` section is disabled when `listen` is empty. Every example in the docs binds
it to `127.0.0.1:19000`.

| Endpoint | Returns |
|---|---|
| `GET /healthz` | `ok` |
| `GET /stats` | totals, plus per-listener `addr`, `active`, `total` |
| `GET /config` | the **resolved** config: `lanes[] {name, protocol, listen, upstream, enforcing, rules[names], masking}` |
| `GET /events` | the last N events, sized by `audit.memory_buffer` |
| `GET /api/sessions`, `/api/sessions/{id}`, `/api/sessions/{id}/events`, `/api/events`, `/api/stats` | the query API, sized by `audit.query_sessions` |

Session shape: `id`, `principal`, `protocol`, `connection`, `duration_ms`,
`statement_count`, `denied_count`, `masked_count`, `verdict`. Filters: `principal`,
`connection`, `protocol`, `since`, `until`, `denied`, `open`, `q`, with `limit` and
`cursor` — cursor paging, not offset. `/api/events` also takes `session_id` and a
repeatable `kind`.

Six event kinds: `session_start`, `statement`, `violation`, `masked`, `error`,
`session_end`. A denial writes `violation` instead of `statement`.

**Two hard constraints.** This listener has no authentication and no CORS of its own, and
it serves a read interface to every statement every user ran — keep it on loopback. And
`audit.memory_buffer` / `audit.query_sessions` both default to `0`, so an empty result may
mean the sink is disabled rather than that nothing happened. A UI must not present the
two the same way.

---

## Licensing

Verbatim from `features/guardrails` and `features/data-masking`:

> **Free tier:** one Data Masking rule and one Guardrail **per Sidecar** are free,
> forever. Running more than one rule per feature, or managing rules centrally across
> Sidecars, requires Enterprise.

| | Sidecar alone | Sidecar + Control Plane |
|---|---|---|
| Configuration | one file per Sidecar, on disk | written once, delivered everywhere |
| Rules | one Data Masking rule + one Guardrail, free | unlimited, plus pre-configured sets |
| Visibility | that Sidecar's local admin API | every Sidecar in one place |
| Licensing | free | **Enterprise** |

So the free cap is **per Sidecar**, and the Control Plane itself is Enterprise — inside
it, rules are unlimited by definition. The upgrade URL the docs use is
`https://hoop.dev/start`.

---

## What the docs say does not exist yet

- **Human review.** `review` is listed as a tool and marked *Coming soon*.
  `require_review` is **refused at startup**, because holding a statement for approval
  needs a review backend the current build does not ship. `defer` is the closest working
  thing. Do not build a Sidecar feature on top of review.
- **The Control Plane handshake as documented.** `control-plane/connect-sidecar` carries
  its own warning: *"Draft. The exact configuration keys and environment variable names
  below are placeholders pending the Control Plane release. The flow is accurate; the
  field names are not final."* That covers `control_plane.url`,
  `control_plane.token_file`, `HOOP_CONTROL_PLANE_URL` and `HOOP_SIDECAR_TOKEN_FILE`.

---

## Where the docs and the repository disagree

Every row was checked against the tree. **The recorded decision wins over the docs page**
— the docs page for the handshake says so itself.

| Topic | hoop.dev/docs | The repository |
|---|---|---|
| CP ↔ Sidecar transport | "Ordinary HTTP … nothing is tunnelled and nothing bidirectional stays open"; "picked up on the ping, not pushed" | One WebSocket per sidecar, opened by the sidecar, with `config.apply` pushed down and `status` heartbeats up. gRPC was considered and rejected. `controlplane/CLAUDE.md`, "Decided" |
| Sidecar credential | `control_plane.token_file` / `HOOP_SIDECAR_TOKEN_FILE`, marked Draft | A `sidecarauth.Anchor` seam whose `Verify` returns the sidecar **name**, not a boolean. The anchor itself is an open question (EVL-234) |
| Control Plane deployment | three pages of its own | Those pages deploy the **gateway**: image `hoophq/hoop`, chart `hoop-chart`, service `hoopgateway`, port `8009`, `API_URL`, `POSTGRES_DB_URI`, local auth by default. The Control Plane backend binds `0.0.0.0:8020`, deliberately not 8009 |
| Code paths | `hoopinspect/proxy/proxy.go`, `hoopinspect/gate/gate.go` | Renamed to `sidecar/`. `hoop-inspect` survives as the binary name and `HOOP_INSPECT_CONFIG` |

Checked on `main`:

- `grep -rniE "control[_-]?plane" --include="*.go" .` finds only the unrelated IPC in
  `tunnel/`. There is no control-plane client in `sidecar/`, no sidecar endpoint in
  `gateway/`, and no `controlplane` entry in `go.work`. (The `-E` matters: without it
  `?` is a literal and the command matches nothing at all.)
- `grep -rn "os.Getenv" sidecar/ --include="*.go"` returns exactly one hit,
  `sidecar/lexer/conformance/corpus_test.go` reading `PG_REGRESS_SQL` — a test fixture
  path in a nested module. The sidecar itself reads no environment variable; the CLI
  wrapper does.
- The sidecar's only HTTP surface is inbound (`serveAdmin`). Its only outbound HTTP is
  the three analyzer providers and the OPA client.

The Control Plane backend lives on the unmerged branch
`origin/perotto/evl-230-bootstrap-new-controlplane-package-in-the-hoop-repo` (EVL-230),
with every handler answering `501` and naming its ticket.

---

## Two rule vocabularies

`docs/adr/0009-guardrails-and-masking-architecture.md` records this and already flags
"Two rule vocabularies" as a known cost. It matters here because a Control Plane that
ships rules to Sidecars has to reconcile them, and **nothing in the repository does
today**.

| | control-plane path | standalone relay |
|---|---|---|
| rules stored in | Postgres: `guardrail_rules`, `datamasking_rules` | one YAML file |
| shipped by | the gateway, in `pb.AgentConnectionParams` per session | read from disk at startup |
| evaluated in | the **agent**, `libhoop/agent/<proto>/` | the Sidecar process |
| rule vocabulary | 2 types: `deny_words_list`, `pattern_match` | the 8 types above |
| detection | MS Presidio, alcatraz or GCP DLP | alcatraz, in-process |

The docs are explicit about the split from the other side. `features/guardrails` closes
with *"Looking for guardrails on the Hoop Gateway instead? That is a different
implementation"*, and `features/data-masking` says the same about Live Data Masking.

`controlplane/frontend` edits the **left** column today. See
`frontend/PRODUCT_GAP.md`.

---

## Related ADRs

| ADR | What it settles |
|---|---|
| `docs/adr/0005-sidecar-flow.md` | How one request flows through the relay. A living current-state doc, not a decision. |
| `docs/adr/0006-sidecar-config-defaults-and-overrides.md` | The inheritance table above, and why `mask` replaces while `policy.rules` concatenate. |
| `docs/adr/0008-analyzer-enforces-without-opa.md` | The analyzer decides for itself; OPA is the opt-in second decider. |
| `docs/adr/0009-guardrails-and-masking-architecture.md` | The two engines, and the deny/mask split. |
| `docs/adr/0010-local-sql-rule-set.md` | One ordered rule list, first denial wins. |

## Link index

Only the Sidecar and Control Plane trunk. A new link in the product comes from here.
Check the body before you trust one — see [the soft-404 note](#soft-404).

```
/docs/introduction/getting-started        /docs/control-plane/install
/docs/introduction/quickstart             /docs/control-plane/deployment/docker-compose
/docs/core-concepts/sidecar               /docs/control-plane/deployment/kubernetes
/docs/core-concepts/control-plane         /docs/control-plane/deployment/aws
/docs/features/direct-access              /docs/control-plane/connect-sidecar
/docs/features/agentic-access
/docs/features/data-masking               /docs/setup/configuration/hoop-inspect/get-started
/docs/features/guardrails                 /docs/setup/configuration/hoop-inspect/config-file
                                          /docs/setup/configuration/hoop-inspect/policy-rules
                                          /docs/setup/configuration/hoop-inspect/risk-analysis
                                          /docs/setup/configuration/hoop-inspect/components
                                          /docs/setup/configuration/hoop-inspect/kerberos
```
