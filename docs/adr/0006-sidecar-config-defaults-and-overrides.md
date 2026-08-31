# ADR-0006: Global defaults and per-lane overrides in the sidecar config

- **Status:** Superseded by [ADR-0011](0011-sidecar-config-schema.md) and [ADR-0012](0012-sidecar-enforcement-defaults.md)
- **Date:** 2026-08-27
- **Author:** @matheusfrancisco
- **Code:** [`sidecar/daemon/config.go`](../../sidecar/daemon/config.go), [`sidecar/policy/`](../../sidecar/policy)
- **Related:** [ADR-0005](0005-sidecar-flow.md) (request flow, current state), [ADR-0008](0008-analyzer-enforces-without-opa.md) (why everything else in the analyzer is OPA-free), [ADR-0009](0009-guardrails-and-masking-architecture.md) (where the rules these defaults resolve are evaluated)
- **Supersedes / Superseded by:** Superseded by [ADR-0011](0011-sidecar-config-schema.md) (the schema and the merge table, restated under `guardrails` and `opa`) and [ADR-0012](0012-sidecar-enforcement-defaults.md) (the enforcement defaults and the `defer` refusal)

> **Superseded (2026-08-31):** the merge *mechanics* below are unchanged and
> still describe the code. Every key name in them has moved: `policy.rules` →
> `guardrails.rules`, `policy.opa` → `opa`, `policy.enforce` →
> `guardrails.mode`, and `mask.enabled` is gone. Two rows of the refusal table
> also changed behaviour rather than spelling. Read ADR-0011 and ADR-0012 for
> what ships; this file is preserved for the per-field reasoning, which the
> rename did not invalidate.

## Context

One `hoop-inspect` process serves several lanes. A lane is one listener: one
protocol, one upstream, its own rules, its own masking, its own OPA endpoint.
The compose stack in `deploy/docker-compose/envoy-stack/` runs two in one
process, `appdb` (postgres) and `httpbin` (http), and a per-user pod fronting
a database and an API runs the same way.

Most of what those lanes want is identical. "Never let a taxpayer id into a
query" is a property of the company, not of a listener; "redact emails on the
way back" is the same rule five times if each lane spells it out. So
`config.yaml` has top-level `policy:` and `mask:` blocks, and each entry under
`listeners:` may carry its own. Something has to say what happens when both
exist.

The forces:

- **Guardrails are a set; a rewrite is an owner.** Two deny rules matching one
  statement both mean "no", so adding a rule cannot make a config more
  permissive. Two mask rules claiming `EMAIL_ADDRESS` are two rewrites of one
  value, and whichever the slice order picks, the other was a lie.
- **A lane must be able to say less, not only more.** A lane rolling out behind
  an enforcing default needs observe-only. A binary protocol whose rows cannot
  be rewritten needs masking off against a global `enabled: true`. A zero
  `bool` cannot express "the operator said false" versus "the operator said
  nothing".
- **Silence is the failure mode that matters.** Every mistake in this file
  produces a lane that starts, looks healthy, and enforces less than its
  author believes. A rule naming an entity the detector was never configured
  to find never matches. A rule that defers to a decision endpoint that does
  not exist reports a finding nobody reads and then allows.
- **`defer` split matching from deciding.** A local rule with `action: defer`
  records a `Finding` and lets a later evaluator rule on it. Today the only
  evaluator that can is an OPA client, so a config keyword quietly turns a
  policy engine into a runtime dependency on the data path.

## Options considered

1. **Deep-merge everything.** One rule to remember: lane wins per key, lists
   concatenate. Rejected for masking: concatenating `[emails, ssn, cards]` with
   a lane's `[ssn-column]` leaves two rules competing for one column and the
   winner decided by slice position, which is not something an operator can
   read off the file.
2. **Replace everything.** Also one rule, and it makes every lane restate the
   company-wide guardrails. A lane that forgets one silently drops a control
   that a reviewer saw approved at the top of the same file. Rejected: the
   failure is invisible and points the wrong way.
3. **Per-field, chosen by what the field is** — concatenate the things that are
   a set of independent guards, replace the things that have a single owner.
   Two rules to remember instead of one, and the split is defensible per field.
   Chosen.

## Decision

We resolve one lane's stack from the defaults and its own block in
`(*Config).resolve`, once at startup, per field:

| Field | Global → lane | Why |
|---|---|---|
| `policy.rules` | **concatenate**, lane's first | The concatenated list splits in two before anything runs: `splitAnalyzerRules` lifts the `ai_analysis` rules out into their own evaluators, and what is left is the local rule set. Every local rule either denies or defers, and the first match wins, so concatenating them is monotonic in the allow/deny outcome. Order picks only which name and message the user reads, and lane-first lets a specific message beat a generic one. `ai_analysis` rules do not share that invariant; the order consequence below says what their position does and does not decide. |
| `policy.opa` | **replace** when the lane sets it | One lane has one decision endpoint. Two do not merge into one. |
| `policy.enforce` | **replace** when set (`*bool`) | A lane must be able to say observe-only against an enforcing default. |
| `mask.enabled` | **replace** when set (`*bool`) | Same, in the other direction: a protocol whose frames cannot be rewritten says false against a global true. |
| `mask.rules` | **replace** wholesale when non-empty | A rule owns an entity or a column. Concatenating produces two rewrites of one value. |
| `pii`, `analyzer`, `audit`, `admin` | process-wide, no lane override | One detector engine and one analyzer per process; the audit sink and admin port are properties of the container. |

The resolve is pure: config in, config out. `daemon.buildLanes` calls it at
startup, validation calls it so an error names the *resolved* lane rather than
"some default you inherited", and `GET :19000/config` renders it so the running
process can be asked what it concluded.

