import { HOST_HINT } from '@/features/License/constants'
import {
  LICENSE_STATE,
  LICENSE_STATUS,
  daysUntilExpiration,
  formatLicenseDate,
  isHostNotAllowedError,
} from '@/utils/license'

// Wider than the global banner's 30 days: renewal happens here.
export const EXPIRATION_WARNING_DAYS = 90

export function licenseTypeLabel(type) {
  if (type === 'enterprise') return 'Enterprise License'
  if (type === 'oss') return 'Open Source License'
  return '—'
}

// The page notice, by state. `status` is the gateway's verdict; expire_at only
// says how soon, never whether. Covers an already expired license too.
export function licenseNotice(licenseInfo, state) {
  if (!licenseInfo) return null
  const { status, type, expire_at: expireAt, verify_error: verifyError } = licenseInfo

  if (status === LICENSE_STATUS.EXPIRED) {
    return {
      color: 'red',
      message: `Your license expired on ${formatLicenseDate(expireAt)}. Creating rules is blocked until a valid license is installed.`,
      action: 'support',
    }
  }

  if (status === LICENSE_STATUS.INVALID) {
    const reason = verifyError ? `: ${verifyError}` : ''
    const hint = isHostNotAllowedError(verifyError) ? ` ${HOST_HINT}` : ''
    return {
      color: 'red',
      message: `Your license could not be verified${reason}. Creating rules is blocked until a valid license is installed.${hint}`,
      action: 'support',
    }
  }

  if (state === LICENSE_STATE.FREE) {
    return {
      color: 'blue',
      message: 'The control plane is part of the Enterprise plan. Add your license to create sidecars and rules.',
      action: 'sales',
    }
  }

  if (type !== 'enterprise') return null
  const daysLeft = daysUntilExpiration(expireAt)
  if (daysLeft === null || daysLeft > EXPIRATION_WARNING_DAYS) return null
  return {
    color: 'amber',
    message: `Your license expires on ${formatLicenseDate(expireAt)}. Renew it to avoid interruption.`,
    action: 'support',
  }
}
