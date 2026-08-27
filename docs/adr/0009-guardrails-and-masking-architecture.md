# ADR-0009: Where guardrails and masking run, and the deny/mask split

- **Status:** Accepted
- **Date:** 2026-08-27
- **Author:** @matheusfrancisco
- **Code:** [`libhoop/agent/`](../../libhoop/agent), [`libhoop/redactor/`](../../libhoop/redactor), [`sidecar/policy/`](../../sidecar/policy), [`sidecar/gate/`](../../sidecar/gate), [`sidecar/pii/alcatraz/`](../../sidecar/pii/alcatraz), [`sidecar/lexer/`](../../sidecar/lexer)
- **Related:** [ADR-0005](0005-sidecar-flow.md) (one request through the relay, current state), [ADR-0006](0006-sidecar-config-defaults-and-overrides.md) (how a lane resolves its rules), [ADR-0008](0008-analyzer-enforces-without-opa.md) (the AI evaluator's half of the same chain), [ADR-0010](0010-local-sql-rule-set.md) (the local rule set this decision's rule 7 feeds)
- **Supersedes / Superseded by:** —

## Context

Two things have to happen to a database session that a policy governs: a
statement that must not run has to be refused before the server sees it, and a
value that must not leave has to be rewritten on the way back. Both are
protocol work. A DELETE and a SELECT are the same TCP bytes to anything that
cannot parse pgwire, and a masked cell is a length-prefixed frame that has to
be rebuilt, not a string in a JSON body.

This repository does that in **two engines**, and the split is the first thing
to understand about this area:

| | control-plane path | standalone relay |
|---|---|---|
| rules stored in | Postgres: `guardrail_rules`, `datamasking_rules` | one YAML file |
| shipped by | gateway, in `pb.AgentConnectionParams` per session | read from disk at startup |
| evaluated in | the **agent**, inside `libhoop/agent/<proto>/` | the `hoop-inspect` relay process |
| rule vocabulary | `deny_words_list`, `pattern_match` | 8 types, including operation/table/pii/ai_analysis |
| detection | MS Presidio, alcatraz or GCP DLP | alcatraz, in-process |

They exist for different deployments, not by accident of history: the first
needs a control plane, an identity and an agent already on the network path;
the second is one process next to one database with no control plane at all.

The forces that shaped both:

- **You can only enforce where the protocol is decoded.** The gateway sees
  framed packets it relays; it has no pgwire parser and no TDS parser. Putting
  a rule there means matching on bytes.
- **A false positive costs more than a false negative here.** A guardrail that
  blocks a legitimate query stops a human's work and gets switched off. A
  masker that rewrites `amount_cents` because nine digits look like an SSN gets
  switched off the same day, and then nothing is masked.
- **The database's own controls are not enough, and not the point.** `GRANT`
  cannot mask a column per session, cannot carry an operator-authored message
  to the client, and does not exist in the same shape across five engines.
- **Wire framing is unforgiving.** Postgres rows carry a message length and a
  per-column length prefix. Change a value in place and the client loses
  synchronization with the server on the next message.
- **Every failure mode here is silent by default.** A rule that never matches,
  a masker that never fires, a detector that was never configured for the
  entity a rule names: all of them look exactly like a healthy deployment.

## Options considered

1. **Enforce in the gateway plugin chain** (`review → audit → dlp →
   accesscontrol → webhooks → slack`). It is already in the path and already
   has the session context. Rejected: the chain sees relayed packets, so a
   guardrail there is substring matching on a byte stream with no notion of a
   statement, and masking there cannot rebuild frames. The vestige of this
   choice is still in the tree — `gateway/guardrails/guardrails.go:106
   Validate` is a complete matcher that nothing on the session path calls, and
   `gateway/transport/plugins/dlp/dlp.go:18-24` is a licence check wearing the
   name of a DLP engine.
2. **Push it into the database** — grants, row-level security, DDL triggers,
   per-column views. Cheapest to trust, and genuinely the right answer for
   coarse authorization. Rejected as the mechanism: it cannot mask for one
   session and not another, cannot explain itself to the developer, and
   duplicating a policy across Postgres, MySQL, SQL Server, Oracle and MongoDB
   dialects is five policies that drift.
3. **Enforce in the agent, inside the protocol proxies** (`libhoop/agent/`).
   The bytes are already decoded there, the session already has an identity,
   and the control plane already ships per-session parameters. Chosen for the
   product path.
4. **A standalone inspecting relay driven by a config file** (`sidecar/`).
   Same decoding argument, no control plane, no agent, no gateway. Chosen for
   deployments that want tier-2 inspection behind something else that already
   owns identity — Envoy, a service mesh, a sidecar container.

Both 3 and 4 shipped. They are not competing implementations of one feature:
they occupy different deployment shapes and share only the `libhoop` module.

## Decision

Seven rules govern this area. Each is enforced by code in both engines, and
each has a failure that motivated it.

### 1. Rules are evaluated where the protocol is decoded; the gateway only distributes