We also fix, in the same place, what a lane must carry for a rule to work at
all. These are startup refusals, not warnings:

| Config | Refused unless |
|---|---|
| a local rule with `action: defer` | the resolved lane has `policy.opa.url` |
| an `ai_analysis` rule with `high\|medium\|low: defer` | the resolved lane has `policy.opa.url` |
| `policy.opa.gate: true` | the lane has at least one `ai_analysis` rule |
| an `ai_analysis` rule with no `trigger:` | the lane has `policy.opa.gate: true` |
| any `ai_analysis` rule | the config has an `analyzer:` section, the protocol has a content builder, and on HTTP `http.capture_body: true` |
| a rule naming an entity | `pii.entities` lists it |
| `mask.enabled: true` | `mask.rules` is non-empty and the protocol supports masking |

`policy.opa.url` on its own needs none of this: it is a single-call decision
after the local rules, with no `input.phase` and no `input.findings`. OPA
becomes *mandatory* only where something defers, because deferring to a
decision that does not exist allows everything, which is the opposite of what
the operator asked for.

## Consequences

**A lane that overrides `mask` must restate every rule it still wants.** This
is live in our own stack: `envoy-stack/sidecar/config.yaml` declares five
global mask rules and the `appdb` lane declares four, so `appdb` does not mask
`CREDIT_CARD` while `httpbin`, which overrides nothing, does. The mechanism was
checked with an equivalent pair of http lanes over one upstream, since `appdb`
speaks postgres: the overriding lane returned `alice@example.com` in the clear
where the inheriting lane returned `[REDACTED:EMAIL_ADDRESS]`, having lost the
global email rule to its own one-rule block. That is the design working as
intended, and it is
also exactly the mistake the design invites. `curl :19000/config` and the
startup log are the antidote; a reviewer reading only the top of the file is
not.

**Rule ORDER within a lane is now load-bearing across two files.** A lane's
rules run before the defaults, so a generic global backstop can never shadow a
lane's specific message. For the LOCAL rules that is the whole of it:
`Rules.EvaluateWith` walks them in order and each one either denies or records
a `Finding` and continues, so reordering the global block changes which rule
name and message a user reads and nothing about allow versus deny. That is the
invariant that makes the concatenation safe to reason about, and it is scoped
to the local set. No rule type allows today; adding a terminal `allow` would
break the invariant, since a lane rule runs first and could shadow a reviewed
global deny.

**`ai_analysis` rules order differently, because they do not all deny.** Each
becomes its own `analyzer.Evaluator`, appended after the local set in
concatenation order, and its verdict is whatever its `high`/`medium`/`low` map
says: `allow`, `warn` and `defer` all forward, and only `block` denies. So
position decides two things. It decides whose message lands on a block, the
same way it does for locals. It also decides which analyzer rules run at all,
because a denial stops the Chain: a lane rule that blocks means the global
rules behind it never call a provider and never report a status, so an outage
they would have recorded is missing from a record that already says denied.

What position does NOT decide is what a policy reads. The `ai_analysis`
`Finding` is folded by `Evaluator.report` most-degraded-status first and then
highest risk, and the `risk_level` annotation merges highest-wins as a pair
with `risk_action` in `mergeAnnotations`. A rule rating a statement low cannot
erase one that rated it high, and a rule that answered cannot hide one that
could not, in either order and from either file. Each rule also holds its own
trigger, cache and `MaxCalls` budget, so moving one between the global and
lane blocks moves no spend between them.

**Slices are copied, not appended in place.** `resolve` builds a fresh slice
per lane; appending onto the shared default would let one lane's rules land in
another through a shared backing array. Covered by
`TestResolveDoesNotAliasAcrossListeners`.

**`enforce` and `mask.enabled` are `*bool` forever.** Any refactor that
"simplifies" them to `bool` silently removes a lane's ability to opt out.
`TestResolveListenerCanDisableEnforcementAgainstEnabledDefault` guards it.

**`defer` costs you an OPA deployment.** A team that wants two-phase
evaluation — report several matches, then rule on the combination — must run
OPA on the data path with `fail_open: false`, or accept that an outage
disables enforcement. ADR-0008 removed that price for the analyzer's own
risk levels, which decide in-process; it stands for a local rule's
`action: defer`, whose finding still has no reader but a Rego policy.

**We are committed to reporting every problem at once.** Validation collects
rather than fails fast, and each message names its lane and its rule. A config
error that costs one restart per typo is the thing this file is most likely to
produce.

### How this was verified

- `go test ./daemon/ -run TestResolve -v` — five cases covering concatenation
  order, aliasing, OPA replacement, enforce replacement, mask replacement.
- `TestSplitAnalyzerRulesPreservesLocalOrder` for the split that leaves the
  local set first-match, and `TestFindingFoldIsOrderIndependent`,
  `TestFindingFoldKeepsTheMostDegradedStatus`,
  `TestFindingFoldKeepsTheHighestLevel` plus `TestChainKeepsHighestRisk` and
  `TestChainKeepsRiskPairConsistent` for the half of analyzer ordering that
  concatenation cannot change.
- Two http lanes over one upstream, one inheriting and one overriding `mask`,
  compared byte for byte (above).
- `GET :19000/config` on a two-lane process: `["lane-no-delete",
  "global-no-cpf"]` versus `["global-no-cpf"]`.
- `hoop-inspect -validate` against a config exercising every OPA refusal in the
  table, which printed all five in one run.
- The three shipped configs (`envoy-stack/sidecar`, `envoy-stack/uds`,
  `metabase-stack/sidecar`) validate clean, and `appdb` resolves to two rules:
  one lane rule plus one global rule.
