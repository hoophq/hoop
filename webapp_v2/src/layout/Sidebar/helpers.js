import { hasRole } from '@/utils/roles'

// `adminOnly` is the gate both products share; `role` (utils/roles) is the
// control plane's. An item may carry either. hasRole lets an admin through every
// role gate; the gateway never passes a role, so nothing changes there.
// `sidecars` follows useSidecarsEnabled() (modes/sidecars).
export function shouldHide(item, isAdmin, isSelfHosted = false, isFeatureFlagEnabled = null, isLicenseFeatureEnabled = null, userRole = null, sidecarsEnabled = false) {
  if (item.adminOnly && !isAdmin) return true
  if (item.role && !hasRole(userRole, item.role)) return true
  if (item.selfhostedOnly && !isSelfHosted) return true
  if (item.featureFlag && isFeatureFlagEnabled && !isFeatureFlagEnabled(item.featureFlag)) return true
  if (item.licenseFeature && isLicenseFeatureEnabled && !isLicenseFeatureEnabled(item.licenseFeature)) return true
  if (item.sidecars && !sidecarsEnabled) return true
  return false
}

// A `linkWhenSingle` group left with one child renders as a link to that child.
function visibleGroup(item, children) {
  if (!item.linkWhenSingle || children.length !== 1) return { ...item, children }
  const { children: _, linkWhenSingle: __, ...link } = item
  return { ...link, path: children[0].path }
}

// Drops hidden items, empty groups and empty sections. `block` keeps a rule
// between two blocks when the section that opens one is hidden.
export function visibleNav(sections, hide) {
  const visibleItems = (items) =>
    items
      .filter((item) => !hide(item))
      .map((item) => (item.children ? visibleGroup(item, visibleItems(item.children)) : item))
      .filter((item) => !item.children || item.children.length > 0)

  let block = 0
  return sections
    .map((section, index) => {
      if (index > 0 && section.divider) block += 1
      return { ...section, block, items: visibleItems(section.items) }
    })
    .filter((section) => section.items.length > 0)
}

// The base path must match and every query param the item declares must be in
// the URL. A longer path, then more params, scores higher.
function matchScore(path, pathname, search) {
  if (!path) return 0
  const [basePath, queryString] = path.split('?')
  if (pathname !== basePath && !pathname.startsWith(basePath + '/')) return 0
  const params = [...new URLSearchParams(queryString)]
  const current = new URLSearchParams(search)
  if (!params.every(([key, value]) => current.get(key) === value)) return 0
  return basePath.length * 100 + params.length + 1
}

const leaves = (items) => items.flatMap((item) => (item.children ? leaves(item.children) : [item]))

// The most specific match wins: `/reviews?status=settled` over `/reviews`.
export function findActivePath(sections, pathname, search = '') {
  let best = null
  let bestScore = 0
  for (const item of leaves(sections.flatMap((section) => section.items))) {
    const score = matchScore(item.path, pathname, search)
    if (score > bestScore) {
      best = item.path
      bestScore = score
    }
  }
  return best
}

export const containsPath = (item, path) =>
  Boolean(path) && leaves(item.children ?? [item]).some((leaf) => leaf.path === path)
