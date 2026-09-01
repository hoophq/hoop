# ADR-0012: Deny by default, fail closed without OPA, and observe as a dry run

- **Status:** Accepted
- **Date:** 2026-08-31
- **Author:** @matheusfrancisco
- **Code:** [`sidecar/daemon/config.go`](../../sidecar/daemon/config.go), [`sidecar/policy/`](../../sidecar/policy), [`sidecar/gate/gate.go`](../../sidecar/gate/gate.go), [`sidecar/audit/`](../../sidecar/audit)
- **Related:** [ADR-0008](0008-analyzer-enforces-without-opa.md) (the analyzer already enforces without OPA), [ADR-0010](0010-local-sql-rule-set.md) (its "`enforce: false` is not a dry run" section is what this ADR fixes), [ADR-0011](0011-sidecar-config-schema.md) (the naming half of the same change)
- **Supersedes / Superseded by:** Supersedes the enforcement half of [ADR-0006](0006-sidecar-config-defaults-and-overrides.md)

## Context

A sidecar exists to refuse statements. Three of its defaults do the opposite,
and a fourth advertises a capability it does not deliver.

**Enforcement defaults to off.** `enforcing()` returns false for an unset
pointer (`daemon/config.go:390`), and `buildPolicy` returns a nil evaluator on
that (`:599`), before rules compile and before any OPA client exists. A config
with a broad `table` rule and no `enforce: true` starts, logs green, and denies
nothing. Five of the six configs in this repo set the field, so the repo's own
examples hide the trap rather than demonstrating it.

**`action: defer` with no OPA refuses the config.** `daemon/config.go:477-485`
for a local rule, `daemon/analyzer.go:604-611` for an `ai_analysis` risk level.
The reasoning is sound and stated at `analyzer.go:605-606`: "Deferring to a
decision that does not exist allows everything. The operator asked for the
opposite." Enforcing it by refusing to boot means one config file cannot serve
a deployment with OPA and a deployment without it, and a team wiring OPA later
writes `defer` into a file that will not load until the day OPA arrives.

**One failure knob reads backwards.** Three sections spell the same idea three
ways:

| Failure | Field | Default | Reads as |
|---|---|---|---|
| OPA unreachable | `opa.fail_open` | `false` | closed |
| classifier failed | `analyzer.fail_open` | `true` | open |
| audit write failed | `audit.fail_closed` | `false` | **open** |

The third is negated against the other two, so `false` means opposite things
one section apart. Its own doc comment calls the default "the uncomfortable
default" (`daemon/config.go:299-305`). None of the eight configs in this repo
sets it.

**Observe-only is not a dry run, and its name says otherwise.** The README
sells it as the rollout path: "run this way for a week before turning
enforcement on" (`daemon/config.go:182-185`, `sidecar/README.md:388-390`). It
evaluates nothing. `buildPolicy` returns nil, `Gate.evaluate` returns
`policy.Allow()` on its first line (`gate/gate.go:564-566`), and the audit
record carries an empty rule and an empty message
(`gate/gate.go:383-384`). An operator reading that trail after a week learns
nothing about what would have been refused. ADR-0010 already says so, in a
section titled "`enforce: false` is not a dry run".

## Options considered

1. **Delete `enforce` and always enforce.** Simplest schema, and it removes the
   dangerous default. Rejected: it also removes the rollout mode, and every
   config that omitted the field starts denying on the next restart with
   nothing to fall back to when a rule turns out too broad.
2. **Keep the field, flip nothing, document the trap harder.** No migration
   risk. Rejected: the trap is a default that silently disables the product,
   and six documentation locations already describe it correctly. More prose
   will not fix a wrong default.
3. **Rename the field, flip the default, and make the off position earn its
   keep.** `guardrails.mode` defaults to `enforce`; `observe` stops meaning
   "skip evaluation" and starts meaning "evaluate and record". Chosen.

