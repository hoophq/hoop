import { Stack, Box, Tooltip, ScrollArea, Divider } from '@mantine/core'
import { ChevronsRight } from 'lucide-react'
import { useUIStore } from '@/stores/useUIStore'
import { IconBtn } from './IconBtn'
import { containsPath } from './helpers'
import { useSidebarNav } from './useSidebarNav'
import classes from './Sidebar.module.css'

// Width of an icon button; the rule between blocks matches it.
const RAIL_ITEM_WIDTH = 40

export function SidebarCollapsed({ nav }) {
  const { toggleSidebarCollapsed, setPendingOpenSection } = useUIStore()
  const { sections, activePath } = useSidebarNav(nav)

  // A group has no page of its own: the rail expands the sidebar with it open.
  const renderItem = (item) => (
    <Box component="li" key={item.path || item.label} className={classes.listItem}>
      <IconBtn
        icon={item.icon}
        label={item.label}
        path={item.path}
        active={containsPath(item, activePath)}
        onClick={
          item.children
            ? () => {
                setPendingOpenSection(item.label)
                toggleSidebarCollapsed()
              }
            : undefined
        }
      />
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
        {sections.map(({ id, label, block, items }, index) => (
          <Box key={id} w="100%" mt={index > 0 && block === sections[index - 1].block ? 'md' : undefined}>
            {index > 0 && block !== sections[index - 1].block && (
              <Divider color="gray.1" my="sm" w={RAIL_ITEM_WIDTH} mx="auto" />
            )}
            <Stack gap="xsAlt" align="center" role="list" aria-label={label}>
              {items.filter((item) => item.icon).map(renderItem)}
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
