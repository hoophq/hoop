import { Group, Button, Text } from '@mantine/core'
import { ArrowLeft } from 'lucide-react'
import classes from './FormFooter.module.css'

// Sticky-feel footer for the Configure Role form. Currently rendered
// inline at the bottom of the page (the CLJS version is also non-sticky).
//
// Layout: [Back] ............ [Delete] [Save]
// Delete is admin-only and styled as a red transparent button. Save is the
// primary CTA. Dirty state is surfaced as a subtle "Unsaved changes" hint
// when there are staged edits, so users always know whether Save is needed.
export default function FormFooter({
  isAdmin,
  saving,
  deleting,
  dirty,
  onBack,
  onDelete,
  onSave,
}) {
  return (
    <Group justify="space-between" align="center" mt="xxl" className={classes.root}>
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
        {isAdmin && (
          <Button
            variant="transparent"
            color="red"
            loading={deleting}
            onClick={onDelete}
          >
            Delete
          </Button>
        )}
        <Button loading={saving} onClick={onSave}>
          Save
        </Button>
      </Group>
    </Group>
  )
}