For `defer` the choice was between two placements of the same fail-closed
posture: refuse at startup, which is stricter and already implemented, or deny
at runtime, which lets one file serve both deployments. They cannot coexist,
because the startup refusal prevents the runtime case from occurring.

## Decision

**Guardrails enforce by default.** `guardrails.mode` takes `enforce` or
`observe` and defaults to `enforce`. The string replaces the `*bool` three-state
hack: an empty value means inherit, which a zero `bool` could never express and
which ADR-0006 solved with a pointer.

**A lane with nothing to enforce relays normally.** `buildPolicy` returns nil
when the chain ends up empty (`daemon/config.go:661-663`), so `mode: enforce`
with no rules and no OPA is a pass-through rather than an error. The existing
warning at `daemon/daemon.go:336-338` covers it and gets reworded to name both
causes, since its current text tells the operator to set a field that no longer
exists.

**An unconsumed `defer` denies.** The keyword means: hand the match to a
decision-maker; with no decision-maker, deny. The startup refusals at
`daemon/config.go:477-485` and `daemon/analyzer.go:604-611` become warnings that
keep their diagnostic text.

"No consumer" means no OPA, never "no analyzer". The analyzer produces findings
and never reads local ones; only the decide-phase client reads them
(`policy/opa.go:193-197`). `anyDeferred` already makes a lane two-phase only
when `pc.OPA.enabled()` (`daemon/config.go:627-628`), so nothing else moves.

`policy.Rules` carries `DenyDeferred bool` beside the existing `FailOpen`
(`policy/policy.go:482-484`), set by `buildPolicy` when OPA is absent. The two
defer branches at `policy/policy.go:622-627` and `:646-649` deny instead of
recording.

Everything else already works without OPA and keeps working: all eight rule
types, response masking, PII detection, the audit trail, and the analyzer's own
`block` and `warn` actions, which ADR-0008 established decide in-process.

**`observe` evaluates and records.** The chain builds as usual and a wrapper
converts denials:

```go
ev, err := buildChain(...)          // unchanged
if gc.Mode == ModeObserve {
    ev = policy.Observe{Evaluator: ev}
}
```

`policy.Observe` calls through and returns
`Verdict{Denied: false, Rule: v.Rule, Message: v.Message}` with an annotation
`guardrails.would_deny`. The gate needs no change: `audit.StatementEvent` takes
`allowed`, `rule` and `message` as independent arguments
(`audit/event.go:154`), the gate already passes all three
(`gate/gate.go:383-384`), and annotations already ride onto the event metadata
(`gate/gate.go:389-396`).

```json
{"kind":"statement","allowed":true,"connection":"reporting",
 "principal":"ana@corp","operation":"update","tables":["customers"],
 "rule":"customers-is-crm-owned",
 "message":"customers is owned by the CRM; write through it",
 "metadata":{"guardrails.would_deny":"customers-is-crm-owned"}}
```

The kind stays `statement`. `audit/event.go:152-153` reserves `KindViolation`
for `allowed=false` "so a security team can select violations directly", and
statements that ran have no business in that stream.

A week of that answers the question the mode exists for:

```bash
jq -r 'select(.metadata["guardrails.would_deny"]) | .metadata["guardrails.would_deny"]' \
  audit.jsonl | sort | uniq -c | sort -rn
    412 customers-is-crm-owned
      7 no-unbounded-delete
      1 no-schema-changes
```

Observe still switches off nothing else. `ErrStreamUnsafe` denies
(`gate/gate.go:354-368`), a failed audit write denies when audit is fail-closed
(`:399-407`), and startup validation runs in full.

**`audit.fail_closed` becomes `audit.fail_open`, defaulting to `false`.** All
three failure knobs then read the same direction, and a statement whose audit
record cannot be written is refused.

**`analyzer.fail_open` keeps its `true` default.** It guards a third-party
network call on the request path, and `sidecar/README.md:465-468` has the
argument: "A classifier that denies whenever its provider has an outage takes
the database down with it." Every other evaluator in the chain is local or is a
policy engine the operator runs. After this ADR the sidecar has one stated
exception rather than one inverted name plus one exception.

