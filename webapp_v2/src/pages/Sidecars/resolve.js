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

// Enforcement modes, GuardrailsConfig.Mode in sidecar/daemon/config.go.
export const MODE_ENFORCE = 'enforce'
export const MODE_OBSERVE = 'observe'

const list = (v) => (Array.isArray(v) ? v : [])
const tag = (rules, source) => list(rules).map((rule) => ({ rule, source }))

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
    guardrails: guardrailRules(listener, config),
    mask: maskRules(listener, config),
    opa: resolveOPA(listener, config),
  }
}
