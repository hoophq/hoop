// Step 1. A "No" here is a valid, recorded answer — TTFV is the moment an admin
// first says yes, so the gateway asks again after a cooldown.
export const REACHED_VALUE_QUESTION = 'Did you get done what you came here to do?'

// Step 2, asked only after a "Yes".
export const ACTIVITY_QUESTION = 'What did you get done today?'

// The `value` of each entry is the analytics contract shared with the gateway
// (the valid-activity slice in the POST /orgs/ttfv-survey handler) — keep both
// lists in sync and never repurpose an existing value, it would corrupt the
// historical time-to-first-value reports.
export const ACTIVITY_OPTIONS = [
  { value: 'connected-infra-resource', label: 'Connected an infra resource' },
  { value: 'approved-or-denied-access-request', label: 'Approved or denied an access request' },
  { value: 'reviewed-recorded-session', label: 'Reviewed a recorded session' },
  { value: 'created-or-activated-policy', label: 'Created or activated a policy (Guardrails)' },
  { value: 'opened-ai-analyzed-session-report', label: 'Opened an AI-analyzed session report' },
  { value: 'set-up-data-masking-rule', label: 'Set up a data masking rule' },
  { value: 'other', label: 'Other (please specify)' },
]

// The only option that also asks for free text.
export const ACTIVITY_OTHER = 'other'

// Matches the ttfv_survey_responses.activity_other cap the API enforces; longer
// values are rejected with a 400.
export const ACTIVITY_OTHER_MAX_LENGTH = 255
