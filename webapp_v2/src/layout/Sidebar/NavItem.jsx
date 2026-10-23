import { Link } from 'react-router-dom'
import { useEffect, useRef, useState } from 'react'
import { Group } from '@mantine/core'
import { ChevronDown } from 'lucide-react'
import { useUIStore } from '@/stores/useUIStore'
import { ItemBadge } from './ItemBadge'
import { SidebarNavLink } from './SidebarNavLink'
import { containsPath } from './helpers'
import classes from './Sidebar.module.css'

const ICON_SIZE = 18

const childrenVariant = (item) => {
  if (!item.icon) return 'nested'
  return item.children.some((child) => child.icon) ? 'guide' : 'label'
}

const ariaLabel = (label, count) => (count > 0 ? `${label} (${count})` : label)

// Opens on the active page, or when the collapsed rail asked for it.
function NavGroup({ item, activePath, counts }) {
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

  const count = counts?.[item.count]

  return (
    <SidebarNavLink
      ref={ref}
      label={item.label}
      aria-label={ariaLabel(item.label, count)}
      leftSection={item.icon ? <item.icon size={ICON_SIZE} aria-hidden="true" /> : undefined}
      rightSection={
        <Group gap="smAlt" wrap="nowrap">
          <ItemBadge count={count} />
          <ChevronDown size={16} aria-hidden="true" className={classes.groupChevron} data-opened={opened || undefined} />
        </Group>
      }
      disableRightSectionRotation
      opened={opened}
      onChange={setOpened}
      childrenVariant={childrenVariant(item)}
    >
      {item.children.map((child) => (
        <NavItem key={child.path || child.label} item={child} activePath={activePath} counts={counts} />
      ))}
    </SidebarNavLink>
  )
}

// `item` is already filtered by the user's gates (useSidebarNav).
export function NavItem({ item, activePath, counts }) {
  const setSidebarOpen = useUIStore((s) => s.setSidebarOpen)

  if (item.children) return <NavGroup item={item} activePath={activePath} counts={counts} />

  const active = item.path === activePath
  const count = counts?.[item.count]

  return (
    <SidebarNavLink
      component={Link}
      to={item.path}
      label={item.label}
      aria-label={ariaLabel(item.label, count)}
      aria-current={active ? 'page' : undefined}
      leftSection={item.icon ? <item.icon size={ICON_SIZE} aria-hidden="true" /> : undefined}
      rightSection={<ItemBadge badge={item.badge} count={count} shortcut={item.shortcut} />}
      active={active}
      onClick={() => setSidebarOpen(false)}
    />
  )
}
