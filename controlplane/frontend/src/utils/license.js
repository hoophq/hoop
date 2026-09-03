// Set by the gateway clock — never re-derive "expired" from expire_at here.
export const LICENSE_STATUS = {
  VALID: 'valid',
  EXPIRED: 'expired',
  INVALID: 'invalid',
}

// Unix seconds -> "Sep 18, 2026", same format as the other Settings tables.
export function formatLicenseDate(timestamp) {
  if (!timestamp) return '—'
  return new Date(timestamp * 1000).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
  })
}

const DAY_MS = 24 * 60 * 60 * 1000

// Whole days left, rounded up so an expiry later today reads as 1, not 0.
export function daysUntilExpiration(expireAt) {
  if (!expireAt) return null
  return Math.ceil((expireAt * 1000 - Date.now()) / DAY_MS)
}

export const LICENSE_STATE = {
  UNKNOWN: 'unknown',
  FREE: 'free',
  ACTIVE: 'active',
  EXPIRED: 'expired',
  INVALID: 'invalid',
}

// The one place that interprets license_info. `status` is the gateway's verdict
// on its own clock. ACTIVE is the same predicate useUserStore uses for
// isFreeLicense. Everything else, including the default OSS license an
// unlicensed organization reports, is FREE.
export function licenseState(licenseInfo, serverInfoLoaded = true) {
  if (!serverInfoLoaded || !licenseInfo) return LICENSE_STATE.UNKNOWN
  if (licenseInfo.status === LICENSE_STATUS.EXPIRED) return LICENSE_STATE.EXPIRED
  if (licenseInfo.status === LICENSE_STATUS.INVALID) return LICENSE_STATE.INVALID
  if (licenseInfo.is_valid && licenseInfo.type === 'enterprise') return LICENSE_STATE.ACTIVE
  return LICENSE_STATE.FREE
}

// A license the organization already holds that no longer verifies. The call to
// action is "Update license", not "Talk to sales": they are a customer.
export function needsLicenseRenewal(licenseInfo) {
  const state = licenseState(licenseInfo)
  return state === LICENSE_STATE.EXPIRED || state === LICENSE_STATE.INVALID
}

// The gateway's VerifyHost message. It is the only signal that a rejection is
// about allowed_hosts and not about the signature.
export function isHostNotAllowedError(message) {
  return /is not allowed to use this license/.test(message ?? '')
}

export const LICENSE_FEATURE_LABELS = {
  guardrails: 'Guardrails',
  'data-masking': 'Live Data Masking',
  'ai-session-analyzer': 'AI Session Analyzer',
  'access-requests': 'Review',
}

// Callout copy for a blocked create action. Existing rules stay editable in
// every state, and the copy says so.
export function licenseRequiredMessage(featureLabel, state) {
  if (state === LICENSE_STATE.EXPIRED) {
    return `Your license expired. New ${featureLabel} rules are blocked until a valid license is installed.`
  }
  if (state === LICENSE_STATE.INVALID) {
    return `Your license could not be verified. New ${featureLabel} rules are blocked until a valid license is installed.`
  }
  return `Creating ${featureLabel} rules needs an Enterprise license. You can still edit and delete the rules you have.`
}
