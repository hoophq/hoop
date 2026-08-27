# ADR-0010: One ordered rule list, six matchers, first denial wins

- **Status:** Accepted
- **Date:** 2026-08-27
- **Author:** @matheusfrancisco
- **Code:** [`sidecar/policy/policy.go`](../../sidecar/policy/policy.go), [`sidecar/policy/pii.go`](../../sidecar/policy/pii.go), [`sidecar/lexer/`](../../sidecar/lexer), [`sidecar/inspect/sqlmeta.go`](../../sidecar/inspect/sqlmeta.go)
- **Related:** [ADR-0005](0005-sidecar-flow.md) (the flow this sits in), [ADR-0006](0006-sidecar-config-defaults-and-overrides.md) (how a lane's list is assembled from global + lane), [ADR-0008](0008-analyzer-enforces-without-opa.md) (the `ai_analysis` rule, which this set deliberately does not evaluate), [ADR-0009](0009-guardrails-and-masking-architecture.md) (where enforcement runs at all)
- **Supersedes / Superseded by:** —

## Context

A lane has to answer one question per statement: may this run here. The input
is what the classifier produced — the raw `Text` the client sent, plus
`Operation`, `Effects`, `Relations` (name + read/write) and `Tables` derived
from the lexer, which discards comments and string-literal content.

The design question is not "how do we match", it is **which of those fields
each rule reads**, because the same intent expressed against two different
fields behaves differently in both directions:

- Against `Text`, `SELECT 'DROP TABLE customers'` is a DROP. Against
  `Operation` it is a select.
- Against `Text`, a `DELETE` inside a CTE and a `DELETE` inside a comment look
  the same. Against `Effects`, one is a delete and the other is nothing.
- Against `Tables`, `INSERT INTO staging SELECT * FROM customers` "touches
  customers". Against `Relations` it reads customers and writes staging.

The other forces:

- **Both failure directions are expensive and only one is visible.** A rule
  that over-matches blocks a human's work and gets switched off within a day. A
  rule that under-matches is silent forever.
- **The classifier is best-effort by construction.** No grammar, no AST; a
  statement it cannot model yields `OpUnknown` with a reason, and function
  bodies are out of reach at any price.
- **The denial has to explain itself.** The operator writes the message and it
  has to reach the developer's terminal, or they file a ticket instead of
  fixing the query.
- **The list is assembled from two places.** ADR-0006 concatenates a lane's
  rules ahead of the global defaults, so whatever precedence this set has is
  precedence across two blocks in one file.

## Options considered

1. **Text matching only** — a word list and a regex, which is what the
   control-plane guardrail path offers (`deny_words_list`, `pattern_match`).
   One mechanism, no classifier to maintain, works on any protocol. Rejected as
   the only mechanism: it denies a verb inside a literal, misses the same verb
   inside a CTE, and cannot express "writes to this table" at all. Kept as two
   rule types, because for a substring that is never legitimate anywhere in a
   statement (`pg_read_file`), the raw text *is* the truth.
2. **A real SQL grammar per dialect.** Precise, and it still cannot see into a
   function body or a `DO $$ … $$` block. Rejected on cost and drift: five
   engines, five grammars, each a dependency that has to track a server
   release.
3. **An expression language over the statement struct** (CEL and friends). One
   rule type, arbitrary power. Rejected for the reason this config refuses
   everything else: an arbitrary expression cannot be checked at startup for
   whether it can ever fire, and "it loaded and never matched" is the failure
   mode being designed against.
4. **Lexer-derived facts plus typed matchers, one ordered list.** Each rule
   type is a small closed matcher over one field. Chosen.

## Decision

**Six rule types, each keyed to exactly one field.** The type is the choice of
field; there is no rule that matches on two.

| `type` | reads | matching |
|---|---|---|
| `operation` | `Operation` (the worst effect, not the leading verb) | exact equality against `operations:` |
| `table` | `Relations`, falling back to `Tables` | lowercased; a bare name matches any schema qualification; `access:` narrows to read or write; `require_table_match:` decides the unclassifiable case |
| `deny_words_list` | `Text`, raw | case-insensitive substring, both sides uppercased |
| `pattern_match` | `Text`, raw | RE2, case-sensitive unless the pattern says `(?i)` |
| `pii` | `Text`, through the detector | denies when a named entity class is found in the query |
| `ai_analysis` | the statement, via a model | **not evaluated here** — lifted out and appended after the local set, see ADR-0008 |

**One ordered list per lane, and the first match that denies ends evaluation.**
Every type denies on match, which is what makes the order safe: it decides
which rule name and message the user reads, never whether the statement runs.
That invariant is what lets ADR-0006 concatenate a lane's rules with the global
defaults without anyone reasoning about the combination.

**`action: defer` is the one exception to "match means deny".** It records a
`Finding` and evaluation continues, so one statement can report several
findings and still be denied by a later hard rule. First-match-wins applies to
denials only.

**There is no allow rule.** Adding a terminal `allow` would let a lane rule
shadow a reviewed global deny and would end the invariant above. If it is ever
added it has to come with a startup warning naming both rules.

**A rule that could never fire is a startup error, and every such error is
reported in one run.** An empty word list, an uncompilable pattern, an empty
operation list, `access: readwrite`, an unknown type, a `pii` rule with no
detector configured, a `pii` rule naming an entity the detector was not built
to find. The reasoning is the same each time: those configurations load, look
healthy and enforce nothing.

**Rule evaluation errors deny.** `Rules.FailOpen` exists in the struct and
nothing in `daemon/` sets it, so a match that errors returns "policy evaluation
failed; denying". In practice it is unreachable, because compilation happens at
startup.

**A denial is the rule's own message**, or a generated one naming the rule and
the operation when the author left `message:` empty. It is rendered in the
protocol's native error frame (ADR-0009).

## Consequences

### The rule type is a real choice, and the wrong one is silently wrong

Two lanes, same query, one rule each:

```
operation rule [drop, delete]:   SELECT 'DROP TABLE customers'  -> allowed
deny_words_list [drop, delete]:  SELECT 'DROP TABLE customers'  -> DENIED
deny_words_list [drop, delete]:  SELECT count(*) FROM customers -- no deletes here -> DENIED
```

Both denials are false positives, and both are what the operator asked for by
choosing that type. The guidance that follows: use `operation` for verbs, use
`table` for objects, and reserve the text matchers for strings that are never
legitimate anywhere in a statement.

### `access` is why a table rule is worth having over a name list

```
rules: [customers-is-crm-owned (table, customers, access: write), read-only-lane (operation, [insert, …])]

DELETE FROM customers WHERE id = 99                        -> customers-is-crm-owned
INSERT INTO payroll(employee) SELECT name FROM customers   -> read-only-lane
```

The second statement reads customers and writes payroll, so the customers rule
correctly does not fire. Without `access` it would, and operators respond to
that by widening the rule until it protects nothing.

### Order is visible in the audit trail, and only there

Same two rules, opposite order, same `DELETE FROM customers`:

```
table rule first      -> "customers is written by the CRM sync"   rule=customers-is-crm-owned
operation rule first  -> "this connection is read-only"           rule=read-only-lane
```

Both deny. A reviewer checking "is this lane read-only" gets the same answer
either way; a developer reading the error gets a useful sentence only in the
first arrangement.

### KNOWN DEFECT: `require_table_match: true` denies every response

Policy runs on both directions. A server-direction statement is the
CommandComplete text (`SELECT 1`) with no relations and no tables, so a rule
with `require_table_match: true` — which exists to deny exactly that
"unclassifiable" case — refuses every result set on the lane:

```
lane rule: {type: table, tables: [customers], require_table_match: true}

  SELECT count(*) FROM payroll   ->  FATAL: this statement touches customers, or could not be shown not to

audit:
  {"kind":"statement","statement":"SELECT count(*) FROM payroll","tables":["payroll"],"allowed":true}
  {"kind":"violation","statement":"SELECT 1","operation":"unknown","rule":"customers-locked","allowed":false}
```

The request passed and the *result* was refused, so the client received no
rows and lost the connection. Nothing completes on such a lane. The documented
hazard was `SET`, `SHOW` and `BEGIN`; this is larger and it is not documented
anywhere.

The fix belongs in `Rules.EvaluateWith`: a `table` rule has no business
evaluating a `FromServer` statement, and `require_table_match` should be scoped
to the client direction. Until that lands, treat `require_table_match` as
unusable on a postgres lane. It is recorded here rather than fixed quietly
because it changes what an existing config does.

### `enforce: false` is not a dry run

`buildPolicy` returns nil for a non-enforcing lane, so no rule is evaluated at
all. The trail then carries `statement` rows and no `violation` rows, even for
a statement an identical enforcing lane refuses. Teams asking for "run it for a
week and show me what it would have blocked" are asking for something that does
not exist; the closest thing today is enforcing rules with `action: defer` and
a decider that allows.

### What this commits us to

- A new rule type names the field it reads, in its doc comment and in the
  table above. A type that matches on two fields is a design smell: it is two
  rules an operator cannot order independently.
- A new rule type ships with its startup refusals in the same change. "Loads
  and never fires" is the bug class this whole package is shaped around.
- The deny-only invariant stays until someone writes the ADR that ends it.

### How this was verified

Five lanes over one Postgres 16, masking off so only the verdict showed, driven
with `psql`; the transcript above is copied from that run. Startup refusals
came from `hoop-inspect -validate` against a config carrying one of each
misconfiguration:

```
no-words: deny_words_list with no words
bad-regex: bad pattern: error parsing regexp: missing closing ): `customers(`
no-ops: operation rule with no operations
weird-access: unknown access "readwrite" (read, write, or empty for either)
typo: unknown rule type "tabel"
pii-no-detector: pii rule needs a scanner (use policy.NewRulesWithScanner)
```
