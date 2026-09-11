# ADR-0015: The analyzer is a listener component, not a guardrail rule

- **Status:** Proposed
- **Date:** 2026-09-11
- **Author:** @matheusfrancisco
- **Code:** [`sidecar/daemon/analyzer.go`](../../sidecar/daemon/analyzer.go), [`sidecar/daemon/config.go`](../../sidecar/daemon/config.go), [`sidecar/daemon/reload.go`](../../sidecar/daemon/reload.go)
- **Related:** [ADR-0008](0008-analyzer-enforces-without-opa.md) (the analyzer decides for itself; every row of its action table survives this ADR), [ADR-0011](0011-sidecar-config-schema.md) (the guardrails/opa split this extends to the analyzer), [ADR-0006](0006-sidecar-config-defaults-and-overrides.md) (the merge table the new block joins), [ADR-0014](0014-sidecar-config-hot-reload.md) (the reload boundary the block sits inside)
- **Supersedes / Superseded by:** amends the config surface of ADR-0008; the decision semantics there stand.

## Context

The AI analyzer shipped spelled as a guardrail rule: `type: ai_analysis`
under `guardrails.rules`, beside `deny_words_list` and `pattern_match`. The
spelling was never true. A guardrail rule is a local matcher — microseconds,
no network, denies or defers on match through one `action`. The analyzer
leaves the process, costs money per statement, can take a second, and maps
`high`/`medium`/`low` onto actions instead of carrying one. The code always
knew: `splitAnalyzerRules` lifted these rules out before `policy.NewRules`
could see them, `Rules` rejected one that reached it anyway, and prompt
precedence already read rule → analyzer section → built-in, which is
component behaviour wearing a rule's clothes.

The costume had a price in every layer that touched it:

- Startup carried refusals that only exist because a component was forced
  into a rule shape: `action:` on an `ai_analysis` rule (the field is read
  by nobody), `opa.gate: true` with no `ai_analysis` rule, an `ai_analysis`
  rule with no `analyzer` section.
- The config could not say where the analyzer runs. The root had
  `analyzer:` (provider, send, cache, budget) but a listener had no analyzer
  block, so per-lane variation squeezed through rule fields while the
  tunables an operator actually varies per lane — `send`, `fail_open`,
  `max_calls` — were stuck process-wide.
- `-validate` reported the component as a rule count (`+ 1 ai rule(s)`),
  and the docs taught the analyzer through the guardrails page.

The feature's intended role is a per-lane decision engine over the wire, a
peer of guardrails and masking that can one day apply both at runtime. A
rule shape blocks that: peers get blocks, subtypes get rows in someone
else's list.

## Options considered

1. **Keep the rule shape and document it harder.** Zero migration cost.
   Rejected: every startup refusal above stays load-bearing, per-lane
   tunables stay impossible, and the next capability (the analyzer applying
   guardrails the user never wrote) has no place to hang config.
2. **A listener `analyzer:` block, and normalize() folds old rules into
   it.** One canonical representation in memory. Rejected on faithfulness: a
   lane carrying two `ai_analysis` rules (different triggers, different
   prompts) cannot fold into one block, and a top-level rule reaches every
   lane while a block is per listener. A fold that is sometimes lossy is
   worse than no fold.
3. **A listener `analyzer:` block beside the rule form, both building
   through one path.** The block is canonical; the rule converts to the
   block's shape (`specFromRule`) and builds through the same
   `buildAnalyzerEvaluator`, so the two spellings cannot drift. The rule
   form warns at load and keeps working. Chosen.

## Decision

Each listener takes its own `analyzer:` block — trigger, `high`/`medium`/
`low`, `prompt`, `message` — plus per-lane overrides of the root defaults:
`send`, `fail_open`, `timeout_sec`, `max_input_bytes`, `max_calls`, `cache`.
A zero field inherits the root value, so the block overrides exactly what it
names.

The root `analyzer:` section keeps what is genuinely process-wide — the
provider, the model, the endpoint, the credential, and one credential read —
and becomes the defaults every lane inherits. Provider settings are NOT per
lane: a second provider per lane doubles the credential surface for a case
nobody has asked for, which is the same line ADR-0008 drew.

