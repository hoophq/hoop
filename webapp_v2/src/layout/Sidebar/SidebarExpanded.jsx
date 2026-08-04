import { Stack, Box, Text, ScrollArea } from '@mantine/core'
import { ChevronsLeft } from 'lucide-react'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { NavItem } from './NavItem'
import { ConfigStatus } from './ConfigStatus'
import { MAIN_ITEMS, DISCOVER_ITEMS, ORGANIZATION_ITEMS } from './constants'
import classes from './Sidebar.module.css'

function SectionLabel({ label, id }) {
  return (
    <Text id={id} size="xs" fw={700} mb="sm">
      {label}
    </Text>
  )
}

export function SidebarExpanded({ navKey }) {
  const { toggleSidebarCollapsed } = useUIStore()
  const { isAdmin, isSelfHosted } = useUserStore()

  const navItemProps = { isAdmin, isSelfHosted }

  return (
    <Stack
      component="nav"
      aria-label="Primary"
      gap={0}
      h="100%"
      style={{ boxSizing: 'border-box', overflow: 'hidden' }}
    >
      <Box mb="xl" mt="xl" className={classes.logoExpanded}>
        <img
          src="/images/hoop-branding/PNG/hoop-symbol+text_black@4x.png"
          alt="Hoop"
          width={160}
          style={{ display: 'block' }}
        />
      </Box>

      <ScrollArea
        key={navKey}
        scrollbars="y"
        type="hover"
        scrollbarSize={10}
        classNames={{ root: classes.expandedScrollArea }}
      >
        <Box px="md">
          <ConfigStatus />

          <Box component="ul" role="list" aria-labelledby="sidebar-main-heading" className={classes.navList}>
            <Stack gap="xsAlt" mb="sm">
              {MAIN_ITEMS.map(item =>
                <Box component="li" key={item.path || item.label} className={classes.listItem}>
                  <NavItem item={item} {...navItemProps} />
                </Box>
              )}
            </Stack>
          </Box>

          {isAdmin && (
            <Box component="ul" role="list" aria-labelledby="sidebar-discover-heading" mt="xl" className={classes.navList}>
              <SectionLabel label="Discover" id="sidebar-discover-heading" />
              <Stack gap="xsAlt" mb="sm">
                {DISCOVER_ITEMS.map(item =>
                  <Box component="li" key={item.path} className={classes.listItem}>
                    <NavItem item={item} {...navItemProps} />
                  </Box>
                )}
              </Stack>
            </Box>
          )}

          {isAdmin && (
            <Box
              component="ul"
              role="list"
              aria-labelledby="sidebar-organization-heading"
              mt="xl"
              className={classes.navList}
            >
              <SectionLabel label="Organization" id="sidebar-organization-heading" />
              <Stack gap="xsAlt" mb="sm">
                {ORGANIZATION_ITEMS.map(item =>
                  <Box component="li" key={item.path || item.label} className={classes.listItem}>
                    <NavItem item={item} {...navItemProps} />
                  </Box>
                )}
              </Stack>
            </Box>
          )}
        </Box>
      </ScrollArea>

      <button aria-label="Collapse sidebar" className={classes.collapseBtn} onClick={toggleSidebarCollapsed}>
        <ChevronsLeft size={24} aria-hidden="true" />
      </button>
    </Stack>
  )
}