The gateway resolves a connection's rules and puts them in the session's
parameters (`gateway/transport/client.go:311` `encodeGuardRailRules`); the
agent hands them to a libhoop proxy constructor; the proxy evaluates them
against decoded statements. No plugin in the chain inspects query text.

### 2. Requests are denied, responses are masked. A request is never rewritten

Masking rewrites bytes coming back. It never touches what the client sent,
because rewriting a statement changes what the database executes: that is a
correctness change wearing a privacy label.

The corollary is that a national ID in a `WHERE` clause is a **guardrail**
problem, not a masking problem. By the time a response exists, the literal is
already in the server's query log, its slow-query log and any `EXPLAIN` output.
So `type: pii` is a rule that **denies the statement** (`sidecar/policy/pii.go`),
and mask rules only ever see result cells.

### 3. Detection is pattern-and-checksum with an opt-in entity set

No NER model on the data path. The detector is alcatraz: regular expressions
plus validators (SSN area/group/serial ranges, CPF mod-11, IBAN, Luhn), 51
built-in entity types plus three credential recognizers registered by
`sidecar/pii/alcatraz/secrets.go`.

`pii.entities` is **required and has no all-entities default**. An SSN carries
no checksum, so any nine digits in a legal range is a valid `US_SSN`: measured
at roughly 32% of random nine-digit business ids. Enabling everything means an
`amount_cents` column comes back redacted, which is how an operator learns to
turn masking off.

### 4. Masking rebuilds frames; it never overwrites bytes in place

Two mechanisms, chosen by asking the codec rather than by naming protocols
(`gate.MaskSupported`):

| mechanism | protocols | how length stays correct |
|---|---|---|
| re-framing | postgres, mssql/TDS | the codec rebuilds the message: `'T'` supplies column names, `'D'` rows are held, a changed row is re-encoded with fresh message and per-column lengths |
| substitution | http | body bytes replaced, `Content-Length` retagged |
| refused at startup | everything else | `mask.enabled` on such a lane is a config error, not a silent no-op |

Held rows must be released on close (`Gate.FlushResponse`), and a response body
whose header block already went out is forwarded **unmasked** and audited,
because `Content-Length` can no longer be corrected.

### 5. A denial is rendered in the protocol's own error frame

Dropping the connection teaches the developer nothing and produces a support
ticket. Every supported protocol gets a native error carrying the
operator-authored message: pgwire `ErrorResponse` with SQLSTATE `42501`, TDS
error number 50000 class 16 followed by `DONE`-with-error, HTTP 403 with
`X-Hoop-Denied: policy`.

In the relay the pgwire severity is `FATAL`, not `ERROR`, and the socket then
closes (`sidecar/proxy/deny.go:147-149`): `ERROR` would leave the client
waiting for a `ReadyForQuery` that is never coming, so the user would see a
hang instead of the message.

### 6. A handler that cannot enforce a configured rule refuses the session

