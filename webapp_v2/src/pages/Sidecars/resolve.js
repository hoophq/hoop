/**
 * What a listener actually runs, after the top-level defaults are merged in.
 *
 * This is a port of `Config.resolve` in sidecar/daemon/config.go. The three
 * sections do NOT merge the same way, and each has a spelling that means "run
 * none of it" as distinct from "inherit":
 *
 *   guardrails.rules  absent inherits · [] runs none · non-empty CONCATENATES,
 *                     the listener's own first
 *   guardrails.mode   the listener's replaces when non-empty; a resolved empty
 *                     mode enforces
 *   opa               the listener's REPLACES when set; `opa: {}` drops an
 *                     inherited endpoint
 *   mask.rules        PRESENCE replaces, `[]` included — see maskRules below
 *
 * Reading the presence of a whole `guardrails`/`mask` block instead of its
 * `rules` key reports a lane that overrides only `mode` as running nothing,
 * while the daemon is still applying the inherited rules.
 */

export const SOURCE_LISTENER = 'listener'
export const SOURCE_INHERITED = 'inherited'
// A rule the control plane distributes. It is not in the stored document at
// all -- composition folds it into the served one on each handshake -- so it
// arrives from the bindings beside the config, never from resolveListener.
export const SOURCE_DISTRIBUTED = 'distributed'

// Enforcement modes, GuardrailsConfig.Mode in sidecar/daemon/config.go.
export const MODE_ENFORCE = 'enforce'
export const MODE_OBSERVE = 'observe'

const list = (v) => (Array.isArray(v) ? v : [])
const tag = (rules, source) => list(rules).map((rule) => ({ rule, source }))

// A `type: ai_analysis` rule is the DEPRECATED spelling of the lane's analyzer
// block. The daemon sorts it out of the guardrail list and reports it beside
// the block instead (buildLanes, daemon.go:940), because the analyzer is a
// component that leaves the process and costs money, not a rule count.
const AI_ANALYSIS = 'ai_analysis'

/**
 * The guardrail rules this lane evaluates, in the order it evaluates them.
 *
 * Concatenation is monotonic in the allow/deny outcome — every rule type denies
 * and matching is first-match-wins — so order only decides which name and
 * message get reported. The listener's own come first so a lane's specific
 * message beats a generic default for the same statement.
 */
export function guardrailRules(listener, config) {
  const own = listener?.guardrails
  const inherited = tag(config?.guardrails?.rules, SOURCE_INHERITED)
  // `rules: null` is the nil slice the daemon reads as absent — and it is what
  // an operator leaves behind after commenting a lane's rules out, so the
  // reference config in deploy/docker-compose/envoy-stack writes it.
  if (own?.rules == null) return inherited
  if (own.rules.length === 0) return []
  return [...tag(own.rules, SOURCE_LISTENER), ...inherited]
}

// The guardrails proper, with the analyzer's deprecated rule form taken out.
export function guardrailMatchers(listener, config) {
  return guardrailRules(listener, config).filter((e) => e.rule?.type !== AI_ANALYSIS)
}

/**
 * The AI analyzer this lane runs, if any.
 *
 * Two spellings, and a lane can carry both while it migrates. `analyzer` is
 * the first-class per-lane block; a `type: ai_analysis` guardrail rule is the
 * older form, and it counts whether the lane wrote it or inherited it from the
 * top level. This mirrors LaneInfo in daemon.go: `Analyzer` is the lane's own
 * block, `Analyzed` counts the deprecated rules among the RESOLVED set.
 *
 * The top-level `analyzer` section is NOT the trigger. It is process-wide and
 * carries the provider, model and credential that any lane's analysis uses;
 * reading its presence as "this lane analyses" marks every lane in the fleet,
 * including ones that classify nothing.
 */
export function laneAnalyzer(listener, config) {
  const block = listener?.analyzer ?? null
  const deprecated = guardrailRules(listener, config).filter((e) => e.rule?.type === AI_ANALYSIS)
  return { block, deprecated, on: Boolean(block) || deprecated.length > 0 }
}

export function guardrailMode(listener, config) {
  return listener?.guardrails?.mode || config?.guardrails?.mode || MODE_ENFORCE
}

/**
 * The masking rules this lane rewrites responses with.
 *
 * The daemon's test is `o != nil && len(o.Rules) > 0`, and `MaskConfig.Rules` is
 * a json.RawMessage — so that length is in BYTES, not entries. The two bytes of
 * `[]` are not the nil slice, which is how a lane switches inherited masking
 * off, exactly as the field's own doc comment says. Reading it as "a non-empty
 * list replaces" instead reports masking on a lane that turned it off.
 */
/**
 * Whether masking is CONFIGURED on this lane, which is not the same question as
 * how many rules it resolves to.
 *
 * MaskConfig.hasRules is `len(m.Rules) > 0 && !isEmptyJSONList(m.Rules)` over a
 * json.RawMessage, so it counts BYTES: `rules: null` is four of them and reads
 * as configured, while `rules: []` is the one spelling that reads as off. The
 * resolved list below flattens null to an empty array — right for "what runs",
 * wrong for "does the daemon think masking is on", and the daemon's protocol
 * and descriptor checks ask the second question.
 */
export function maskConfigured(listener, config) {
  const own = listener?.mask
  const raw = own == null || !('rules' in own) ? config?.mask?.rules : own.rules
  if (raw === undefined) return false
  return !(Array.isArray(raw) && raw.length === 0)
}

export function maskRules(listener, config) {
  const own = listener?.mask
  // Presence of the KEY, not of a value, and this is where mask parts company
  // with guardrails. `mask: null`, or a block that never names rules, overrides
  // nothing and inherits. `mask: {rules: null}` is a present block: the four
  // bytes of `null` are not the nil slice either, so it replaces the inherited
  // set — with nothing, because null carries no rules.
  if (own == null || !('rules' in own)) return tag(config?.mask?.rules, SOURCE_INHERITED)
  return tag(own.rules, SOURCE_LISTENER)
}

// An empty block is the opt-out spelling: it drops an inherited endpoint rather
// than replacing it with an unbuildable one. `off()` in daemon/config.go tests
// every field for its zero value.
const opaOff = (opa) => !opa?.url && !opa?.timeout_sec && !opa?.fail_open && !opa?.gate

export function resolveOPA(listener, config) {
  const own = listener?.opa
  if (own === undefined || own === null) return config?.opa ?? null
  return opaOff(own) ? null : own
}

// Everything a lane resolves to, for a reader that wants all of it at once.
export function resolveListener(listener, config) {
  return {
    mode: guardrailMode(listener, config),
    guardrails: guardrailMatchers(listener, config),
    mask: maskRules(listener, config),
    opa: resolveOPA(listener, config),
    analyzer: laneAnalyzer(listener, config),
  }
}
