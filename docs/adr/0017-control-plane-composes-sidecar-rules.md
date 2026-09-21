# ADR-0017: The control plane composes rules into the served sidecar config

- **Status:** Proposed
- **Date:** 2026-09-17
- **Author:** @rogerio
- **Deciders:** @rogerio
- **Supersedes / Superseded by:** amends ADR-0009

## Context

Guardrails, data masking and the AI session analyzer are configured inside
each sidecar's own document today. A company with one sidecar does not need
a control plane; a company with a thousand listeners cannot manage them one
file at a time. EVL-283 asks for the other shape: author once, distribute to
the fleet, and see whether the fleet took it.

The gateway already stores exactly these three rule kinds, for connections,
in `private.guardrail_rules`, `private.datamasking_rules` and
`private.ai_session_analyzer_rules`. The control plane is the same binary
(ADR-0013) and route parity already serves all three in control-plane mode.
What is missing is the association: a rule that names a sidecar listener
rather than a connection, and something that turns it into the document the
sidecar reads.

Four constraints bound the answer.

- **The sidecar decodes with `DisallowUnknownFields`, at three layers.** A
  key it does not declare refuses the WHOLE document, and it cannot recover
  on its own. Whatever composition writes has to be a key `daemon.Config`
  already has.
- **Only some sections hot-swap** (ADR-0014). `nonRuleDoc` strips top-level
  `guardrails`, `opa`, `mask`, `pii` and per-listener `guardrails`, `opa`,
  `mask`, `analyzer`; whatever survives is the baseline and drifting it makes
  the sidecar ask for a restart. Everything this ADR distributes must land
  inside that boundary, or every rule edit becomes a fleet restart.
- **The two engines do not speak the same rule vocabulary.** The sidecar
  knows eight guardrail types and the gateway two; the gateway has custom
  entity types and the sidecar cannot register a recognizer; the gateway has
  an output side and the sidecar denies requests and masks responses. ADR-0009
  looked at federating the two and declined.
- **Listener names are not unique** in `daemon.Validate`, which keys on the
  bind address, and may be absent entirely.

## Options considered

1. **A second rule store, shaped for the sidecar.** Honest about the
   vocabulary split, and doubles every rule surface: two pages, two APIs, two
   sets of seeded content, and an admin who has to know which is which. The
   Linear ask is explicitly to reuse what the gateway has.

2. **Materialise on write.** When a rule changes, rewrite the stored document
   of every sidecar it reaches. Reads stay trivial. Editing a rule bound to
   three hundred sidecars becomes a fan-out job with partial-write states, a
   reconciliation path, and a stored document that is no longer what any
   admin authored.

3. **Compose on read** (chosen). The rule rows stay where they are, a binding
   table points them at sidecars and listeners, and the served document is
   derived on every handshake.

4. **Attribute- or rulepack-keyed binding.** The indirection the gateway
   already uses for connections. Cut for V1: the sidecar config has no
   counterpart for either, so it would introduce a concept with nothing
   behind it.

## Decision

**Compose on read, at the seam that already exists, restricted to the
vocabulary subset both engines already spell identically.**

Three junction tables — one per feature, because only a real foreign key
makes a deleted rule drop its bindings and a polymorphic `rule_name` column
cannot carry one. Each row is `(org_id, rule_name, sidecar_id,
listener_name)`, with an empty listener meaning the whole sidecar.

`services.ComposeSidecarConfiguration` loads a sidecar's bindings and folds
them into a COPY of the stored configuration:

| Rule | Lands in | Merge |
|---|---|---|
| guardrail, sidecar-wide | `guardrails.rules` | append |
| guardrail, per listener | `listeners[i].guardrails.rules` | append |
| masking | `listeners[i].mask.rules` (or top-level) | replace |
| masking threshold | `pii.threshold` | one per process |
| analyzer | `listeners[i].analyzer` risk actions + prompt | overlay |

The asymmetry is the daemon's, not a choice here: `Config.resolve`
concatenates guardrails and replaces mask rules. Appending also preserves
`rules: []`, which is how a lane opts out of the defaults entirely.

