import { GUARDRAIL_RULE_TYPES } from '@/pages/sidecarRuleVocabulary'

export const SIDECAR_ONLY = 'Sidecar only'
export const AGENT_ONLY = 'Agent only'

// The two guardrail engines the agent runs, on statements and on results.
export const AGENT_GUARDRAIL_TYPES = ['deny_words_list', 'pattern_match']

// Why the agent cannot run this guardrail rule. Empty = it can.
export function guardrailAgentBlockers(rule) {
  const out = []
  if (!AGENT_GUARDRAIL_TYPES.includes(rule.type)) {
    const label = GUARDRAIL_RULE_TYPES.find((t) => t.value === rule.type)?.label ?? rule.type
    out.push(`${label} rules`)
  }
  if (rule.type !== 'operation' && (rule.operations ?? []).length > 0) out.push('an operations scope')
  if (rule.action === 'defer') out.push('reporting a finding instead of denying')
  return out
}

// Why the agent cannot run this masking rule. Empty = it can.
export function maskingAgentBlockers(rule) {
  return rule.match === 'columns' ? ['matching by column name'] : []
}

const AGENT_SKIPS = {
  guardrail: {
    verb: ['uses', 'use'],
    rest: 'a sidecar-only option, so the agents of the selected resource roles and attributes skip it and enforce the other rules.',
  },
  datamasking: {
    verb: ['matches', 'match'],
    rest: 'by column, so the agents of the selected resource roles and attributes skip it and mask with the other rules.',
  },
}

// Text of the form alert when a rule bound to agents carries an option they cannot run.
export function agentSkipsMessage(kind, blockedIndexes) {
  const { verb, rest } = AGENT_SKIPS[kind]
  const numbers = blockedIndexes.map((i) => i + 1)
  if (numbers.length === 1) return `Rule ${numbers[0]} ${verb[0]} ${rest}`
  const last = numbers.pop()
  return `Rules ${numbers.join(', ')} and ${last} ${verb[1]} ${rest}`
}

// Text of the form alert when the stored agent half is not what the rule list
// would write: the admin edited it through the API or Terraform.
export const AGENT_HALF_DIFFERS = {
  guardrail:
    'The statement rules the agents run differ from the rules below (set through the API or Terraform). Saving rewrites them from the rules below.',
  datamasking:
    'The entity types the agents mask differ from the rules below (set through the API or Terraform). Saving rewrites them from the rules below.',
}
