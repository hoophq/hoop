// The three risk tiers, in render order. `recommended` is pre-selected on a new
// rule and carries the "Recommended" badge.
export const RISK_LEVELS = [
  {
    key: 'low',
    field: 'low_risk',
    legacyField: 'low_risk_action',
    label: 'Low risk',
    description:
      'Activities that appear unlikely to cause significant system, data, or security impact based on intent and structure.',
    recommended: 'allow_execution',
  },
  {
    key: 'medium',
    field: 'medium_risk',
    legacyField: 'medium_risk_action',
    label: 'Medium risk',
    description:
      'Activities that may modify data, configuration, or runtime behavior in a scoped or limited way.',
    recommended: 'require_access_request',
  },
  {
    key: 'high',
    field: 'high_risk',
    legacyField: 'high_risk_action',
    label: 'High risk',
    description:
      'Activities that suggest potentially destructive, irreversible, privilege-altering, or security-sensitive behavior.',
    recommended: 'block_execution',
  },
]

export const REQUIRE_ACCESS_REQUEST = 'require_access_request'

// The API still returns the deprecated flat `<tier>_risk_action` alongside the
// nested `<tier>_risk`, so both are read, newest shape first.
export function tierFromRisk(riskEvaluation, level) {
  const tier = riskEvaluation?.[level.field]
  return {
    action: tier?.action ?? riskEvaluation?.[level.legacyField] ?? level.recommended,
    ruleName: tier?.access_request_rule_name ?? null,
  }
}

// Every tier of a rule, keyed for the form: { low: {action, ruleName}, ... }.
export function riskFromRule(rule) {
  return Object.fromEntries(
    RISK_LEVELS.map((level) => [level.key, tierFromRisk(rule?.risk_evaluation, level)]),
  )
}

// The rule name only belongs to the payload when the tier actually waits for an
// approval; the other two actions carry no rule.
function buildTier({ action, ruleName }) {
  return action === REQUIRE_ACCESS_REQUEST && ruleName
    ? { action, access_request_rule_name: ruleName }
    : { action }
}

// A require_access_request tier without a rule would be saved as an approval
// gate pointing at nothing, so the form blocks the save until one is picked.
export function hasIncompleteTier(risk) {
  return RISK_LEVELS.some((level) => {
    const tier = risk[level.key]
    return tier.action === REQUIRE_ACCESS_REQUEST && !tier.ruleName
  })
}

export function formToPayload({
  name,
  description,
  connectionNames,
  customPrompt,
  agentic,
  risk,
}) {
  const prompt = customPrompt.trim()
  return {
    name: name.trim(),
    // Both optional fields are sent as null rather than "" when left empty, so
    // the gateway stores an absent value instead of an empty string.
    description: description || null,
    connection_names: connectionNames,
    risk_evaluation: Object.fromEntries(
      RISK_LEVELS.map((level) => [level.field, buildTier(risk[level.key])]),
    ),
    custom_prompt: prompt || null,
    agentic,
  }
}
