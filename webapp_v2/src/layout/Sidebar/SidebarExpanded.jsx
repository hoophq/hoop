import { Stack, Box, Text, ScrollArea } from '@mantine/core'
import { ChevronsLeft } from 'lucide-react'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { NavItem } from './NavItem'
import { ConfigStatus } from './ConfigStatus'
import { shouldHide } from './helpers'
import { useModeConfig } from '@/modes'
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

export function SidebarExpanded({ navKey }) {
  const { toggleSidebarCollapsed } = useUIStore()
  const { isAdmin, isSelfHosted } = useUserStore()
  const isFeatureFlagEnabled = useUserStore((s) => s.isFeatureFlagEnabled)
  const isLicenseFeatureEnabled = useUserStore((s) => s.isLicenseFeatureEnabled)
  const { nav, shell } = useModeConfig()

  const navItemProps = { isAdmin, isSelfHosted }

  // NavItem returns null for a hidden entry, but its <li> wrapper would survive
  // and the Stack would still lay out a gap around it — a non-admin saw a double
  // gap where Dashboard sits between Resources and Terminal. Filter here, like
  // the collapsed rail already does.
  const visible = (items) =>
    items.filter((i) => !shouldHide(i, isAdmin, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled))

  // No profile block here any more: the user menu lives in the global header
  // (layout/Header/UserMenu.jsx), so user/gatewayVersion/logout moved with it.
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
        classNames={{ root: classes.expandedScrollArea, viewport: classes.scrollFill }}
      >
        <Box px="md" className={classes.scrollContent}>
          {shell.configStatus && <ConfigStatus />}

          {/* One list per section of the mode's nav. A section whose items are
              all hidden renders nothing, heading included. */}
          {nav.map(({ id, label, items }) => {
            const shown = visible(items)
            if (shown.length === 0) return null
            const headingId = label ? `sidebar-${id}-heading` : undefined
            return (
              <Box
                key={id}
                component="ul"
                role="list"
                aria-labelledby={headingId}
                aria-label={label ? undefined : 'Main navigation'}
                mt={label ? 'xl' : undefined}
                className={classes.navList}
              >
                {label && <SectionLabel label={label} id={headingId} />}
                <Stack gap="xsAlt" mb="sm">
                  {shown.map((item) => (
                    <Box component="li" key={item.path || item.label} className={classes.listItem}>
                      <NavItem item={item} {...navItemProps} />
                    </Box>
                  ))}
                </Stack>
              </Box>
            )
          })}
        </Box>
      </ScrollArea>

      <button aria-label="Collapse sidebar" className={classes.collapseBtn} onClick={toggleSidebarCollapsed}>
        <ChevronsLeft size={24} aria-hidden="true" />
      </button>
    </Stack>
  )
}
