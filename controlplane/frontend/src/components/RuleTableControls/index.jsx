import { Group } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'

// New / Select / Select all / Delete row controls shared by every rule table
// (port of the legacy rule_buttons.cljs). On the free plan `disableNew` blocks
// adding a second rule per table, but selection/deletion stay available.
export default function RuleTableControls({
  onAdd,
  selectMode,
  onToggleSelectMode,
  allSelected,
  onToggleAll,
  onDelete,
  disableNew,
}) {
  return (
    <Group gap="sm">
      <Button
        type="button"
        variant="light"
        leftSection={<Plus size={14} />}
        onClick={onAdd}
        disabled={disableNew}
      >
        New
      </Button>
      <Button
        type="button"
        variant="light"
        color="gray"
        onClick={onToggleSelectMode}
      >
        {selectMode ? 'Cancel' : 'Select'}
      </Button>
      {selectMode && (
        <>
          <Button type="button" variant="light" color="gray" onClick={onToggleAll}>
            {allSelected ? 'Deselect all' : 'Select all'}
          </Button>
          <Button
            type="button"
            variant="light"
            color="red"
            leftSection={<Trash2 size={14} />}
            onClick={onDelete}
          >
            Delete
          </Button>
        </>
      )}
    </Group>
  )
}
