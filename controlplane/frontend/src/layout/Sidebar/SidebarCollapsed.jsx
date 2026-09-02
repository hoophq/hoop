import { Stack, Box, Text, Tooltip, ScrollArea } from '@mantine/core'
import { ChevronsRight } from 'lucide-react'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { IconBtn } from './IconBtn'
import { shouldHide } from './helpers'
import { MAIN_ITEMS, REVIEW_ITEMS, FEATURE_ITEMS, ORGANIZATION_ITEMS } from './constants'

// Mirrors the labelled groups in SidebarExpanded, in the same order.
const SECTIONS = [
  { id: 'reviews', label: 'Reviews', items: REVIEW_ITEMS },
  { id: 'features', label: 'Features', items: FEATURE_ITEMS },
  { id: 'organization', label: 'Organization', items: ORGANIZATION_ITEMS },
]
import classes from './Sidebar.module.css'

export function SidebarCollapsed() {
  const toggleSidebarCollapsed = useUIStore((s) => s.toggleSidebarCollapsed)
  const { isAdmin, isApprover, isSelfHosted } = useUserStore()
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
          {MAIN_ITEMS.filter((i) => !shouldHide(i, isAdmin, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled, isApprover)).map((item) => (
            <Box component="li" key={item.path || item.label} className={classes.listItem}>
              <IconBtn {...item} />
            </Box>
          ))}
        </Stack>

        {isAdmin && SECTIONS.map(({ id, label, items }) => (
          <Box key={id} mt="xxl" w="100%">
            <Text size="xs" fw={600} mb="xs" className={classes.sectionHidden}>{label}</Text>
            <Stack gap="xsAlt" align="center" role="list" aria-label={label}>
              {items.filter((i) => !shouldHide(i, isAdmin, isSelfHosted, isFeatureFlagEnabled, isLicenseFeatureEnabled, isApprover)).map((item) => (
                <Box component="li" key={item.path} className={classes.listItem}>
                  <IconBtn {...item} />
                </Box>
              ))}
            </Stack>
          </Box>
        ))}

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
