import { GUARDRAIL_TEMPLATES_BY_SUBTYPE } from '@/pages/Guardrails/templates'
import { MASKING_TEMPLATES } from '@/pages/Features/DataMasking/templates'
import { AI_ANALYZER_TEMPLATES_BY_SUBTYPE } from '@/pages/Features/AiSessionAnalyzer/templates'

// The activation-journey cards of the success step, ported from
// webapp/src/webapp/features/activation_journey (templates.cljs, subs.cljs).
// The order is a product requirement: Guardrails, Live Data Masking, AI
// Session Analyzer.
export const FEATURES = [
  {
    feature: 'guardrails',
    label: 'Guardrails',
    genericDescription: 'Create custom rules to guide and protect usage within your resources.',
    createPath: '/guardrails/new',
    listPath: '/guardrails',
    connectionsParam: 'ids',
  },
  {
    feature: 'masking',
    label: 'Live Data Masking',
    genericDescription:
      'Matches PII in query results and redacts it before the response reaches the client.',
    createPath: '/features/data-masking/new',
    listPath: '/features/data-masking',
    connectionsParam: 'ids',
  },
  {
    feature: 'ai-analyzer',
    label: 'AI Session Analyzer',
    genericDescription:
      'Reviews every session for suspicious activity. Stops operations that exceed your risk limit.',
    createPath: '/features/ai-session-analyzer/rules/new',
    listPath: '/features/ai-session-analyzer',
    connectionsParam: 'names',
  },
]

// Templates carry the ?template= id in `name`; masking keeps it in `id`.
function templatesFor(feature, subtype) {
  if (feature === 'guardrails') return GUARDRAIL_TEMPLATES_BY_SUBTYPE[subtype] ?? []
  if (feature === 'ai-analyzer') return AI_ANALYZER_TEMPLATES_BY_SUBTYPE[subtype] ?? []
  return MASKING_TEMPLATES.map((t) => ({ ...t, name: t.id }))
}

function enabledForRoles(feature, rules, { connectionIds, connectionNames }) {
  if (feature === 'ai-analyzer') {
    const names = new Set(connectionNames)
    return rules.some((rule) => (rule.connection_names ?? []).some((n) => names.has(n)))
  }
  const ids = new Set(connectionIds)
  return rules.some((rule) => (rule.connection_ids ?? []).some((id) => ids.has(id)))
}

// One card, decided in order:
// 1. feature already enabled for a created role → done card, links to the list
// 2. provider gate fails → list page, no template
// 3. free plan and the feature configured → locked, Talk to Sales
// 4. free plan → first recommended template
// 5. enterprise → next template not configured yet (by rule name)
// 6. enterprise, all configured → create page, no template
function buildCard({ feature, label, genericDescription }, templates, ctx) {
  const { free, gateOk, enabled, configured, configuredNames } = ctx
  if (enabled) {
    return {
      feature,
      enabled: true,
      badge: { label: 'Enabled', color: 'green' },
      title: label,
      description: 'Already enabled for this resource role.',
      cta: { kind: 'list', label: `Go to ${label}` },
    }
  }
  const template = free
    ? templates[0]
    : templates.find((t) => !configuredNames.has(t.name))
  const locked = gateOk && free && configured
  let cta
  if (!gateOk) cta = { kind: 'list', label: `Go to ${label}` }
  else if (locked) cta = { kind: 'talk-to-sales', label: 'Talk to Sales' }
  else if (template) cta = { kind: 'create-with-template', label: `Go to ${label}`, templateId: template.name }
  else cta = { kind: 'create', label: `Go to ${label}` }
  let badge = null
  if (locked) badge = { label: 'Upgrade to unlock', color: 'gray' }
  else if (free) badge = { label: 'Included in free plan', color: 'green' }
  return {
    feature,
    locked,
    badge,
    title: template?.title ?? label,
    description: template?.cardDescription ?? genericDescription,
    cta,
  }
}

// lists: { guardrails, masking, 'ai-analyzer' } rule arrays; gates: same keys,
// whether the feature's provider is set up.
export function buildCards({ subtype, free, gates, lists, connectionIds, connectionNames }) {
  return FEATURES.map((info) => {
    const rules = lists[info.feature] ?? []
    return buildCard(info, templatesFor(info.feature, subtype), {
      free,
      gateOk: Boolean(gates[info.feature]),
      enabled: enabledForRoles(info.feature, rules, { connectionIds, connectionNames }),
      configured: rules.length > 0,
      configuredNames: new Set(rules.map((r) => r.name)),
    })
  })
}

// Where a card's button goes. The created roles ride along in ?connections=.
export function ctaPath(card, { connectionIds, connectionNames }) {
  const info = FEATURES.find((f) => f.feature === card.feature)
  if (card.cta.kind === 'list') return info.listPath
  if (card.cta.kind === 'create') return info.createPath
  const params = new URLSearchParams({ template: card.cta.templateId })
  const connections = info.connectionsParam === 'ids' ? connectionIds : connectionNames
  if (connections.length > 0) params.set('connections', connections.join(','))
  return `${info.createPath}?${params.toString()}`
}