`gateway/api/sidecar/sidecar.go` `withOrgLicense()` calls it before
`servedConfig()`. Both `Handshake` and `Configuration` inherit it — one
change, two endpoints, and they cannot drift.

**The restriction is a gate, not a convention.** Every write to a bound rule
is refused with a 422 naming the rule, the sidecar and what to do instead,
when it carries: a guardrail output rule or a rule type outside
`deny_words_list`/`pattern_match`; a `pattern_regex` Go's RE2 cannot compile;
a custom entity type; `require_access_request` (the sidecar declares
`require_review` and refuses it at startup — EVL-289); a listener with no
analyzer block; a listener name that does not resolve to exactly one lane; or
a second threshold on a sidecar that already has one. The guards re-run on
every write while a binding exists, because editing a compliant rule into a
non-compliant one would otherwise walk straight past them.

**Absent is not empty.** `sidecar_targets` is an optional pointer: omitting it
leaves the bindings alone, `[]` unbinds. Without that distinction, any write
that did not mention the field — a script fixing a typo, an MCP call, the
gateway's own UI — would silently unbind a rule from the whole fleet.

### Amending ADR-0009

ADR-0009 said nothing federates the two rule vocabularies, and nothing should
until someone wants both on one byte path. That still holds: nobody wants both
on one byte path here. What changes is that one rule row can now be authored
once and rendered into either engine, restricted to the subset ADR-0011
observed is spelled identically in both.

**This is the shared table's price, and it must be stated.** The gateway
validates nothing on write — the rule columns are bare JSONB — and
`gateway/transport/client.go` decodes through a four-field struct, so a
richer rule type would already reach an agent as `{"type":"table",
"words":null}` and match nothing forever. The day someone adds a seventh rule
type to that table for the gateway's own benefit, the write gate above is the
only thing standing between this feature and a silent fleet-wide no-op. A new
rule type is added on both sides or refused for bound rules.

## Consequences

**Good.**

- Editing a rule bound to three hundred sidecars is one row update. No
  fan-out, no partial fleet, no reconciliation.
- The stored document stays what an admin authored. `GET /sidecars/:id` and
  the listener editor keep meaning what they meant.
- Everything distributed sits inside the ADR-0014 hot-reload boundary, so a
  rule edit swaps into a running sidecar with no dropped connection. Pinned by
  a test that asserts `daemon.BaselineDoc` is byte-identical before and after
  composition — `BaselineDoc` is exported for exactly that, because a copy of
  the boundary on the gateway side would pass its own test and still be wrong
  the first time a `Config` field is added.
- A composed document round-trips through the control plane's own strict
  parser in a test, which is what proves no unknown key was introduced.

**Costs, accepted.**

- One query per sidecar per handshake, bounded by that sidecar's bindings, at
  one request per sidecar per minute.
- A rule bound to a listener that is later renamed makes the handshake answer
  an error rather than serve a document enforcing less than the admin sees
  bound. The sidecar keeps the rules it already has, so nothing is dropped,
  but the fleet stops converging until the binding is fixed. Skipping the
  binding silently was the alternative and is worse.
- The threshold is process-wide on a sidecar, so two bound masking rules
  asking for different sensitivities is refused rather than resolved. There is
  no correct answer to pick.
- One SSH or gRPC lane makes a drifted document restart-bound for the WHOLE
  sidecar (`reload.go`, `isEndpointLane`), so a rule edit hot-swaps only on a
  sidecar whose lanes are all relay lanes. The sidecar reports `last_outcome`
  and the fleet view names it; this ADR does not change it.

**Cut from V1, deliberately.** Attributes and rulepacks as a binding key; the
root `analyzer` provider block (restart-bound baseline, and `credentials_file`
is a path the plane cannot supply); guardrail output rules; the six richer
sidecar rule types; mask `columns`/`strategy`/`keep_last`; and
`guardrails.mode: observe`. The binding table resolves targets to a set of
`(sidecar, listener)` pairs either way, so an attribute-keyed row is additive
later.
