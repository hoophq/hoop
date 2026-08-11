// Answers of the onboarding "How did you hear about Hoop?" survey. The `value`
// of each entry is the analytics contract shared with the gateway
// (validSignupOrigins in gateway/api/user/originsurvey.go) — keep both lists in
// sync and never repurpose an existing value, it would corrupt the historical
// acquisition-channel reports.
export const ORIGIN_OPTIONS = [
  { value: 'search-engine', label: 'Search on Google or another search engine' },
  { value: 'ai-discovery', label: 'AI discovery (Claude, ChatGPT, etc.)' },
  { value: 'referral', label: 'Referral from someone (colleague, friend)' },
  { value: 'already-in-use-at-company', label: 'It was already in use at the company I work for' },
  { value: 'tech-community', label: 'Tech community (Hacker News, Reddit, etc.)' },
  { value: 'social-media', label: 'Social media (LinkedIn, X/Twitter, etc.)' },
  { value: 'hoop-free-tools', label: "Using one of Hoop's free tools" },
  { value: 'other', label: 'Other (please specify)' },
]

// The only option that also asks for free text.
export const ORIGIN_OTHER = 'other'

// Matches the users.signup_origin_other column width; the API rejects longer
// values with a 400.
export const ORIGIN_OTHER_MAX_LENGTH = 255

// Dismissing with the X hides the survey for the current tab session only. It
// comes back on the next visit while the user is still inside the 7 day window
// the gateway enforces, so a dismissal never costs us the answer permanently.
export const SNOOZE_STORAGE_KEY = 'hoop:origin-survey-snoozed'
