import { Group, Paper, Stack } from '@mantine/core'

// `tone` picks the Paper surface: neutral (muted), info or warning. The icon
// sits in a box as tall as one line of the text, so it centres on the first
// line whatever the copy wraps to.
const SURFACES = { neutral: 'muted', info: 'info', warning: 'warning' }

export default function Callout({ icon: Icon, tone = 'neutral', action, children }) {
  return (
    <Paper variant={SURFACES[tone]} radius="md" p="sm">
      <Group gap="xs" align="flex-start" wrap="nowrap">
        <Group h={20} align="center" flex="0 0 auto">
          <Icon size={16} aria-hidden="true" />
        </Group>
        <Stack gap={4} flex={1}>
          {children}
        </Stack>
        {action}
      </Group>
    </Paper>
  )
}
