import { aiSessionAnalyzerService } from '@/services/aiSessionAnalyzer'
import { dataMaskingService } from '@/services/dataMasking'
import { guardrailsService } from '@/services/guardrails'
import { analyzerBlocked } from './config'
import { SOURCE_GATEWAY } from './features'

// Guardrail and masking rules are addressed by id, analyzer rules by name.
// `editPath` is the gateway's own form of the rule: a gateway rule reaching a
// listener through its mirror is edited there, never on a sidecar form.
export const RULE_APIS = {
  guardrails: {
    kind: 'guardrail',
    list: async () => (await guardrailsService.list()).data ?? [],
    get: (rule) => guardrailsService.get(rule.id),
    update: (rule, body) => guardrailsService.update(rule.id, body),
    editPath: (rule) => `/guardrails/edit/${rule.id}`,
  },
  'data-masking': {
    kind: 'datamasking',
    list: async () => (await dataMaskingService.list()).data ?? [],
    get: (rule) => dataMaskingService.get(rule.id),
    update: (rule, body) => dataMaskingService.update(rule.id, body),
    editPath: (rule) => `/features/data-masking/edit/${rule.id}`,
  },
  'ai-analyzer': {
    kind: 'analyzer',
    // The analyzer list answers a paginated envelope.
    list: async () => (await aiSessionAnalyzerService.listRules()).data?.data ?? [],
    get: (rule) => aiSessionAnalyzerService.getRule(rule.name),
    update: (rule, body) => aiSessionAnalyzerService.updateRule(rule.name, body),
    editPath: (rule) => `/features/ai-session-analyzer/rules/edit/${encodeURIComponent(rule.name)}`,
  },
}

// A binding only names the rule, so the record is found in the list first.
export async function findRule(api, ruleName) {
  const found = (await api.list()).find((r) => r.name === ruleName)
  if (!found) throw new Error(`Rule "${ruleName}" was not found.`)
  return found
}

// A rule binds to listeners, never to a sidecar, so the sidecar level fans out.
export function targetLanes(sidecar, listener, featureKey) {
  const config = sidecar.configuration
  if (featureKey === 'ai-analyzer' && analyzerBlocked(config)) return []
  return (listener ? [listener] : (config?.listeners ?? [])).map((l) => l?.name).filter(Boolean)
}

export const lanesAsTargets = (sidecar, lanes) =>
  lanes.map((listener_name) => ({ sidecar_id: sidecar.id, listener_name }))

const bindingsOf = (boundRules, kind, ruleName) =>
  (boundRules ?? []).filter((b) => b.kind === kind && b.rule_name === ruleName)

// Native bindings only (sidecar_targets). A gateway rule reaching a lane
// through its mirror carries source "gateway" and is not a target.
export function boundCount(boundRules, kind, ruleName, lanes) {
  const on = new Set(
    bindingsOf(boundRules, kind, ruleName)
      .filter((b) => b.source !== SOURCE_GATEWAY)
      .map((b) => b.listener_name),
  )
  return lanes.filter((l) => on.has(l)).length
}

// True when the rule reaches a lane in scope through a mirror and through
// nothing else: it is bound on its gateway form, so no target toggles it.
export function mirrorOnly(boundRules, kind, ruleName, lanes) {
  const rows = bindingsOf(boundRules, kind, ruleName).filter((b) => lanes.includes(b.listener_name))
  return rows.length > 0 && rows.every((b) => b.source === SOURCE_GATEWAY)
}

// Targets on other sidecars are kept: the API replaces the whole set.
function withLanes(targets, sidecar, lanes, on) {
  const kept = (targets ?? []).filter((t) => !(t.sidecar_id === sidecar.id && lanes.includes(t.listener_name)))
  return on ? [...kept, ...lanesAsTargets(sidecar, lanes)] : kept
}

export const ruleErrorMessage = (error) =>
  error?.response?.data?.message || error?.message || 'Hoop refused the change.'

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
