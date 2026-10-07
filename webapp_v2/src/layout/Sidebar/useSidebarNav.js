import { useLocation } from 'react-router-dom'
import { useUserStore } from '@/stores/useUserStore'
import { findActivePath, shouldHide, visibleNav } from './helpers'

// The sections the signed-in user may see, and the path of the active item.
export function useSidebarNav(nav) {
  const location = useLocation()
  const { isAdmin, isSelfHosted, role } = useUserStore()
  const isFeatureFlagEnabled = useUserStore((s) => s.isFeatureFlagEnabled)
  const isLicenseFeatureEnabled = useUserStore((s) => s.isLicenseFeatureEnabled)

  const sections = visibleNav(nav, (item) =>
    shouldHide(item, isAdmin, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled, role),
  )
  const activePath = findActivePath(sections, location.pathname, location.search)

  return { sections, activePath }
}
