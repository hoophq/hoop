// Step 1. A "No" here is a valid, recorded answer — TTFV is the moment an admin
// first says yes, so the gateway asks again after a cooldown.
//
// "yet" is load-bearing and should survive any rewording: the metric is the
// first time value was ever reached, not whether it was reached in this visit.
// Scoping the question to the session or the day would collect a truthful "No"
// from someone whose first value happened last week — and a "No" costs a
// cooldown, so it delays the very measurement this asks for. The product is
// deliberately not named: naming it invites a verdict on the product, where
// what we need to know is whether the admin accomplished their own goal.
export const REACHED_VALUE_QUESTION = 'Have you gotten what you needed yet?'

// Step 2, asked only after a "Yes". "so far" is cumulative for the same reason.
export const ACTIVITY_QUESTION = 'What have you gotten done so far?'

// Every option is a moment the product did something *for* the admin, never one
// where they configured it. Connecting a resource, writing a policy and defining
// a masking rule are all setup — costs the admin pays up front — so counting
// them as value would measure "finished configuring", which is the thing TTFV
// exists to be distinguished from. The guardrail and masking entries are
// deliberately phrased as the rule being seen to apply, not as the rule being
// created. Anything new here has to clear the same bar.
//
// The `value` of each entry is the analytics contract shared with the gateway
// (the valid-activity slice in the POST /orgs/ttfv-survey handler) — keep both
// lists in sync and never repurpose an existing value, it would corrupt the
// historical time-to-first-value reports.
export const ACTIVITY_OPTIONS = [
  { value: 'saw-guardrail-applied', label: 'Saw a guardrail apply to a live session' },
  { value: 'saw-data-masked', label: 'Saw sensitive data masked in a session' },
  { value: 'approved-or-denied-access-request', label: 'Approved or denied an access request' },
  { value: 'reviewed-recorded-session', label: 'Reviewed a recorded session' },
  { value: 'opened-ai-analyzed-session-report', label: 'Opened an AI-analyzed session report' },
  { value: 'other', label: 'Other (please specify)' },
]

// The only option that also asks for free text.
export const ACTIVITY_OTHER = 'other'

// Matches the ttfv_survey_responses.activity_other cap the API enforces; longer
// values are rejected with a 400.
export const ACTIVITY_OTHER_MAX_LENGTH = 255