libhoop gates proxy construction on what the protocol handler actually
implements (`libhoop/libhoop.go:42-43`: "Remove a proxy from this refusal only
when its protocol handler actually calls ValidateGuardRailRules for both
directions"). MySQL refuses any guarded session at `libhoop/libhoop.go:99-101`;
MSSQL and MongoDB refuse **output** rules; SSM and raw TCP refuse everything.
`HasGuardRailRules` reads an unknown payload shape as "guarded", so a schema
change cannot silently disarm a rule.

The relay makes the same call at startup instead of at connect: an unsupported
protocol, an entity no configured detector can find, `mask.enabled` with no
rules, a `defer` with nothing to defer to — each is a refusal that names the
lane, and all of them are reported in one run.

### 7. Statement classification is lexer-derived, and reports the worst effect

A guardrail keyed on the first word of a statement is trivially bypassed and
trivially over-triggered. The relay's classifier (`sidecar/lexer/`) is a
single-pass, dialect-aware tokenizer over a stack of labelled regions, which
yields the effects a statement has, and the relations it touches with
`read`/`write` access. `Operation` is the **worst** effect, not the verb typed.

Consequences that fall out of that choice, all covered by
`sidecar/inspect/bypass_test.go`: a `DELETE` inside a CTE is a delete;
`SELECT 'DROP TABLE x'` is a select, because literal content is never
classified; `EXPLAIN DELETE` is a select and `EXPLAIN ANALYZE DELETE` is a
delete; `INSERT INTO staging SELECT * FROM customers` writes `staging` and
reads `customers`, which is what `access: read|write` exists to express.
An unparseable statement is `OpUnknown` with a reason, and fails closed only if
a rule asked for that (`require_table_match`).

## Consequences

### Protocol capability is uneven, and that is the first thing to check

Control-plane path (verified by reading `libhoop/agent/*` and
`libhoop/libhoop.go`):

| protocol | input guardrails | output guardrails | masking | guarded session refused |
|---|---|---|---|---|
| postgres | yes (simple + Parse/Bind) | yes | yes | no |
| oracle | yes | yes | yes | no |
| mssql / TDS | yes (SQLBatch + RPC) | **no** | **no** | if output rules set |
| mongodb | yes (OP_MSG) | **no** | yes | if output rules set |
| mysql | **no** | **no** | yes | **always** |
| ssm, raw tcp | no | no | no | always |

`grep -ric guardrail libhoop/agent/mysql/` returns 0 in every file. MySQL is a
hole with a fence around it, not a gap that silently passes traffic.

Relay: postgres, mssql and http are the registered codecs
(`sidecar/codec/all/all.go:17-20`). A lane naming mysql or mongodb fails config
validation. Masking is available on postgres, mssql and http; the MSSQL
reframer latches off for the rest of the connection on a `SQL_VARIANT`, `XML`
or `UDT` column, and policy keeps working while masking silently stops.

### Detection has known blind spots, and column rules are the answer to some

A column rule keys on the name in `RowDescription`, which is the **output
label**, so an alias defeats it. An entity rule keys on the value, so it
survives an alias and dies on reshaping (`replace(ssn,'-','')`, a split across
two columns, a broken checksum). Neither covers a value that is both aliased
and detector-refused — alcatraz declines all-sequential SSNs like
`123-45-6789` as fixtures, which is why the shipped configs put a column rule
and an entity rule on the same column.

`COPY … TO STDOUT` is never masked: it emits `CopyOutResponse`/`CopyData`, not
`RowDescription`/`DataRow`, so there is no column name and no row frame to
rebuild. The statement **is** classified, so the answer is a policy rule, not a
mask rule.

### The engines' operational defaults differ, on purpose and not

Fail direction is deliberate per component: local rule errors deny, OPA
outages deny, an audit-sink outage denies only under `audit.fail_closed`, and
the AI analyzer fails **open** because a classifier that denies during a vendor
outage takes the database down with it. Two others are accidents of the
control-plane path worth knowing before writing a rule: deny words are
case-sensitive substrings in both engines, so `DROP TABLE` misses
`drop table`; and a PCRE pattern that works against Presidio fails to compile
in the RE2 `localguardrails` fallback, which refuses to build rather than
matching less.

### What we are committed to

- Adding a protocol means writing a codec or a libhoop proxy, not extending a
  matcher. There is no path to "guardrails for MySQL" that does not decode
  MySQL.
- Any new enforcement point must render a native error frame and must refuse
  the session when it cannot enforce a configured rule. Silent pass-through is
  the one behavior this whole area exists to prevent.
- Masking stays response-side. A proposal to rewrite requests is a proposal to
  change query semantics and needs its own ADR.

### What we will have to revisit

- **Two rule vocabularies.** The control-plane path has two rule types and the
  relay has eight. A customer who moves from the agent to the relay, or the
  reverse, re-authors their policy. Nothing in the repo federates the two, and
  nothing should until someone wants both on one byte path.
- **Bound parameters are invisible to the relay.** The pgwire codec decodes
  `Query` and `Parse`; `Bind` values are skipped, so a rule matching literal
  content sees `$1`. The agent path reconstructs Parse+Bind
  (`libhoop/agent/postgres/redactor.go:113-117`) and the relay does not.
- **A relay denial discards the whole read.** The gate nulls the payload for
  the entire 32 KiB chunk, including statements earlier in the same buffer that
  were already audited as allowed. The trail then claims a statement ran that
  the upstream never saw.

### How this was verified

A real Postgres 16 behind the relay, driven with `psql` and `pgbench`
(reproduced by `run.sh` in the scratch notes for this work):

```
SELECT name, email, ssn, cpf, iban FROM customers
  -> [REDACTED:EMAIL_ADDRESS] | ***-**-8973 | [REDACTED:BR_CPF] | ******************5555

SELECT ssn AS employee_code FROM customers
  -> ***-**-8973      entity rule caught the real SSN
  -> 123-45-6789      alias defeated the column rule, detector refuses the fixture

DELETE FROM customers WHERE id = 1
  -> FATAL:  customers is written by the CRM sync; change it there   (socket closes)

SELECT * FROM customers WHERE cpf = '529.982.247-25'
  -> FATAL:  do not put a taxpayer id in a query; it lands in the database's own logs

SELECT 'DROP TABLE customers'          -> allowed
WITH gone AS (DELETE ... RETURNING id) -> denied, the CTE body was seen
SELECT count(*) FROM public.payroll    -> denied by a rule naming bare `payroll`
COPY (SELECT email, ssn ...) TO STDOUT -> NOT masked
```

Extended query protocol via `pgbench -M prepared`: the allowed read completed,
the denied write returned the same `FATAL`, and the audit trail recorded
`{"kind":"statement","metadata":{"pg.message":"Parse"}}`,
`{"kind":"masked","masked_entities":["EMAIL_ADDRESS","column:ssn"]}` and
`{"kind":"violation","rule":"customers-is-crm-owned"}`.
