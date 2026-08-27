# ADR-0008: The AI analyzer decides for itself; OPA is the opt-in second decider

- **Status:** Accepted
- **Date:** 2026-08-27
- **Author:** @matheusfrancisco
- **Code:** [`sidecar/analyzer/`](../../sidecar/analyzer), [`sidecar/daemon/config.go`](../../sidecar/daemon/config.go), [`sidecar/daemon/analyzer.go`](../../sidecar/daemon/analyzer.go), [`sidecar/policy/`](../../sidecar/policy)
- **Related:** [ADR-0005](0005-sidecar-flow.md) (request flow, current state), [ADR-0006](0006-sidecar-config-defaults-and-overrides.md) (the refusal table this ADR explains the other half of), [ADR-0009](0009-guardrails-and-masking-architecture.md) (the enforcement points this chain sits in)
- **Supersedes / Superseded by:** —

## Context

The analyzer is the third evaluator in a lane's chain, and the package that
introduced `EvalContext`, `Finding` and the two-phase OPA call. Its own doc
comment argues the separation: a producer establishes a fact, a policy decides
what the fact means, and the two should not be the same component, because the
ruling depends on the actor, the hour and the table.

Read straight, that argument ends with the model as a pure producer. It
classifies, writes `findings.ai_analysis.risk_level`, and a decide-phase Rego
policy rules on it. All the machinery for that exists: `input.phase`,
`input.findings`, `EvalContext.Requested`, `policy.PhaseGate` /
`policy.PhaseDecide`.

Ending there has a price nobody agreed to pay. A team running one sidecar next
to one database would have to deploy and operate OPA, and write Rego, before a
model call could refuse anything — to express a three-row table mapping high,
medium and low onto allow, warn and block. The same team would then have their
AI policy inherit OPA's failure modes: `fail_open: false` and an OPA outage
stops the lane, `fail_open: true` and the outage disables the control, and
neither has anything to do with whether the model answered.

Against that, the case for OPA is real and narrow. It is combination logic:
"the scanner found `US_SSN` **and** the statement writes to `customers`",
"the model could not answer **and** this touches protected data". Those read
several findings at once, they belong to InfoSec, and nothing in a per-rule
action table can express them. ADR-0006 records where that lands as a startup
refusal; this ADR records the reasoning that put the rest of the analyzer on
the other side of that line.

## Options considered

1. **The analyzer is a pure producer.** It classifies and reports; every
   allow/deny comes from a decider behind it. One decision point per lane, one
   language to audit, and the cleanest version of the producer/policy split.
   Rejected: it makes a paid model call worthless without a second service, and
   it puts a three-row lookup table in Rego.
2. **The analyzer is the only decider.** Drop `defer`, keep `high|medium|low:
   block|warn|allow`, and let OPA stay a single-call evaluator ahead of it.
   Simple, and enough for most lanes. Rejected: it deletes the case OPA is
   genuinely good at. A risk level is only half a decision when the other half
   is which table was touched and who is asking.
3. **Both, with the split stated per risk level.** The mapping is per rule and
   runs in-process; `defer` is the one value that names an external decider,
   and a lane that defers to nothing is refused at startup. Chosen.

## Decision

We will keep the risk-to-action mapping on the rule and execute it in the
analyzer. A lane with no `policy.opa.url` enforces AI verdicts.

`buildPolicy` appends an OPA client only when the resolved lane has one, so a
lane without one resolves to `[policy.Rules, analyzer.Evaluator…]` and nothing
else. Each `ai_analysis` rule becomes its own `analyzer.Evaluator`, sharing the
provider and the credential but carrying its own trigger, action map, message
and prompt.

| `high` / `medium` / `low` | Who decides | Needs OPA |
|---|---|---|
| `block` | the analyzer, in-process | no |
| `warn` | the analyzer: forwards, annotates `risk_level` + `risk_action` | no |
| `allow` (also the default for an unnamed level) | the analyzer | no |
| `defer` | a decide-phase policy reading `input.findings.ai_analysis` | **yes** |
| `require_review` | nothing in this build | refused outright |

The rest of the analyzer's behaviour is local by the same reasoning, and each
piece is a control an operator would otherwise lose by not running OPA:

- **The trigger is the cost control.** An empty trigger classifies nothing, so
  a rule states what it cares about. `policy.opa.gate` moves that question into
  Rego for lanes that want it; it is an alternative to the trigger, never a
  prerequisite.
