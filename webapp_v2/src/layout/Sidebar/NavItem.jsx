import { Link, useLocation } from 'react-router-dom'
import { useEffect } from 'react'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { ItemBadge } from './ItemBadge'
import { SidebarNavLink } from './SidebarNavLink'
import { shouldHide, isActive } from './helpers'

// ─── Collapsible nav item (Integrations / Settings) ───────────────────────
// Separate component so useEffect can run on mount to clear pendingOpenSection.

export function CollapsibleNavItem({ item, isAdmin, isSelfHosted, defaultOpened, onMount }) {
  useEffect(() => {
    onMount?.()
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <SidebarNavLink
      label={item.label}
      aria-label={item.label}
      leftSection={<item.icon size={24} aria-hidden="true" />}
      defaultOpened={defaultOpened}
    >
      {item.children.map((child) => (
        <NavItem key={child.path} item={child} isAdmin={isAdmin} isSelfHosted={isSelfHosted} />
      ))}
    </SidebarNavLink>
  )
}

// ─── Single expanded nav item ──────────────────────────────────────────────

export function NavItem({ item, isAdmin, isSelfHosted }) {
  const location = useLocation()
  const { setSidebarOpen, pendingOpenSection, clearPendingOpenSection } = useUIStore()
  const isFeatureFlagEnabled = useUserStore((s) => s.isFeatureFlagEnabled)
  const isLicenseFeatureEnabled = useUserStore((s) => s.isLicenseFeatureEnabled)

  if (shouldHide(item, isAdmin, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled)) return null

  const active = item.path ? isActive(item.path, location.pathname) : false
  const closeMobile = () => setSidebarOpen(false)

  if (item.children) {
    const shouldOpen = pendingOpenSection === item.label
    return (
      <CollapsibleNavItem
        item={item}
        isAdmin={isAdmin}
        isSelfHosted={isSelfHosted}
        defaultOpened={shouldOpen}
        onMount={shouldOpen ? clearPendingOpenSection : undefined}
      />
    )
  }

  if (item.action) {
    return (
      <SidebarNavLink
        label={item.label}
        aria-label={item.label}
        leftSection={item.icon ? <item.icon size={24} aria-hidden="true" /> : undefined}
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
      leftSection={item.icon ? <item.icon size={24} aria-hidden="true" /> : undefined}
      rightSection={<ItemBadge badge={item.badge} shortcut={item.shortcut} />}
      active={active}
      onClick={closeMobile}
    />
  )
}
