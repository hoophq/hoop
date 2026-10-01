import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'
import { dataMaskingService } from '@/services/dataMasking'
import { guardrailsService } from '@/services/guardrails'

// Guardrail and masking rules are addressed by id, analyzer rules by name.
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

// A rule binds to listeners, never to a sidecar, so the sidecar level fans out.
export function targetLanes(sidecar, listener) {
  if (listener) return listener.name ? [listener.name] : []
  return (sidecar.configuration?.listeners ?? []).map((l) => l.name).filter(Boolean)
}

export const lanesAsTargets = (sidecar, lanes) =>
  lanes.map((listener_name) => ({ sidecar_id: sidecar.id, listener_name }))

export function boundCount(boundRules, kind, ruleName, lanes) {
  const on = new Set(
    (boundRules ?? []).filter((b) => b.kind === kind && b.rule_name === ruleName).map((b) => b.listener_name),
  )
  return lanes.filter((l) => on.has(l)).length
}

// Targets on other sidecars are kept: the API replaces the whole set.
function withLanes(targets, sidecar, lanes, on) {
  const kept = (targets ?? []).filter((t) => !(t.sidecar_id === sidecar.id && lanes.includes(t.listener_name)))
  return on ? [...kept, ...lanesAsTargets(sidecar, lanes)] : kept
}

export const ruleErrorMessage = (error) =>
  error?.response?.data?.message || error?.message || 'The control plane refused the change.'

// The record read back whole plus the new targets, since PUT replaces every
// field it names. One request per rule, nothing atomic; failures are returned.
export async function applyBindings(api, sidecar, lanes, changes) {
  const failed = []
  // Unbinds first: a listener runs one analyzer, so a replacement is refused
  // while the old one is still bound.
  const ordered = [...changes].sort((a, b) => Number(a.on) - Number(b.on))
  for (const { rule, on } of ordered) {
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
