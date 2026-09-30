import { Group, Stack } from '@mantine/core'

// The icon sits in a box as tall as one line of the text, so it centres on
// the first line whatever the copy wraps to.
export default function Callout({ icon: Icon, color, action, children }) {
  return (
    <Group gap="xs" align="flex-start" wrap="nowrap" p="sm" bg={color} bdrs="md">
      <Group h={20} align="center" flex="0 0 auto">
        <Icon size={16} aria-hidden="true" />
      </Group>
      <Stack gap={4} flex={1}>
        {children}
      </Stack>
      {action}
    </Group>
  )
}
