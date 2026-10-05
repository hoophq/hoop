export const TRAFFIC_AGENT = 'agent'
export const TRAFFIC_SIDECAR = 'sidecar'
export const TRAFFIC_BOTH = 'both'

export const RULE_KIND_GUARDRAIL = 'guardrail'
export const RULE_KIND_DATAMASKING = 'datamasking'
export const RULE_KIND_ANALYZER = 'analyzer'

const nonEmpty = (value) => Array.isArray(value) && value.length > 0

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
    Object.values(r.risk_evaluation ?? {}).some((action) => action && action !== 'allow_execution'),
}

export function ruleTraffic(kind, rule) {
  if (rule?.sidecar_spec == null) return TRAFFIC_AGENT
  return AGENT_FIELDS[kind](rule) ? TRAFFIC_BOTH : TRAFFIC_SIDECAR
}

// `traffics` is what useRuleTraffics() answers. With one, the page needs no data to pick.
export function resolveRuleTraffic(traffics, kind, rule) {
  return traffics.length === 1 ? traffics[0] : ruleTraffic(kind, rule)
}

export function newRulePath(basePath, traffics, traffic) {
  return traffics.length > 1 ? `${basePath}?traffic=${traffic}` : basePath
}
