import { Group } from '@mantine/core'
import SegmentedControl from '@/components/SegmentedControl'

// The view options row of a tab (Figma "View Options"): the views of the
// content on the left, its section actions (Download, Copy) on the right.
// One view draws no control.
export default function ViewOptions({ views = [], value, onChange, children }) {
  return (
    <Group justify="space-between" wrap="nowrap" gap="md">
      {views.length > 1 ? (
        <SegmentedControl size="sm" data={views} value={value} onChange={onChange} />
      ) : (
        <span />
      )}
      {children && (
        <Group gap="xs" wrap="nowrap">
          {children}
        </Group>
      )}
    </Group>
  )
}
