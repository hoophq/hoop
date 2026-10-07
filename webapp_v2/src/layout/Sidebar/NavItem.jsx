import { Link } from 'react-router-dom'
import { useEffect, useRef, useState } from 'react'
import { ChevronDown } from 'lucide-react'
import { useUIStore } from '@/stores/useUIStore'
import { ItemBadge } from './ItemBadge'
import { SidebarNavLink } from './SidebarNavLink'
import { containsPath } from './helpers'

const ICON_SIZE = 18

const childrenVariant = (item) => {
  if (!item.icon) return 'nested'
  return item.children.some((child) => child.icon) ? 'guide' : 'label'
}

// Opens on the active page, or when the collapsed rail asked for it.
function NavGroup({ item, activePath }) {
  const { pendingOpenSection, clearPendingOpenSection } = useUIStore()
  const requested = pendingOpenSection === item.label
  const hasActive = containsPath(item, activePath)

  const [opened, setOpened] = useState(hasActive || requested)
  const [hadActive, setHadActive] = useState(hasActive)
  if (hasActive !== hadActive) {
    setHadActive(hasActive)
    if (hasActive) setOpened(true)
  }

  const ref = useRef(null)
  useEffect(() => {
    if (!requested) return
    clearPendingOpenSection()
    ref.current?.scrollIntoView({ block: 'start' })
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <SidebarNavLink
      ref={ref}
      label={item.label}
      aria-label={item.label}
      leftSection={item.icon ? <item.icon size={ICON_SIZE} aria-hidden="true" /> : undefined}
      rightSection={<ChevronDown size={16} aria-hidden="true" />}
      opened={opened}
      onChange={setOpened}
      childrenVariant={childrenVariant(item)}
    >
      {item.children.map((child) => (
        <NavItem key={child.path || child.label} item={child} activePath={activePath} />
      ))}
    </SidebarNavLink>
  )
}

// `item` is already filtered by the user's gates (useSidebarNav).
export function NavItem({ item, activePath }) {
  const setSidebarOpen = useUIStore((s) => s.setSidebarOpen)

  if (item.children) return <NavGroup item={item} activePath={activePath} />

  const active = item.path === activePath

  return (
    <SidebarNavLink
      component={Link}
      to={item.path}
      label={item.label}
      aria-label={item.label}
      aria-current={active ? 'page' : undefined}
      leftSection={item.icon ? <item.icon size={ICON_SIZE} aria-hidden="true" /> : undefined}
      rightSection={<ItemBadge badge={item.badge} shortcut={item.shortcut} />}
      active={active}
      onClick={() => setSidebarOpen(false)}
    />
  )
}