`type: ai_analysis` still loads, still enforces, and records a deprecation
through the same `Config.Deprecations` funnel every other renamed field uses
(`-strict` turns it into a non-zero exit). Both spellings can serve one lane
during a migration, each as its own evaluator: block first, then the rules
in concatenation order, all after the local rules and the gate/single-call
OPA position.

Identity follows the shape. A block's evaluator is named after the LANE —
`Finding.Rule`, the `ai_rule` audit key and the `max_calls` budget all carry
it — because a component has no rule name and the listener name is the
identity an operator edits. The field NAMES in findings, audit metadata and
`/stats` do not change, so dashboards and Rego keyed on
`findings.ai_analysis` keep working unmodified.

The block sits inside the ADR-0014 hot-reload boundary: it builds
evaluators, not sockets, so editing it swaps like a rule edit
(`laneRuleDoc` carries it, `nonRuleDoc` strips it), while the root section
stays restart-bound with the provider credential it holds. Budgets key on
the evaluator name, so a reloaded block continues its lane's running count.

`-validate` reports the component: `+ ai analyzer`, with
`(N deprecated ai rule(s))` counting what is left to migrate on that lane.

Nothing in ADR-0008 moves. The action table, `defer`'s degradation to
`block` on OPA-less lanes, `require_review`'s refusal, fail-open-by-default
and the trigger-as-cost-control all apply to the block verbatim, because the
block and the rule build the same evaluator.

## Consequences

**The refusal table reads the same, one shape earlier.** A block with no
trigger on an ungated lane, no action for any level, or no root `analyzer`
section refuses at startup with the same reasoning the rule form had — those
were never symptoms of the rule shape, only phrased as if they were. The one
refusal that WAS a costume artifact, `action:` on an `ai_analysis` rule,
survives only on the deprecated form and dies with it.

**Two spellings exist until the rule form is removed.** That is the price of
"the old version must not stop working", and it is bounded: both compile to
one evaluator through one builder, validation shares `validateRiskActions`,
and the deprecation warning names the replacement per rule, per lane. The
removal release deletes `specFromRule`, `splitAnalyzerRules` and the
`ai_analysis` arms of validation, and nothing else.

**`ai_rule` changes value, not name, on migration.** A migrated lane reports
the listener name where it reported the rule name. Anyone keying dashboards
on the VALUE (not the field) sees the switch; the migration section in the
sidecar README says so out loud. Findings were already keyed by source
(`ai_analysis`), which does not move.

**A lane with several ai rules has no block equivalent yet.** Different
triggers with different prompts on one lane stay on the deprecated form
until the block grows a list or the lane splits. `-validate` counts them per
lane, so the leftover is visible rather than folklore.

**The seam for "the analyzer applies guardrails" now exists.** A per-lane
component block is where a future `apply: {guardrails: ..., mask: ...}`
hangs without touching the rule set. That capability is unwritten and out of
scope here; this ADR only guarantees it has an address.

### How this was verified

Against `sidecar/` at 2026-09-11, with the stub provider and no OPA
anywhere. `go test ./...` passes across the module; `daemon/laneanalyzer_test.go`
pins the contract:

- A postgres lane with `analyzer: {trigger: {operations: [delete]}, high: block}`
  validates, builds, and `-validate` reports `+ ai analyzer` with
  `LaneInfo.Analyzer` set and no rule count.
- The block without a root `analyzer` section, without a trigger on an
  ungated lane, without any risk action, with `high: require_review`, with
  an unknown action, an unknown `send`, or any negative numeric: each
  refused, naming the lane.
- Prompt precedence block → root → built-in, and the output contract
  survives all three.
- `max_calls: 1` on a block bounds the lane across evaluator rebuilds (one
  purse per lane name; a second lane spends its own).
- `high: defer` on the block builds the two-phase chain with the decide
  OPA last, same as the rule form.
- A config carrying `type: ai_analysis` validates, works, and lists a
  deprecation naming the `analyzer` block; a lane carrying both spellings
  builds both evaluators and reports both in its summary.
- Editing only the block leaves `nonRuleDoc` byte-identical and changes
  `laneRuleDoc`, which is the hot-swap condition.
