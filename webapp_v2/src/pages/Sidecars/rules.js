import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'
import { dataMaskingService } from '@/services/dataMasking'
import { guardrailsService } from '@/services/guardrails'

// The rule API behind each feature accordion: how its rules are listed, read
// back whole and rewritten, and the kind `bound_rules` files them under. A
// guardrail or masking rule is addressed by id, an analyzer rule by name.
export const RULE_APIS = {
  guardrails: {
    kind: 'guardrail',
    list: async () => (await guardrailsService.list()).data ?? [],
    get: (rule) => guardrailsService.get(rule.id),
    update: (rule, body) => guardrailsService.update(rule.id, body),
  },
  'data-masking': {
    kind: 'datamasking',
    list: async () => (await dataMaskingService.list()).data ?? [],
    get: (rule) => dataMaskingService.get(rule.id),
    update: (rule, body) => dataMaskingService.update(rule.id, body),
  },
  'ai-analyzer': {
    kind: 'analyzer',
    // The analyzer list answers a paginated envelope.
    list: async () => (await aiSessionAnalyzerService.listRules()).data?.data ?? [],
    get: (rule) => aiSessionAnalyzerService.getRule(rule.name),
    update: (rule, body) => aiSessionAnalyzerService.updateRule(rule.name, body),
  },
}

// The listeners a binding made here reaches. A rule binds to a listener, never
// to a sidecar, so the sidecar level fans out to every named lane.
export function targetLanes(sidecar, listener) {
  if (listener) return listener.name ? [listener.name] : []
  return (sidecar.configuration?.listeners ?? []).map((l) => l.name).filter(Boolean)
}

export const lanesAsTargets = (sidecar, lanes) =>
  lanes.map((listener_name) => ({ sidecar_id: sidecar.id, listener_name }))

// How many of `lanes` the rule already reaches.
export function boundCount(boundRules, kind, ruleName, lanes) {
  const on = new Set(
    (boundRules ?? []).filter((b) => b.kind === kind && b.rule_name === ruleName).map((b) => b.listener_name),
  )
  return lanes.filter((l) => on.has(l)).length
}

// The rule's targets with these lanes of this sidecar added or removed. What
// it reaches on other sidecars is kept, since the API replaces the whole set.
function withLanes(targets, sidecar, lanes, on) {
  const kept = (targets ?? []).filter((t) => !(t.sidecar_id === sidecar.id && lanes.includes(t.listener_name)))
  return on ? [...kept, ...lanesAsTargets(sidecar, lanes)] : kept
}

export const ruleErrorMessage = (error) =>
  error?.response?.data?.message || error?.message || 'The control plane refused the change.'

// One PUT per rule, carrying the rule as the API returned it plus the new
// target set: the request replaces every field it names, and a body missing
// `input` or `risk_evaluation` would blank them. Nothing here is atomic, so
// the caller gets the rules that failed; the rest are already written.
export async function applyBindings(api, sidecar, lanes, changes) {
  const failed = []
  for (const { rule, on } of changes) {
    try {
      const { data: stored } = await api.get(rule)
      await api.update(rule, {
        ...stored,
        // Required by the analyzer API even when empty; the read can answer null.
        ...(api.kind === 'analyzer' ? { connection_names: stored.connection_names ?? [] } : {}),
        sidecar_targets: withLanes(stored.sidecar_targets, sidecar, lanes, on),
      })
    } catch (error) {
      failed.push({ rule, error })
    }
  }
  return failed
}
