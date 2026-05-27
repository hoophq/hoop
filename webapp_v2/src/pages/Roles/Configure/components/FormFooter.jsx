import { Group, Button, Text } from '@mantine/core'
import { useMediaQuery } from '@mantine/hooks'
import { ArrowLeft } from 'lucide-react'
import { useUIStore } from '@/stores/useUIStore'
import classes from './FormFooter.module.css'

// Sidebar widths come from layout/Layout.jsx. Mirror the constants
// rather than import from Layout to keep this component decoupled from
// the AppShell internals.
const SIDEBAR_WIDTH = 310
const SIDEBAR_COLLAPSED_WIDTH = 72

// Sticky-feel footer for the Configure Role form. Currently rendered
// inline at the bottom of the page (the CLJS version is also non-sticky).
//
// Layout: [Back] ............ [Delete] [Save]
// Delete is admin-only and styled as a red transparent button. Save is the
// primary CTA. Dirty state is surfaced as a subtle "Unsaved changes" hint
// when there are staged edits, so users always know whether Save is needed.
export default function FormFooter({
  saving,
  deleting,
  dirty,
  onBack,
  onDelete,
  onSave,
}) {
  const sidebarCollapsed = useUIStore((s) => s.sidebarCollapsed)
  const isDesktop = useMediaQuery('(min-width: 769px)')
  const leftOffset = isDesktop
    ? sidebarCollapsed ? SIDEBAR_COLLAPSED_WIDTH : SIDEBAR_WIDTH
    : 0

  return (
    <Group
      justify="space-between"
      align="center"
      pos="fixed"
      bottom={0}
      left={leftOffset}
      right={0}
      className={classes.root}
    >
      <Button
        variant="default"
        leftSection={<ArrowLeft size={16} />}
        onClick={onBack}
      >
        Back
      </Button>

      <Group gap="md">
        {dirty && (
          <Text size="sm" c="dimmed">Unsaved changes</Text>
        )}
        <Button
          variant="transparent"
          color="red"
          loading={deleting}
          onClick={onDelete}
        >
          Delete
        </Button>
        <Button loading={saving} onClick={onSave}>
          Save
        </Button>
      </Group>
    </Group>
  )
}
