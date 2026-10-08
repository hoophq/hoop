import { Stack, Box, Text, ScrollArea, Divider } from '@mantine/core'
import { ChevronsLeft } from 'lucide-react'
import { useUIStore } from '@/stores/useUIStore'
import { NavItem } from './NavItem'
import { useSidebarNav } from './useSidebarNav'
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

// `top` renders above the first section (the gateway's ConfigStatus).
export function SidebarExpanded({ nav, navKey, top }) {
  const toggleSidebarCollapsed = useUIStore((s) => s.toggleSidebarCollapsed)
  const { sections, activePath } = useSidebarNav(nav)

  return (
    <Stack
      component="nav"
      aria-label="Primary"
      gap={0}
      className={classes.expandedNav}
    >
      <Box mb="xl" mt="xl" className={classes.logoExpanded}>
        <img
          src="/images/hoop-branding/PNG/hoop-symbol+text_black@4x.png"
          alt="Hoop"
          width={135}
          className={classes.logoImage}
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
          {top}

          {sections.map(({ id, label, heading = true, block, items }, index) => {
            const headingId = `sidebar-${id}-heading`
            const rule = index > 0 && block !== sections[index - 1].block
            return (
              <Box key={id}>
                {rule && <Divider color="gray.1" my="sm" />}
                <Box
                  component="ul"
                  role="list"
                  aria-labelledby={heading ? headingId : undefined}
                  aria-label={heading ? undefined : label}
                  mt={index > 0 && !rule ? 'md' : undefined}
                  className={classes.navList}
                >
                  {heading && <SectionLabel label={label} id={headingId} />}
                  <Stack gap="xsAlt" mb="sm">
                    {items.map((item) => (
                      <Box component="li" key={item.path || item.label} className={classes.listItem}>
                        <NavItem item={item} activePath={activePath} />
                      </Box>
                    ))}
                  </Stack>
                </Box>
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
