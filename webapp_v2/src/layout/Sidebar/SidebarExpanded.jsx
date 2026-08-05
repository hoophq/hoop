import { Stack, Box, Text, ScrollArea } from '@mantine/core'
import { ChevronsLeft } from 'lucide-react'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { useAuthStore } from '@/stores/useAuthStore'
import { useNavigate } from 'react-router-dom'
import { NavItem } from './NavItem'
import { ProfileDisclosure } from './ProfileDisclosure'
import { ConfigStatus } from './ConfigStatus'
import { shouldHide } from './helpers'
import { MAIN_ITEMS, DISCOVER_ITEMS, ORGANIZATION_ITEMS } from './constants'
import classes from './Sidebar.module.css'

// Left padding lives in the CSS module — it carries an optical correction that
// no spacing token can express. See .sectionLabel.
function SectionLabel({ label, id }) {
  return (
    <Text id={id} size="xs" fw={700} mb="sm" className={classes.sectionLabel}>
      {label}
    </Text>
  )
}

export function SidebarExpanded({ skipLink, navKey }) {
  const navigate = useNavigate()
  const { toggleSidebarCollapsed } = useUIStore()
  const { user, isAdmin, isSelfHosted, gatewayVersion } = useUserStore()
  const isFeatureFlagEnabled = useUserStore((s) => s.isFeatureFlagEnabled)
  const isLicenseFeatureEnabled = useUserStore((s) => s.isLicenseFeatureEnabled)
  const { logout } = useAuthStore()

  const navItemProps = { isAdmin, isSelfHosted }

  // NavItem returns null for a hidden entry, but its <li> wrapper would survive
  // and the Stack would still lay out a gap around it — a non-admin saw a double
  // gap where Dashboard sits between Resources and Terminal. Filter here, like
  // the collapsed rail already does.
  const visible = (items) =>
    items.filter((i) => !shouldHide(i, isAdmin, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled))

  const handleLogout = () => {
    logout()
    navigate('/login')
  }

  return (
    <Stack
      component="nav"
      aria-label="Primary"
      gap={0}
      h="100%"
      style={{ boxSizing: 'border-box', overflow: 'hidden' }}
    >
      {skipLink}

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
        classNames={{ root: classes.expandedScrollArea, viewport: classes.scrollFill }}
      >
        <Box px="md" className={classes.scrollContent}>
          <ConfigStatus />

          <Box component="ul" role="list" aria-labelledby="sidebar-main-heading" className={classes.navList}>
            <Stack gap="xsAlt" mb="sm">
              {visible(MAIN_ITEMS).map(item =>
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
                {visible(DISCOVER_ITEMS).map(item =>
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
                {visible(ORGANIZATION_ITEMS).map(item =>
                  <Box component="li" key={item.path || item.label} className={classes.listItem}>
                    <NavItem item={item} {...navItemProps} />
                  </Box>
                )}
              </Stack>
            </Box>
          )}

          {/* margin-top:auto — drops to the bottom when the nav list is short
              (non-admin), scrolls along with it when it is not (admin). */}
          <Box pt="lg" pb="sm" className={classes.profileFooter}>
            <ProfileDisclosure user={user} onLogout={handleLogout} gatewayVersion={gatewayVersion} />
          </Box>
        </Box>
      </ScrollArea>

      <button aria-label="Collapse sidebar" className={classes.collapseBtn} onClick={toggleSidebarCollapsed}>
        <ChevronsLeft size={24} aria-hidden="true" />
      </button>
    </Stack>
  )
}
