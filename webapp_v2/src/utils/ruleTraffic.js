export const TRAFFIC_AGENT = 'agent'
export const TRAFFIC_SIDECAR = 'sidecar'
export const TRAFFIC_BOTH = 'both'

export const RULE_KIND_GUARDRAIL = 'guardrail'
export const RULE_KIND_DATAMASKING = 'datamasking'
export const RULE_KIND_ANALYZER = 'analyzer'

const nonEmpty = (value) => Array.isArray(value) && value.length > 0

// What the sidecar analyzer editor stores in the agent-only risk_evaluation.
const DEFAULT_RISK_ACTION = 'allow_execution'
const RISK_ACTIONS = ['low_risk_action', 'medium_risk_action', 'high_risk_action']

const AGENT_FIELDS = {
  [RULE_KIND_GUARDRAIL]: (r) =>
    nonEmpty(r.connection_ids) ||
    nonEmpty(r.attributes) ||
    nonEmpty(r.input?.rules) ||
    nonEmpty(r.output?.rules),
  [RULE_KIND_DATAMASKING]: (r) =>
    nonEmpty(r.connection_ids) ||
    nonEmpty(r.attributes) ||
    nonEmpty(r.supported_entity_types) ||
    nonEmpty(r.custom_entity_types),
  [RULE_KIND_ANALYZER]: (r) =>
    nonEmpty(r.connection_names) ||
    Boolean(r.custom_prompt) ||
    Boolean(r.agentic) ||
    RISK_ACTIONS.some((key) => (r.risk_evaluation?.[key] ?? DEFAULT_RISK_ACTION) !== DEFAULT_RISK_ACTION),
}

// GET /guardrails drops sidecar_spec (gateway/api/guardrails List), so a guardrail
// row cannot be classified until the list returns it.
export const LIST_CARRIES_SIDECAR_SPEC = {
  [RULE_KIND_GUARDRAIL]: false,
  [RULE_KIND_DATAMASKING]: true,
  [RULE_KIND_ANALYZER]: true,
}

export function ruleTraffic(kind, rule) {
  if (rule?.sidecar_spec == null) return TRAFFIC_AGENT
  return AGENT_FIELDS[kind](rule) ? TRAFFIC_BOTH : TRAFFIC_SIDECAR
}

// `traffics` is what useRuleTraffics() answers. With one, the page needs no data to pick.
export function resolveRuleTraffic(traffics, kind, rule) {
  return traffics.length === 1 ? traffics[0] : ruleTraffic(kind, rule)
}

export function newRulePath(basePath, traffic) {
  return `${basePath}?traffic=${traffic}`
}

const NEW_RULE_HINTS = {
  [TRAFFIC_AGENT]: 'Create one for agent resources.',
  [TRAFFIC_SIDECAR]: 'Create one for sidecar listeners.',
}

// `traffics` is what useNewRuleTraffics() answers.
export function newRuleHint(traffics) {
  return traffics.length === 1
    ? NEW_RULE_HINTS[traffics[0]]
    : 'Create one for agent resources or for sidecar listeners.'
}
