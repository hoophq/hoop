# ADR-0019: A decision model beside the LLM analyzer: calibrated risk, a question battery, and several analyzers per lane

- **Status:** Proposed
- **Date:** 2026-09-21
- **Author:** @matheusfrancisco
- **Code:** [`sidecar/analyzer/`](../../sidecar/analyzer), [`sidecar/analyzer/typesafe/`](../../sidecar/analyzer/typesafe) (new), [`sidecar/daemon/analyzer.go`](../../sidecar/daemon/analyzer.go), [`sidecar/daemon/config.go`](../../sidecar/daemon/config.go), [`sidecar/policy/policy.go`](../../sidecar/policy/policy.go) (`mergeAnnotations`)
- **Related:** [ADR-0008](0008-analyzer-enforces-without-opa.md) (the analyzer decides for itself; every row of its action table survives this ADR), [ADR-0015](0015-listener-analyzer-block.md) (the listener block this ADR turns into a list, the one-provider rule it relaxes at the root only, and the "analyzer applies guardrails" seam it reserved), [ADR-0014](0014-sidecar-config-hot-reload.md) (entries reload; the root `providers:` map does not), [ADR-0009](0009-guardrails-and-masking-architecture.md) (the enforcement points; the masking half is deliberately NOT touched here), [ADR-0006](0006-sidecar-config-defaults-and-overrides.md) (the refusal table that grows eleven rows)
- **Supersedes / Superseded by:** amends the provider-per-process rule and the single-block lane form of ADR-0015; the decision semantics there stand.
- **External:** [TypeSafe API reference](https://docs.typesafe.ai/api), [Confidence](https://docs.typesafe.ai/confidence), [Guardrails cookbook](https://docs.typesafe.ai/cookbooks/llm_guardrails), [Models](https://docs.typesafe.ai/models)

## Context

The analyzer is the one evaluator in a lane's chain that leaves the process.
Everything in `sidecar/analyzer` exists to make that affordable and safe: a
trigger narrows what is worth asking about, a cache collapses statement
shapes, a budget bounds the bill, `send: redacted|refuse` keeps detected
entities off the wire, and a prompt contract keeps the model from quoting a
statement value back into the audit trail.

Every provider it has today (`anthropic`, `openai`, `vertex`) is a text
generation model coerced into an enum. `prompt.go` states the coercion
plainly: the model is offered three tools named after the risk levels and told
to call exactly one, and "the name IS the risk level". Three things follow
from that design, and each is a cost the package pays on purpose because
there was no alternative:

1. **There is no confidence.** `RiskLevel`'s doc comment: "Three levels, not
   a score. The classification comes from which tool the model chose to
   call, so there is no confidence number underneath to expose, and
   inventing one would imply a precision the method does not have." An
   operator cannot say "hold anything the model is unsure about", because
   the model never says how sure it is. `high: require_review` holds every
   high, including the ones a human will wave through in two seconds.
2. **The output is prose, so the output is a leak channel.** `Result.Title`
   reaches the end user in the protocol's error frame; `Explanation` is
   model reasoning. Both are kept OUT of `Event.Metadata` because audit
   redaction never reaches metadata and "a model that quotes the statement
   back would write the value into the record verbatim". The
   `promptContract` — never quote a literal, call one tool — is appended to
   every prompt and cannot be removed by config for exactly this reason.
   It is a mitigation, not a guarantee: the model is asked not to leak.
3. **One question per call.** A lane gets one classification: how risky is
   this statement. "Does it delete without a selective WHERE", "does it read
   an unbounded set of rows from a table holding personal data", "is this a
   schema change" are all folded into one three-way verdict and one block of
   guidance prose. An operator who wants those answered separately has no
   place to write them, and asking an LLM several things at once degrades
   each answer.

TypeSafe's Jev is a different kind of model. It is trained to return
calibrated decisions, not text: a request carries a `state` and a map of
typed questions, and the response carries one typed answer per question. A
**Choice** returns the chosen option, a probability for every option and a
`confidence` derived from that distribution. A **Noul** returns the
probability that a yes/no statement is true. A **Score** returns a
probability-weighted position on an ordered rubric. Every question in a
request is evaluated in parallel against the same state, so adding questions
barely moves latency, and there is no generated text anywhere in the
response. The API is one JSON POST with a bearer token
(`POST https://api.typesafe.ai/v1/systemone`), priced on input tokens only
($0.042/Mtok for `jev-1.13.0`), with a 32k-token cap on state plus the
longest question. Rate-limit errors are `429`/`529` with `retry-after`.

Set against the three costs above, that is not one more LLM vendor. It is
the shape `analyzer/` was already trying to build by hand:

- The Choice `{low, medium, high}` IS `RiskLevel`, with the probabilities
  and confidence the current design says it cannot have.
- A model that cannot emit prose cannot quote a literal. The leak channel
  the contract mitigates is closed by construction.
- N Nouls in one call is the "guardrails the operator never wrote" that
  ADR-0015 reserved a seam for.

The forces against are also real. Adding a provider whose answer shape
differs from the others means the `Provider` contract can no longer be
"return a level and a title". Confidence thresholds are tuned per model
version and move when the alias moves. A battery is more config surface,
more refusals, more to document. And Jev is one vendor with one model; the
package must not become TypeSafe-shaped in a way that strands the three LLM
providers or a future one.

One more force, and it is the one that decides how this lands rather than
whether. Nobody switches the model that refuses production writes on the
strength of a docs page. The path from "we use Claude" to "we use Jev" runs
through weeks of **both answering every statement on the same lane**, with
one deciding and the other recorded, and a number at the end that says how
often they disagreed and who was right. After that, the steady state for
many lanes is still both: an LLM for the risk judgment where prose guidance
carries the nuance, a decision model for the cheap, fast, calibrated
battery. The config cannot say any of this today. ADR-0015 fixed ONE
provider per process — "a second provider per lane doubles the credential
surface for a case nobody has asked for" — and a lane's `analyzer:` is one
block. Two evaluators on a lane exist only through the deprecated
`type: ai_analysis` rule form, both on the same provider, and ADR-0015
lists "a lane with several ai rules has no block equivalent yet" as an
open consequence.

Two evaluators sharing a lane also expose a wart in the trail. `risk_level`
and `risk_action` merge as a highest-wins pair (`policy.mergeAnnotations`),
but `ai_rule` merges last-writer-wins, so a row can carry one evaluator's
level beside the other evaluator's name.

## Options considered

1. **One more `Provider`, no contract change.** `analyzer/typesafe`
   implements `Classify(ctx, systemPrompt, content)` by sending one Choice
   question, mapping `choice` to `RiskLevel`, and returning an empty
   `Title`. Zero risk to the evaluator; every existing test holds. Rejected
   as the END state: it drops `probabilities` and `confidence` on the floor
   and cannot ask a second question, so the two properties that make the
   model worth adding are unreachable from config. It also sends the
   tool-calling contract as ~150 tokens of noise per call. Kept as **phase
   1**, because it ships in a day and proves the wire.
2. **Make the whole contract question-shaped.** Replace `Classify` with
   `Ask(state, questions) (answers)` and have the LLM providers emulate a
   battery through tool calling or JSON mode. One interface, one path.
   Rejected: it forces the three LLM providers to fake calibrated
   probabilities they do not have, which is worse than not exposing one
   (the `RiskLevel` comment's argument is right for THEM), and it rewrites
   three working providers and their tests to add a fourth.
3. **Two capability interfaces, path chosen at construction; confidence
   routing and a Noul battery as per-lane config that is refused on a
   provider that cannot serve it.** `Classifier` is what the three LLM
   providers already satisfy structurally, untouched. `Asker` is what a
   decision model implements. `analyzer.New` picks the path once. Config
   that needs answers only an `Asker` can give (`min_confidence`,
   `questions`) is refused at startup on a `Classifier`-only provider, with
   the lane and the provider named. Chosen.
4. **Also drive response masking from the model** ("does this column hold
   PII"). Deferred to its own ADR. It lands on the response path, which
   today never leaves the process, and it picks between real alternatives
   (classify column names only vs. values; per-row vs. per-result-shape
   caching; what to do below the confidence floor). Folding it in here
   would hide those choices under a provider PR. See Consequences.
5. **One provider per process, as ADR-0015 fixed it; migration by
   swapping.** Zero config change. Rejected: it makes the migration a leap
   — turn Claude off, turn Jev on, watch the trail — with no way to
   compare verdicts on the same statement, and it forbids the steady state
   where the two do different jobs on one lane.
6. **Named providers at the root, a list of entries per lane, and a
   `shadow` mode that records without deciding.** The credential surface
   stays at the root (one read per named provider at startup, none per
   lane), which is the property ADR-0015 was protecting. Each entry is its
   own evaluator with its own trigger, action map, battery, budget and
   provider; the chain runs them in list order. A shadow entry writes a
   prefixed annotation set and never a verdict or a finding, so Rego and
   the session rollup cannot mistake a rehearsal for a decision. Chosen,
   and it retires the last reason the deprecated rule form had to exist.

## Decision

We will add TypeSafe's Jev as an analyzer provider and extend the analyzer,
in one place, to consume the two things a decision model returns and an LLM
does not: a **confidence** on the risk level, and **several independent
answers per call**. The three LLM providers keep working unchanged, and a
lane configured against one of them cannot silently lose a control it wrote
for a decision model. And we will let a lane run **more than one analyzer,
on more than one provider**, so the new model can be rehearsed beside the
old one and, where that is the right shape, kept beside it.

### The provider

`sidecar/analyzer/typesafe/typesafe.go`, in the root module. It is stdlib
`net/http` over one JSON POST, the same footing as `analyzer/anthropic` and
`analyzer/openai`; only `vertex` needs a nested module, for OAuth2.
Registered under the vendor name, `typesafe`, matching `anthropic` /
`openai` / `vertex`. The model is `model:` in the root `analyzer` section as
today; docs recommend the versioned id (`jev-1.13.0`) over the alias.

The provider follows the rules the anthropic package already states and
this ADR restates as binding for every provider:

- **A non-2xx never surfaces the body.** An error body may echo the request
  and therefore the statement.
- **`429` and `529` get one bounded retry inside `ctx`, honoring
  `retry-after`.** The deadline is the evaluator's `timeout`, and a retry
  that would cross it is not attempted.
- **The response's `model` field is logged once at startup and on
  change.** An alias moves under an unchanged config; thresholds tuned
  against `jev-1.13.0` must not start ruling under a different model
  without a line in the log. This is the same reason `anthropic.apiVersion`
  is pinned.
- **`credentials_file` is required.** There is no ambient credential for
  this vendor; a missing one is refused at startup like a missing
  `model`.

### The contract split

```go
// analyzer.go
type Provider interface { Name() string }

// What the three LLM providers implement today. Unchanged signature.
type Classifier interface {
    Provider
    Classify(ctx context.Context, systemPrompt, content string) (*Result, error)
}

// What a decision model implements.
type Asker interface {
    Provider
    Ask(ctx context.Context, state string, b Battery) (Answers, error)
}
```

`analyzer.New` refuses a provider that implements neither. It chooses the
path at construction and never per statement:

| Path | `prompt` / `promptKey` | Output contract appended | Result carries |
|---|---|---|---|
| `Classifier` | `BuildSystemPrompt(Guidance)` | yes, always (unchanged) | `RiskLevel`, `Title`, `Explanation` |
| `Asker` | fingerprint of the serialized `Battery` | **no** — there is no prose to constrain | `RiskLevel`, `Confidence`, `Probabilities`, `Answers` |

The contract stays mandatory for prose-producing providers; a reworded
question invalidates the cache the way a reworded prompt does, through the
same `promptKey` mechanism.

`Result` grows three optional fields. An LLM provider leaves them zero,
and a zero `Confidence` means "not measured", never "certain".

### The battery

Every `Asker` lane sends one request per classified statement, carrying:

- **`risk`** — a Choice, always present. `instructions` is the lane's
  `Guidance` (or `PromptGuidance`), `criteria` are the three level
  descriptions lifted out of the current guidance prose into structured
  form. `choice` → `RiskLevel`; `confidence` → `Result.Confidence`.
- **`questions.<id>`** — zero or more Nouls the operator writes.

```yaml
analyzer:
  provider: typesafe
  model: jev-1.13.0
  credentials_file: /etc/hoop/typesafe.key
  send: redacted
  timeout_sec: 3            # a decision model answers in well under a second

listeners:
  - name: appdb
    protocol: postgres
    analyzer:
      trigger: {operations: [delete, update, select]}
      high: block
      medium: warn
      low: allow
      min_confidence: 0.6              # below this the level is not trusted...
      uncertain: require_review        # ...and this action runs instead
      questions:
        unbounded_write:
          ask: "Does this statement update or delete rows without a selective WHERE clause?"
          yes: "No WHERE clause, or a WHERE that matches most of the table."
          no:  "A keyed lookup or a narrow filter."
          action: block
          threshold: 0.8
          review_threshold: 0.4        # [0.4, 0.8) holds for a human
        bulk_export:
          ask: "Does this read an unbounded set of rows from a table that holds personal data?"
          action: defer                # Rego reads the probability and decides
          threshold: 0.7
        schema_change:
          ask: "Does this alter, drop or create a table, index or column?"
          action: require_review
          threshold: 0.9
```

The `state` is `content.Text` as the protocol's `Builder` already renders
it — the same bytes an LLM provider receives, after the same `Redact` pass,
under the same `max_input_bytes`. The battery changes what is ASKED, never
what is SENT.

### Routing is code, not model

TypeSafe's own guidance is that the model supplies the assessment and the
application owns the decision. The analyzer already does: `ActionMap` is a
lookup table. It grows two rows and a fold, all in `evaluator.go`:

| Answer | Condition | Action |
|---|---|---|
| `risk` | `confidence ≥ min_confidence` (or `min_confidence` unset) | `Actions[level]` — unchanged ADR-0008 table |
| `risk` | `confidence < min_confidence` | `Actions[uncertain]` |
| `questions.<id>` | `p ≥ threshold` | that question's `action` |
| `questions.<id>` | `review_threshold ≤ p < threshold` | `require_review` |
| `questions.<id>` | otherwise | nothing |

Several answers can fire on one statement. The fold is by precedence,
`block > require_review > defer > warn > allow`, so a Noul that blocks is
not outvoted by a risk level that warns. `Action` gains a `rank()` the way
`RiskLevel` has one. The trail records what fired, not just what won.

Everything downstream of the action is untouched: `block` denies through
`policy.Deny` with `SourceAnalyzer`; `require_review` enters `hold()` and
files the RAW statement exactly as `high: require_review` does today;
`defer` publishes and forwards; `warn` and `allow` forward.

### What the trail and OPA see

`report()` publishes under the existing `Source` (`ai_analysis`) — no new
source name, so every dashboard and Rego policy keyed on
`findings.ai_analysis` keeps working — with a wider `values`:

```json
{"risk_level": "medium", "confidence": 0.58,
 "questions": {"unbounded_write": 0.91, "bulk_export": 0.12, "schema_change": 0.02}}
```

A decide-phase policy can now write
`input.findings.ai_analysis.values.confidence < 0.6` or
`input.findings.ai_analysis.values.questions.bulk_export > 0.7 and
"customers" in input.statement.tables` — the conjunctions ADR-0008 said
belong in Rego, now over numbers rather than a single enum. The fold across
two evaluators on one lane keeps the highest level and the LOWEST
confidence, because a second rule's certainty cannot vouch for the first's
doubt.

Two annotation keys join the fixed vocabulary in `analyzer.go`:
`ai_confidence` (two decimals) and `ai_fired` (comma-joined question ids
whose threshold was met). Both pass the standing rule for `Event.Metadata`
— a number and operator-chosen identifiers, never model prose, never a
statement value. The denial message, absent `message:`, becomes
`refused by risk analysis: unbounded_write`, which is safe on the wire for
the same reason.

### Several analyzers on one lane

**Named providers at the root.** The root `analyzer:` section keeps its
fields and stays the default every lane inherits. It gains `providers:`, a
map of additional named provider configs, each with the same four
provider-only fields (`provider`, `model`, `endpoint`, `credentials_file`,
plus `extra`). Every credential is still read once, at startup, at the
root, so the surface ADR-0015 protected does not grow per lane; a hot
reload that edits `providers:` is restart-bound like the root provider is
today (ADR-0014).

**A lane's `analyzer:` is a block or a list of blocks.** A block is the
ADR-0015 form and means what it meant. A list holds entries, each of which
is the block form plus two fields: `name` (required in a list, an
identifier) and `provider` (a key from `providers:`, or omitted for the
root default). Each entry is its own `analyzer.Evaluator` — own trigger,
own action map, own battery, own `send`/`fail_open`/`timeout`/`cache`,
own `max_calls` purse — and the chain runs them in list order after the
local rules and the gate/single-call OPA position, the slot the block and
the deprecated rule form already occupy. A single block's evaluator is
still named after the lane; a list entry's is named `<lane>/<name>`, which
is what `Finding.Rule`, `ai_rule`, `/stats` and the budget registry carry.

```yaml
analyzer:
  provider: anthropic                 # the default, as today
  model: claude-sonnet-4-5@20250929
  credentials_file: /etc/hoop/anthropic.key
  send: redacted
  providers:
    jev:
      provider: typesafe
      model: jev-1.13.0
      credentials_file: /etc/hoop/typesafe.key

listeners:
  - name: appdb
    protocol: postgres
    analyzer:
      - name: risk                    # what decides today, unchanged
        trigger: {operations: [delete, update]}
        high: block
        medium: warn

      - name: jev-trial               # what we are evaluating: records, never decides
        provider: jev
        mode: shadow
        trigger: {operations: [delete, update]}
        high: block                   # what it WOULD do; shadow turns it into a record
        min_confidence: 0.6
        uncertain: require_review
        questions:
          unbounded_write: {ask: "...", action: block, threshold: 0.8}

      - name: guard                   # what Jev is already trusted with: cheap semantic guardrails
        provider: jev
        timeout_sec: 2
        questions:
          schema_change: {ask: "...", action: require_review, threshold: 0.9}
          bulk_export:   {ask: "...", action: defer, threshold: 0.7}
```

Three entries, two providers, three jobs: the incumbent decides risk, the
candidate rehearses the same decision, and the decision model already
enforces the battery the LLM was never good at. Removing `jev-trial` or
swapping `provider: jev` onto `risk` is the migration, one line each.

**Shadow mode.** `mode: shadow` on an entry means: classify exactly as
configured, decide nothing. It differs from `warn` in what it writes. An
entry set to all-`warn` is a real evaluator observing one tier — its level
merges into `risk_level`, its finding folds into `ai_analysis`, and a
session's rollup rises with it. A shadow entry must do none of that,
because a rehearsal that raises the lane's risk level or feeds a
decide-phase Rego policy is deciding after all. So a shadow evaluator:

- writes its whole annotation set under a `shadow.` prefix —
  `shadow.ai_rule`, `shadow.risk_level`, `shadow.risk_action` (what it
  WOULD have done), `shadow.ai_status`, `shadow.ai_confidence`,
  `shadow.ai_fired` — following the `guardrails.would_deny` precedent for
  a dry-run key that must not collide with the real one;
- reports **no finding**: `input.findings.ai_analysis` is the real
  producers' and only theirs;
- files **no review** and spends its own `max_calls` purse, never another
  entry's;
- writes `shadow.disagreement` when the lane's real `ai_analysis` finding
  answered with a different level: the value is `<rule>=<level>,<rule>=<level>`
  — evaluator names and levels, never prose — and `/stats` for the entry
  counts `Compared`, `Agreed`, `WouldDeny`. That is the number the trial
  exists to produce: how often the two disagreed, and which rows to read
  to learn who was right.

A shadow entry reads the real finding, so it must run after the entries it
is shadowing. The builder orders shadow entries last regardless of list
position and `-validate` says so; an operator who wants Jev to shadow only
the `risk` entry puts them on the same trigger.

**Two enforcing entries fold as two evaluators always have.** The chain
short-circuits on the first denial, so the cheaper entry goes first and a
statement it refuses never costs the second a call. Findings fold under
`ai_analysis` by the existing rule — most degraded status wins, then
highest level, and with this ADR lowest confidence. `ai_rule` joins the
`risk_level`/`risk_action` pair in `mergeAnnotations` as a triple, so a row
carries the level, the action and the NAME of the evaluator that produced
them; the last-writer-wins wart above is fixed as part of this, and it is a
bug fix regardless of whether a lane ever runs two providers.

**The deprecated rule form loses its last job.** ADR-0015 kept
`type: ai_analysis` alive because "a lane with several ai rules has no
block equivalent yet". It has one now. The rule form's removal release is
unblocked; this ADR does not schedule it.

### Startup refusals (extending ADR-0006's table)

| Config | Refused because |
|---|---|
| `min_confidence` or `questions` on a `Classifier`-only provider | the control would silently never run; ADR-0008's rule that every action states its decider applies to every ROUTE too |
| `min_confidence` without `uncertain`, or `uncertain` without `min_confidence` | half a rule: a floor with no consequence, or a consequence with no floor |
| a question with `review_threshold ≥ threshold`, or a threshold outside `(0, 1]` | the review band is empty or the action can never fire |
| a question id that is not `[a-z0-9_]+` | ids reach the wire in the denial and the trail in `ai_fired`; keep them identifiers |
| `uncertain: defer` or a question `action: defer` on a lane with no decide-phase OPA | the ADR-0008 rule, one more row |
| a list entry without `name`, two entries with one name, or a `name` that is not an identifier | the name IS the evaluator's identity in findings, the trail, `/stats` and the budget registry |
| `provider:` naming a key absent from `analyzer.providers` | a lane that classifies against nothing must not start |
| `providers.<k>` without `model` or without `credentials_file` where the provider has no ambient credential | the same rule the root section already enforces, per entry |
| `mode: shadow` as the only entry on a lane, with no enforcing analyzer to compare against | a rehearsal of nothing; `shadow.disagreement` can never be written, and the operator almost certainly meant `warn` |
| `mode:` other than `shadow` or unset | there is one dry-run word; `observe` belongs to `guardrails.mode` and means something else there (it wraps the whole chain) |

### What this ADR does not decide

- **Response masking.** A Choice per result-set column over column names
  (never values), cached per `(tables, column)`, producing a synthesized
  `columns:` mask rule — is the obvious next use and is out of scope. It
  touches `gate/` and the mask path, which today never leaves the process,
  and it picks between real alternatives. It gets its own ADR; this one
  only guarantees the provider and the `Asker` contract it would build on.
- **Structured `state`.** Jev accepts a JSON object as state, and a
  rendering like `{operation, tables, statement}` may classify better than
  the text `Builder`s emit. Not now: the string is what every builder
  produces, what `Redact` scans, and what the cache keys on. Revisit with
  measurements.
- **Score questions.** Severity-as-a-continuum is tempting but the action
  map is discrete and Choice already yields probabilities per level. A
  Score adds a second way to say the same thing.

## Consequences

### What we win

**"Hold what the model is unsure about" becomes one line of YAML.** Today
`high: require_review` pages a human for every high. With
`min_confidence: 0.6, uncertain: require_review`, a confident high still
blocks, a confident low still passes, and only the statements the model
itself cannot place go to a person. That is the review queue an approver
wants: short, and full of genuinely ambiguous requests. The same floor
turns `high: block` into "block when sure, hold when not" without an OPA
deployment, which is the exact gap ADR-0008 left open under "two cost
controls exist and only one is OPA-free".

**Guardrails the operator can write in plain language, per lane, in one
call.** `questions:` is the seam ADR-0015 reserved. An operator who wants
"no mass delete", "no unbounded export of personal data" and "schema
changes need a review" writes three Nouls with three actions and three
thresholds. They are evaluated independently — no context rot between
them, no prompt engineering to keep one from drowning another — and the
whole battery costs one round trip at roughly the latency of one question.
The local `guardrails:` rules stay the deterministic, free first line; the
battery is the semantic second line for what a regex cannot say.

**The audit trail stops being prose-free by policy and becomes
prose-free by construction.** `Title` and `Explanation` are empty on the
`Asker` path because the model produces none. Nothing in the response can
carry a customer's taxpayer id into the trail or the error frame. The
`promptContract` stays for LLM providers; for this one, the property it
asks for is a property of the wire.

**Every verdict is a number OPA can reason about.** `confidence` and
per-question probabilities land in `input.findings.ai_analysis.values`, so
a Rego policy can express "treat the model's medium as high on
`customers` when confidence is under 0.5" — a rule that combines the
actor, the table and the model's own certainty, which no per-rule action
table will ever say. Dashboards get a distribution instead of three
buckets; `/stats` can report how much traffic sits in the uncertain band,
which is the number that tells an operator whether their floor is right.

**Cheaper and faster per statement.** Input-only pricing at $0.042/Mtok
against generation models charging for output tokens too; a decision model
answers in a fraction of the time a generation model takes to produce a
tool call with a title and an explanation. `DefaultTimeout` can drop from
10s to low single digits per lane, which shortens the worst-case hold on a
proxied connection during a vendor slowdown. Trigger, cache and
`max_calls` all still apply, so the bill's upper bound is unchanged and its
typical value is lower.

**A provider that reports which model answered.** Every response names the
versioned id. Pinning `jev-1.13.0` and logging the field means a threshold
tuned this quarter keeps meaning the same thing next quarter, and a
deliberate upgrade is a config edit an operator can correlate with a
change in the trail.

**The migration is a measurement, not a leap.** A lane adds one shadow
entry and keeps deciding with the model it trusts today. After a week the
trail holds, per statement, what the incumbent did and what the candidate
would have done, and `/stats` holds the agreement rate. `shadow.disagreement`
rows are the reading list: each names both evaluators and both levels, so
the review is "open these forty rows" rather than "re-run a month of
traffic". Promotion is deleting `mode: shadow`; rollback is adding it back.
Nothing in this loop needs a second lane, a second listener port, a replay
harness or a redeploy — the block is inside the ADR-0014 hot-reload
boundary, so the whole trial is a config edit.

**Each model does the job it is good at, on the same lane.** An LLM with
prose guidance still carries the nuanced "how risky is this" judgment where
an operator has spent weeks tuning wording; the decision model runs the
cheap, fast, calibrated battery beside it. The two do not compete for one
`provider:` field. The chain's short-circuit puts the cheaper entry first,
so the battery's refusals save the LLM its call, and the trail names which
evaluator produced each row's level because `ai_rule` now travels with it.

**Two vendors is a resilience story, not only a comparison one.** A lane
that enforces with two providers has two independent outages to survive
instead of one shared fate. It is not a failover — an entry whose provider
is down still reports `error` and follows its own `fail_open` — but the
other entry keeps deciding, which is more than a single-provider lane has
today. Pairing it with `fail_open: false` on the battery and `true` on the
risk entry, or the reverse, is a per-entry choice for the first time.

**`type: ai_analysis` can finally go.** The list entry is the "several
analyzers per lane" the deprecated rule form was the only spelling of. Its
removal release becomes a deletion with nothing to migrate that the block
form cannot express.

**Nothing an existing deployment relies on moves.** `anthropic`, `openai`
and `vertex` satisfy `Classifier` structurally and are not edited. The
action table, `defer`, `require_review`, `send`, `fail_open`, the trigger,
the cache ordering (redact before cache), the budget, the findings source
name, the `risk_level`/`risk_action`/`ai_status`/`ai_rule` keys: all
unchanged. A single-block lane on the root provider is byte-for-byte the
ADR-0015 behaviour. A config that never writes `provider: typesafe`,
`providers:` or a list under a lane's `analyzer:` cannot observe this ADR —
with one exception, the `ai_rule` merge fix, which changes a row only where
two evaluators already share a lane through the deprecated rule form and
the row was wrong.

### What gets harder, and what we commit to

**Thresholds are per model version.** A `min_confidence` or a
`threshold` tuned against `jev-1.13.0` is not a property of the config;
it is a property of the config and the model. The README will say so where
the fields are documented, recommend the versioned id over `jev-latest`,
and point at the logged `model` field as the thing to watch. We accept
that an operator who uses the alias can see their bands shift on a vendor
release.

**Two provider capabilities exist and config can name one the provider
lacks.** The refusal table above is the whole mitigation, and it must stay
complete: any future field that only an `Asker` can honor is refused on a
`Classifier` at startup, lane named. A control that loads and never runs is
the failure ADR-0008 and ADR-0015 both refuse elsewhere; we are extending
that rule, not making an exception to it.

**The battery is a bill multiplier the cache must contain.** Each question
adds input tokens to every uncached classification. The cache keys on the
statement shape plus the battery fingerprint, so a lane with a stable
battery pays per new shape, not per statement; a lane whose battery is
edited hourly pays for every edit. `-validate` will print the per-statement
question count beside the existing per-statement cost note.

**`ai_fired` and question ids are a new stable surface.** Renaming a
question id changes what dashboards see, the same way ADR-0015 noted
`ai_rule` changing value on migration. Ids are identifiers, refused
otherwise, so at least they cannot contain a space or a value.

**One vendor implements `Asker`.** Until a second decision model exists,
`Asker` is TypeSafe-shaped in practice. The interface is deliberately the
smallest thing the routing needs — a state, named typed questions, named
typed answers — and carries nothing from TypeSafe's SDK. If a second vendor
cannot fit it, the interface was wrong and this ADR is the one to
supersede.

**If the confidence turns out not to be calibrated for SQL, the routing
is still correct — it is the bands that move.** The design assumes
`confidence` orders statements by how sure the model is. If a specific
statement class is systematically over- or under-confident, the fix is a
threshold or a criteria rewording, not a code change; the numbers are in
the trail to find it. If it turns out not to order them at all, `uncertain`
degrades to a second name for whatever `Actions[level]` says, and a lane
that set `min_confidence: 1.0` gets the level table back exactly.

**Shadow is a rehearsal only as long as it writes nothing real.** The
`shadow.` prefix, the absent finding and the absent review are the whole
guarantee, and they are the kind of invariant that erodes one helpful
field at a time ("just fold the shadow confidence into stats", "just let
Rego see it as `ai_shadow`"). We commit to the line as drawn: a shadow
evaluator's only outputs are prefixed annotations and its own `/stats`
entry. Anything a policy can read or a session rollup can rise on is a
decision, and an entry that makes one is not in shadow.

**A disagreement is a signal, not a verdict.** `shadow.disagreement`
says the two models differed; it does not say which was right, and the
trial's reading step is a human reading rows. Publishing an agreement rate
without that step invites "92% agreement, ship it" when the 8% are the
`DROP`s. The README will say what the number can and cannot tell you.

**More evaluators is more spend, and each purse is separate on purpose.**
Three entries on a lane can make three calls per statement (two, once the
chain short-circuits on a denial). Each entry's `max_calls` bounds ITS
spend and nothing else, because a shared purse would let a runaway shadow
starve the entry that enforces. `-validate` prints the per-statement call
count for the lane so the multiplication is visible before it is billed.

**The root `providers:` map is restart-bound.** It holds credentials, so
it sits outside the hot-reload boundary with the root provider. Adding a
vendor to a running relay is a restart; moving an entry between vendors
already declared is a reload. That asymmetry is ADR-0014's and this ADR
inherits it rather than opening the credential path to a live edit.

### Phasing

1. **Provider only** (option 1 as a step): `analyzer/typesafe` behind
   `Classifier`, one Choice, level mapping, empty title. Wire shape,
   errors, retry, credential handling, live script under `scripts/dev/`.
2. **Named providers and entry lists:** `analyzer.providers`, the block-or-
   list lane form, `<lane>/<name>` identity, per-entry purses, the
   `ai_rule` triple in `mergeAnnotations`, `mode: shadow` with the
   `shadow.` annotation set and `shadow.disagreement`, `/stats` agreement
   counters, refusals. This lands second so a real lane can shadow Jev as
   a plain `Classifier` before any routing change ships.
3. **Contract split and confidence routing:** `Asker`, `Result.Confidence`,
   `min_confidence` / `uncertain`, `ai_confidence` annotation and finding
   value, refusals, README section.
4. **Battery:** `questions:`, Noul routing with thresholds and the review
   band, the precedence fold, `ai_fired`, per-question findings, refusals,
   `-validate` question count.

Each step is releasable alone and each is `minor`. Step 2 carries one
`patch`-shaped fix inside it (`ai_rule` merging) that is called out in its
PR.

### How this will be verified

Against `sidecar/` with an `httptest` server standing in for
`api.typesafe.ai` and the stub provider standing in for a `Classifier`.
Unit tests, `testing` only, alongside the code:

- **Provider:** request body carries `state`, `model` and the serialized
  battery; a Choice answer maps to the level; a `4xx` body containing a
  planted statement never appears in the returned error; `429` with
  `retry-after` retries once inside the deadline and not across it; the
  `model` field change is logged once.
- **Routing:** a high at confidence 0.9 blocks; the same high at 0.4 with
  `uncertain: require_review` files a review with the raw statement; a
  Noul at `p=0.85` blocks over a risk level that warns; a Noul at `p=0.5`
  in the review band holds; a cached `Result` re-applies routing (the cache
  collapses classifications, not approvals — unchanged); two evaluators on
  one lane fold to the highest level and the lowest confidence.
- **Coexistence:** a lane with `[risk (stub, enforcing), trial (typesafe,
  shadow)]` builds two evaluators named `appdb/risk` and `appdb/trial`; a
  statement the incumbent rates low and the shadow rates high forwards,
  carries `risk_level=low`, `ai_rule=appdb/risk`, `shadow.risk_level=high`,
  `shadow.risk_action=block`, `shadow.disagreement=appdb/risk=low,appdb/trial=high`,
  and reports ONE finding under `ai_analysis` with `rule: appdb/risk`; the
  session rollup stays low. The shadow entry with `require_review` files
  nothing and the `Review` func is never called. Two enforcing entries fold
  `ai_rule` with the level that won. A shadow entry listed first runs last.
  Each entry spends its own purse: `max_calls: 1` on one leaves the other
  classifying.
- **Reload boundary:** editing a lane's entry list changes `laneRuleDoc`
  and leaves `nonRuleDoc` byte-identical; editing `analyzer.providers`
  changes `nonRuleDoc`.
- **Refusals:** each row of the table above, via `hoop-inspect -validate`,
  naming the lane. A `Classifier` lane with neither `min_confidence` nor
  `questions` is byte-for-byte the behaviour it had before this ADR.
- **Trail:** `ai_confidence` and `ai_fired` appear on classified rows and
  nowhere else; no annotation value contains a byte of the statement,
  asserted with a planted marker.
- **Existing suites** (`analyzer`, `daemon`, `gate`, `policy`) pass
  unmodified except where a test asserted the old single-interface
  `Provider`, which is updated to `Classifier`.
- One live run through `scripts/dev/` against the real endpoint, results
  recorded in that directory's README the way the Vertex scripts are.
