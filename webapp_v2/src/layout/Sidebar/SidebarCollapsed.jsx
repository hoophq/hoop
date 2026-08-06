import { Stack, Box, Text, Tooltip, ScrollArea, Group } from '@mantine/core'
import { ChevronsRight } from 'lucide-react'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { IconBtn } from './IconBtn'
import { getUserInitials, shouldHide } from './helpers'
import { MAIN_ITEMS, DISCOVER_ITEMS, ORGANIZATION_ITEMS } from './constants'
import classes from './Sidebar.module.css'

export function SidebarCollapsed({ skipLink }) {
  const { toggleSidebarCollapsed, setPendingOpenSection } = useUIStore()
  const { user, isAdmin, isSelfHosted } = useUserStore()
  const isFeatureFlagEnabled = useUserStore((s) => s.isFeatureFlagEnabled)
  const isLicenseFeatureEnabled = useUserStore((s) => s.isLicenseFeatureEnabled)

  return (
    <Stack
      component="nav"
      aria-label="Primary"
      gap={0}
      align="center"
      className={classes.collapsedNav}
    >
      {skipLink}

      <Box mb="xl" mt="xl" className={classes.logoCollapsed}>
        {/* The symbol SVG carries a viewBox but no width/height, so both axes
            are given here — with only a height it has no layout width to fall
            back on if the asset fails to load. viewBox is square. */}
        <img
          src="/images/hoop-branding/SVG/hoop-symbol_black.svg"
          alt="Hoop"
          width={24}
          height={24}
          style={{ display: 'block' }}
        />
      </Box>

      <ScrollArea
        scrollbars="y"
        type="hover"
        scrollbarSize={10}
        classNames={{ root: classes.collapsedScrollArea, viewport: classes.scrollFill }}
      >
        <Stack gap={2} align="center" role="list" aria-label="Main navigation">
          {MAIN_ITEMS.filter((i) => !shouldHide(i, isAdmin, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled)).map((item) => (
            <Box component="li" key={item.path || item.label} className={classes.listItem}>
              <IconBtn {...item} />
            </Box>
          ))}
        </Stack>

        {isAdmin && (
          <Box mt="xxl" w="100%">
            <Text size="xs" fw={600} mb="xs" className={classes.sectionHidden}>Discover</Text>
            <Stack gap="xsAlt" align="center" role="list" aria-label="Discover">
              {DISCOVER_ITEMS.filter((i) => !shouldHide(i, isAdmin, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled)).map((item) => (
                <Box component="li" key={item.path} className={classes.listItem}>
                  <IconBtn {...item} />
                </Box>
              ))}
            </Stack>
          </Box>
        )}

        {isAdmin && (
          <Box mt="xxl" w="100%">
            <Text size="xs" fw={600} mb="xs" className={classes.sectionHidden}>Organization</Text>
            <Stack gap="xsAlt" align="center" role="list" aria-label="Organization">
              {ORGANIZATION_ITEMS.filter((i) => !shouldHide(i, isAdmin, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled)).map((item) =>
                item.children ? (
                  <Box component="li" key={item.label} className={classes.listItem}>
                    <IconBtn
                      icon={item.icon}
                      label={item.label}
                      onClick={() => {
                        setPendingOpenSection(item.label)
                        toggleSidebarCollapsed()
                      }}
                    />
                  </Box>
                ) : (
                  <Box component="li" key={item.path} className={classes.listItem}>
                    <IconBtn {...item} />
                  </Box>
                )
              )}
            </Stack>
          </Box>
        )}

        {/* margin-top:auto — drops to the bottom when the nav list is short
            (non-admin), scrolls along with it when it is not (admin). */}
        <Group justify="center" pt="lg" pb="sm" className={classes.profileFooter}>
          <Tooltip label={user?.name || user?.email || 'Profile'} position="right" withArrow>
            <Box
              role="button"
              tabIndex={0}
              aria-label="Open user menu"
              className={`${classes.avatar} ${classes.avatarClickable}`}
              onClick={() => {
                setPendingOpenSection('__profile__')
                toggleSidebarCollapsed()
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  setPendingOpenSection('__profile__')
                  toggleSidebarCollapsed()
                }
              }}
            >
              {getUserInitials(user)}
            </Box>
          </Tooltip>
        </Group>
      </ScrollArea>

      <div className={classes.collapsedFooter}>
        <Tooltip label="Expand sidebar" position="right" withArrow>
          <button
            aria-label="Expand sidebar"
            className={classes.iconBtn}
            onClick={toggleSidebarCollapsed}
          >
            <ChevronsRight size={24} aria-hidden="true" />
            <span className={classes.srOnly}>Expand sidebar</span>
          </button>
        </Tooltip>
      </div>
    </Stack>
  )
}
