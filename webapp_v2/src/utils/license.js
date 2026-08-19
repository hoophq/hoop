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
