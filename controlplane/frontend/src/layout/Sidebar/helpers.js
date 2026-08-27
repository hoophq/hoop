export function shouldHide(item, isAdmin, isSelfHosted = false, isFeatureFlagEnabled = null, isLicenseFeatureEnabled = null) {
  if (item.adminOnly && !isAdmin) return true
  if (item.selfhostedOnly && !isSelfHosted) return true
  if (item.featureFlag && isFeatureFlagEnabled && !isFeatureFlagEnabled(item.featureFlag)) return true
  if (item.licenseFeature && isLicenseFeatureEnabled && !isLicenseFeatureEnabled(item.licenseFeature)) return true
  return false
}

export function isActive(path, pathname, search = '') {
  if (!path) return false
  // The start page redirects to Sidecars, so highlight it while "/" is on screen.
  // In webapp_v2 this special case named /dashboard, which does not exist here.
  if (path === '/sidecars') return pathname === '/sidecars' || pathname === '/'
  // Items may target a query state of a page (e.g. /jira-templates?tab=configuration).
  // Match the pathname against the base path, then require every query param the
  // item declares to be present in the current URL.
  const [basePath, queryString] = path.split('?')
  const pathMatches = pathname === basePath || pathname.startsWith(basePath + '/')
  if (!pathMatches || !queryString) return pathMatches
  const current = new URLSearchParams(search)
  return [...new URLSearchParams(queryString)].every(([key, value]) => current.get(key) === value)
}
