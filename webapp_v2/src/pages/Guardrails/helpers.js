// Agents enforce guardrails through a DLP provider; has_redact_credentials
// says whether one is configured. Sidecars need none.
export const AGENT_DLP_REQUIRED =
  'Guardrails for agent resources need a DLP provider (Microsoft Presidio or Google Cloud DLP). Sidecars enforce guardrails without one.'
