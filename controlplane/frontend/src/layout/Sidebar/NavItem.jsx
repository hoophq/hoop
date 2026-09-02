import { Link, useLocation } from 'react-router-dom'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { ItemBadge } from './ItemBadge'
import { SidebarNavLink } from './SidebarNavLink'
import { shouldHide, isActive } from './helpers'

// The gateway sidebar also supported items with `children`, rendered as a
// collapsible group (Integrations, Settings). The control plane navigation is flat,
// so that path is gone along with the pendingOpenSection state it needed. Bring it
// back with the page that needs sub-navigation, not before.

export function NavItem({ item, role, isSelfHosted }) {
  const location = useLocation()
  const setSidebarOpen = useUIStore((s) => s.setSidebarOpen)
  const isFeatureFlagEnabled = useUserStore((s) => s.isFeatureFlagEnabled)
  const isLicenseFeatureEnabled = useUserStore((s) => s.isLicenseFeatureEnabled)

  if (shouldHide(item, role, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled)) return null

  const active = item.path ? isActive(item.path, location.pathname, location.search) : false
  const closeMobile = () => setSidebarOpen(false)

  if (item.action) {
    return (
      <SidebarNavLink
        label={item.label}
        aria-label={item.label}
        leftSection={item.icon ? <item.icon size={18} aria-hidden="true" /> : undefined}
        rightSection={<ItemBadge badge={item.badge} shortcut={item.shortcut} />}
        onClick={() => { item.action(); closeMobile(); }}
      />
    )
  }

  return (
    <SidebarNavLink
      component={Link}
      to={item.path}
      label={item.label}
      aria-label={item.label}
      aria-current={active ? 'page' : undefined}
      leftSection={item.icon ? <item.icon size={18} aria-hidden="true" /> : undefined}
      rightSection={<ItemBadge badge={item.badge} shortcut={item.shortcut} />}
      active={active}
      onClick={closeMobile}
    />
  )
}
