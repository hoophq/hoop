import { Stack, Box, Tooltip, ScrollArea, Divider } from '@mantine/core'
import { ChevronsRight } from 'lucide-react'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { IconBtn } from './IconBtn'
import { shouldHide } from './helpers'
import { NAV, FOOTER_NAV } from './controlPlaneNav'
import classes from './Sidebar.module.css'

// Width of an icon button; the rule between groups matches it.
const RAIL_ITEM_WIDTH = 40

export function SidebarCollapsed() {
  const { toggleSidebarCollapsed, setPendingOpenSection } = useUIStore()
  const { isAdmin, isSelfHosted, role } = useUserStore()
  const isFeatureFlagEnabled = useUserStore((s) => s.isFeatureFlagEnabled)
  const isLicenseFeatureEnabled = useUserStore((s) => s.isLicenseFeatureEnabled)

  const visible = (items) =>
    items.filter((i) => !shouldHide(i, isAdmin, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled, role))

  const sections = NAV.map((section) => ({ ...section, shown: visible(section.items) })).filter(
    (section) => section.shown.length > 0,
  )
  const footerItems = visible(FOOTER_NAV.items)

  // A group (Settings) has no page of its own: the rail expands the sidebar
  // with that group open instead.
  const renderItem = (item) =>
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

  return (
    <Stack
      component="nav"
      aria-label="Primary"
      gap={0}
      align="center"
      className={classes.collapsedNav}
    >
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
        {sections.map(({ id, label, shown }, index) => (
          <Box key={id} w="100%">
            {index > 0 && <Divider color="gray.2" my="sm" w={RAIL_ITEM_WIDTH} mx="auto" />}
            <Stack gap="xsAlt" align="center" role="list" aria-label={label}>
              {shown.map(renderItem)}
            </Stack>
          </Box>
        ))}

        {footerItems.length > 0 && (
          <Box w="100%" pt="sm" pb="sm" className={classes.profileFooter}>
            <Stack gap="xsAlt" align="center" role="list" aria-label="Settings">
              {footerItems.map(renderItem)}
            </Stack>
          </Box>
        )}
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