- **`send: refuse` denies locally.** Content carrying a detected entity is not
  transmitted, and the statement is refused in-process. Allowing it would leak
  nothing and would also mean the operator's `refuse` did nothing.
- **`fail_open` decides classification failures locally**, and defaults to
  **true** — the opposite of every other evaluator here. A classifier that
  takes the database down during a provider outage is worse than no classifier.
- **The verdict cache and `max_calls` budget** are per process. A spent budget
  reports `ai_status: budget_exhausted` and allows, which is the same outcome
  as a lane with no analyzer.

`require_review` stays in the `Action` enum and is refused in two places
(`analyzer.New`, and validation with the lane and rule named). The enum keeps
the schema stable for when a review backend exists; the refusal is there
because a config that looks like it holds statements for approval and quietly
forwards them is worse than one that will not start.

## Consequences

**AI blocking is reachable with no OPA and no Rego.** The whole of it:

```yaml
analyzer: {provider: anthropic, model: claude-sonnet-4-5, credentials_file: /etc/hoop/key}
policy:
  enforce: true
  rules:
    - name: risky-writes
      type: ai_analysis
      trigger: {operations: [update, delete, insert]}
      high: block
      medium: warn
```

**Two cost controls exist and only one is OPA-free.** `trigger:` is local;
`opa.gate` is a round trip per statement and needs an endpoint. An operator who
wants the gate's expressiveness without the service has nothing today, and the
honest answer is "widen or narrow the trigger". Closing that hole means an
in-process decider reading `input.findings`, which is unwritten and needs its
own ADR.

**Without OPA, a provider outage allows by default.** `fail_open` defaults true
and is per process, so a lane that must not forward an unclassified statement
sets `fail_open: false` and accepts that the provider's availability is now the
lane's. There is no third mode, and adding one is not on the table: the
`ai_status` annotation already distinguishes `ok`, `cached`, `skipped`,
`error`, `budget_exhausted` and `refused` in the audit trail, which is where a
"how often would this have blocked" question gets answered.

**Conjunctions still cost an OPA deployment.** An operator who wants "high risk
**and** writes to `customers`" cannot say it in the action map, and the
pressure will be to widen the trigger until the model sees everything, which is
a bill rather than a control. Watch for that shape in configs; it is the signal
that the native engine's phase 1 matters more than its phase 3.

**We are committed to the analyzer never needing an evaluator behind it.** Any
action added to the enum states which decider it requires and is refused at
startup when that decider is absent, per level, per rule, with the lane named.
`require_review` is the precedent, and a `defer` on a lane with no
`policy.opa.url` is refused for the same reason: deferring to a decision that
does not exist allows everything.

**If the native decision engine lands, one row changes.** `defer` becomes
legal against `decision.rules` as well as `policy.opa.url`. Nothing else in
this ADR moves, because a native decider is one more consumer of the same
`Finding` the analyzer already writes — which is the test of whether the seam
was drawn in the right place.

### How this was verified

Against `sidecar/` at 2026-08-27, with a stub provider and no OPA anywhere.

- `hoop-inspect -validate` on a postgres lane with three local rules and one
  `ai_analysis` rule at `high: block`, and no `policy.opa.url`:
  `config OK: 1 listener(s) / appdb postgres enforcing 3 rule(s) + 1 ai rule(s)`.
- The same config with `high: defer`, with `high: require_review`, and with
  `action: defer` on a local rule: each refused at startup, each message naming
  the lane, the rule and the level.
- A chain built as `policy.Chain{rules, analyzer}` — no `OPAClient` in it —
  evaluated one statement per rule type. Denied: `operation`,
  `deny_words_list`, `pattern_match`, `table` (write access), `pii`,
  `http_resource`, `http_status` (5xx), and `ai_analysis` at high risk, which
  reported `risk_level=high risk_action=block ai_status=ok`. Allowed: the same
  table rule against a read, a low-risk classification
  (`risk_action=allow`), an untriggered statement (`ai_status=skipped`), and a
  200 response.
- `go test ./policy/... ./analyzer/... ./daemon/... ./gate/...` passes, and
  `daemon/twophase_test.go` pins the chain shapes this ADR describes:
  OPA before the analyzer on a single-call lane, the decide phase last on a
  deferring one.