## Consequences

**Configs that omitted `enforce` start denying.** That is the point of the
change and the sharpest edge in it. A rule set nobody validated because it never
fired now fires. The migration note says so in those words, and `-validate`
prints the resolved rule count per lane so an operator can read what is about
to become live.

**Renaming `audit.fail_closed` inverts polarity, and that is the most dangerous
line in either ADR.** `fail_closed: false` and `fail_open: false` mean opposite
things. `normalize` therefore reads the old field rather than pattern-matching
the new one:

| Config says | Result |
|---|---|
| `audit.fail_closed: false` | `fail_open: true`, warn. **Behaviour preserved.** |
| `audit.fail_closed: true` | `fail_open: false`, warn |
| `audit.fail_open: <x>` | used as written |
| both | config refused |
| neither | `fail_open: false`, the flip |

Only a config that omitted the field changes behaviour. Anyone who wrote it down
keeps what they had, and that property is what makes the rename survivable.
`AuditConfig.FailClosed` becomes `*bool` so the table is implementable
(`daemon/config.go:305`).

**Audit-sink availability becomes traffic availability.** All eight configs in
this repo run `audit.file: "-"`, six with `async_queue_size: 1024`. After the
flip, a blocked stdout pipe or a full queue refuses statements. Correct posture
for a system that exists to prove who did what, and a new way for traffic to
stop.

**A dry run costs what enforcement costs.** An observe lane with `ai_analysis`
rules makes model calls, and one with OPA makes round trips, because nothing can
report what would have been denied without evaluating it. Today's observe mode
is free because it does nothing. Teams that used it as a cheap "off" switch
should set `guardrails: {rules: []}` on that lane instead.

**`defer` short-circuits on a lane with no OPA.** Today a deferring rule records
and evaluation continues, so a later hard rule can still deny and Rego sees
every match (`policy/policy.go` `Action` doc). Once it denies, it stops the set,
so the first deferring rule wins. The same config run with and without OPA can
therefore report a different rule name in the denial and in the audit record.
No regression, since that config does not load today, and it belongs in the docs
because meeting it in an audit trail is confusing.

**We are committed to `mode` being a string, not a bool.** ADR-0006 pinned
`*bool` "forever" and guarded it with
`TestResolveListenerCanDisableEnforcementAgainstEnabledDefault`. That test
survives with a string; the reasoning behind it (a lane must be able to say
less) survives unchanged. A future third mode has somewhere to go.

**Observe mode gains a real definition, so it can be tested.** A test can now
assert that a denying rule on an observe lane produces `allowed: true` with the
rule name attached, which no test could express before.

## How this will be verified

- A postgres lane with `mode: observe` and a `table` rule, driven with a
  matching UPDATE: the statement reaches the upstream, and the audit line
  carries `allowed: true`, the rule name, and `guardrails.would_deny`.
- The same lane at `mode: enforce`: `kind: violation`, `allowed: false`, same
  rule name.
- A lane with `action: defer` and no `opa` block: loads, warns at startup,
  denies on match. `twophase_test.go:227-236`
  (`TestLocalDeferWithoutOPAIsRefused`) inverts, and its analyzer twin with it.
- The same rule on a lane with OPA: still records a finding, still does not
  short-circuit. `twophase_test.go:190-222` unchanged.
- A config omitting `audit.fail_closed`, with a sink wired to fail: statements
  refused. The same config with `audit.fail_closed: false` written out
  explicitly: statements allowed, deprecation warning printed.
- `TestFailOpenDefaultsTrue` (`daemon/analyzer_test.go:240-247`) unchanged, so
  the analyzer exception stays deliberate rather than drifting.
- `hoop-inspect -validate` on all six shipped configs after the migration,
  reporting the resolved rule count per lane.
