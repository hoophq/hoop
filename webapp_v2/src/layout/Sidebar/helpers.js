import { hasRole } from '@/utils/roles'

// `adminOnly` is the gateway's gate; `role` (utils/roles) is the control plane's.
// An item may carry either. hasRole lets an admin through every role gate.
export function shouldHide(item, isAdmin, isSelfHosted = false, isFeatureFlagEnabled = null, isLicenseFeatureEnabled = null, userRole = null) {
  if (item.adminOnly && !isAdmin) return true
  if (item.role && !hasRole(userRole, item.role)) return true
  if (item.selfhostedOnly && !isSelfHosted) return true
  if (item.featureFlag && isFeatureFlagEnabled && !isFeatureFlagEnabled(item.featureFlag)) return true
  if (item.licenseFeature && isLicenseFeatureEnabled && !isLicenseFeatureEnabled(item.licenseFeature)) return true
  return false
}

export function isActive(path, pathname, search = '') {
  if (!path) return false
  if (path === '/dashboard') return pathname === '/dashboard' || pathname === '/'
  // Items may target a query state of a page (e.g. /jira-templates?tab=configuration).
  // Match the pathname against the base path, then require every query param the
  // item declares to be present in the current URL.
  const [basePath, queryString] = path.split('?')
  const pathMatches = pathname === basePath || pathname.startsWith(basePath + '/')
  if (!pathMatches || !queryString) return pathMatches
  const current = new URLSearchParams(search)
  return [...new URLSearchParams(queryString)].every(([key, value]) => current.get(key) === value)
}
