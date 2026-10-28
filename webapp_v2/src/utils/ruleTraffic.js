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

// The agent half of an analyzer rule. Guardrail and masking rules need no such
// test: their one form writes both halves, so field presence says nothing.
const AGENT_FIELDS = {
  [RULE_KIND_ANALYZER]: (r) =>
    nonEmpty(r.connection_names) ||
    Boolean(r.custom_prompt) ||
    Boolean(r.agentic) ||
    RISK_ACTIONS.some((key) => (r.risk_evaluation?.[key] ?? DEFAULT_RISK_ACTION) !== DEFAULT_RISK_ACTION),
}

// What binds a rule to agent resources, as opposed to what it says.
const AGENT_BINDINGS = {
  [RULE_KIND_GUARDRAIL]: (r) => nonEmpty(r.connection_ids) || nonEmpty(r.attributes),
  [RULE_KIND_DATAMASKING]: (r) => nonEmpty(r.connection_ids) || nonEmpty(r.attributes),
  [RULE_KIND_ANALYZER]: (r) => nonEmpty(r.connection_names),
}

// Where a rule runs is where it is bound: agent resources, sidecar listeners, or both.
export function ruleTraffic(kind, rule) {
  const agent = AGENT_BINDINGS[kind](rule ?? {})
  const sidecar = nonEmpty(rule?.sidecar_targets)
  if (agent && sidecar) return TRAFFIC_BOTH
  return sidecar ? TRAFFIC_SIDECAR : TRAFFIC_AGENT
}

// Which form edits a stored analyzer rule. A rule the agent runs opens the
// gateway form whatever its targets: that form binds them too. A sidecar rule
// that also carries agent fields has no form that keeps both halves.
export function ruleForm(kind, rule) {
  if (rule?.sidecar_spec == null) return TRAFFIC_AGENT
  return AGENT_FIELDS[kind](rule) ? TRAFFIC_BOTH : TRAFFIC_SIDECAR
}

// `traffics` is what useRuleTraffics() answers. With one, the page needs no data to pick.
export function resolveRuleForm(traffics, kind, rule) {
  return traffics.length === 1 ? traffics[0] : ruleForm(kind, rule)
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
